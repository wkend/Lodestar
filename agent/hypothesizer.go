package agent

import (
	"context"
	"fmt"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
	"github.com/Autumn-27/norma/transcript"
)

// Hypothesizer is the second brain (Stage 6b): a situation-level agent woken on a
// slow heartbeat that reads the whole exploration graph and emits Hypothesis nodes
// (unverified conjectures) via record_hypothesis. It is structurally a sibling of
// Planner — same read-the-overview → produce-a-node pipeline — but with a distinct
// tool set (read-only + record_hypothesis, no add_intent/prove_goal) and no
// planner-only machinery (todos, kill/steer, goal judging, deadline wrapup). The
// planner consumes its output: open hypotheses fold into graph_overview and drive
// verification intents. It never calls t.notify — the planner sees hypotheses on
// its own next round (zero cross-loop notify races; see engine hypothesizerLoop).
type Hypothesizer struct {
	prov           llm.Provider
	model          string
	tx             *transcript.Store
	window         int
	maxTurns       int
	proxyAddr      string
	proxyCACert    string
	nonStreamingFn func() bool
	maxTokensFn    func() int
	workDir        string
}

func NewHypothesizer(prov llm.Provider, model, workDir string, tx *transcript.Store, window, maxTurns int) *Hypothesizer {
	return &Hypothesizer{prov: prov, model: model, workDir: workDir, tx: tx, window: window, maxTurns: maxTurns}
}

func (h *Hypothesizer) SetProxy(addr, caCert string)   { h.proxyAddr, h.proxyCACert = addr, caCert }
func (h *Hypothesizer) SetNonStreaming(fn func() bool) { h.nonStreamingFn = fn }
func (h *Hypothesizer) SetMaxTokens(fn func() int)     { h.maxTokensFn = fn }

func (h *Hypothesizer) nonStreaming() bool { return h.nonStreamingFn != nil && h.nonStreamingFn() }
func (h *Hypothesizer) maxTokens() int {
	if h.maxTokensFn == nil {
		return 0
	}
	return h.maxTokensFn()
}

// hypothesizerDefaultTmpl is the built-in EDITABLE body of the second brain's prompt.
const hypothesizerDefaultTmpl = `你是一个授权渗透测试系统的"态势研判者"（第二大脑）。你被周期性唤醒，任务【单一而明确】：通读整张探索图的当前态势，把散落在各处的【已确认事实】综合起来，提出 planner 可能没注意到的【跨点、跨资产的态势级假设】——供 planner 派意图去验证。

任务目标：{{.Goal}}

⚠️ **你的唯一产物是 hypothesis（假设），不是意图、不是发现、不是执行。** 你不派意图、不打目标、不动手测；你只读态势、做综合推断，然后用 record_hypothesis 记下值得验证的猜想。派意图和验证是 planner 与 worker 的事。

**什么是好的假设？** 它必须【综合多条已有事实】、指向一个【尚未被现有意图覆盖、又对目标有价值】的攻击/横向/提权方向。例如：
- "A 站点泄露的内网 IP 与 B 服务的回连地址一致 → 二者可能同主机，SSRF 可打 B 的管理口"；
- "多个接口都用同一种可预测的 id 编码 → 可能存在系统性 IDOR，值得挑高价值接口验证"。
**不是好的假设**：单条事实的复述、无事实支撑的凭空猜测、已被现有 open/running 意图覆盖的方向、已 confirmed/refuted 过的老假设。

决策流程（每次唤醒）：
1. **完整态势已附在下方**（graph_overview 预取，等同你调该工具）：goals、资产、open/running/recent_done 意图（含血缘 parents/yields）、recent_facts（最近事实，含否定结论与 from_intent）、findings、以及 **hypotheses（你之前已提出、仍 open 的假设——别重复提）**。需要某条的完整证据时调 node_detail(id) / list_facts。
2. **综合**：把 recent_facts 及更多事实横向串起来看，找出【单条事实看不出、多条一起才浮现】的模式或关联。重点看那些【现有意图还没往那个方向走】的组合。
3. **克制产出**：只对【确有事实支撑、且未被现有意图/已有假设覆盖】的方向调 record_hypothesis（parent_ids 传你综合的那些事实 id）。**没有新的、有价值的假设时，产出 0 条是完全正常的**——绝不要为了"有产出"而复述事实或硬凑猜测。宁可不提，也不要噪声。一次最多提 2-3 条最有价值的。

记住：你的价值在于【发现别人没注意到的关联】，不在于产量。图里没有值得综合的新东西时，直接结束本轮。`

func hypothesizerSystem(goal, workDir string) string {
	body := renderSystem("hypothesizer", hypothesizerDefaultTmpl, PlannerVars{Goal: goal, Now: nowStr()})
	return body + artifactSpec(workDir)
}

// Hypothesize runs one situation-assessment round: read the full overview, emit
// 0..N hypotheses. Mirrors Planner.Plan's session assembly. emit surfaces the
// second brain's steps to the UI (worker="hypothesizer").
func (h *Hypothesizer) Hypothesize(ctx context.Context, taskID int64, as *db.AssetStore, ts *db.ExplorationStore, goal string, emit func(db.Activity)) error {
	tsx := NewToolSet(ts, "hypothesizer")
	if as != nil {
		tsx.SetAssetStore(as, as.Companies())
	}
	tsx.SetTaskID(taskID)
	tsx.SetCoverageEnabled(as == nil || as.CoverageEnabled(taskID))
	if origin, _ := ts.OriginFactID(); origin > 0 {
		tsx.SetOwnerNode(origin) // 假设锚到任务根(origin fact)，与 planner 一致
	}
	base := append(tsx.DropCoverageTools(tsx.HypothesizerTools()), actool.DefaultTools()...)
	ctx = WithRunInfo(ctx, RunInfo{TaskID: taskID, ExplorationID: explorationID(ts)})
	tools, def, cleanup := AugmentTools(ctx, "hypothesizer", base)
	defer cleanup()

	situational := renderGraphOverview(tsx.graphOverviewData())
	taskDir := ensureRunDir(h.workDir, taskID, 0)
	sysBody := hypothesizerSystem(goal, taskDir)
	sysBody += constraintBlock(ts) // 操作约束框定假设边界（与 planner 一致）
	system, boundary := deferredSystem(sysBody, def)

	opts := agentcore.Options{
		Provider:        h.prov,
		SystemPrompt:    system,
		DynamicBoundary: boundary,
		Tools:           tools,
		DeferredTools:   def.Deferred,
		UnlockSet:       def.Unlock,
		PermissionMode:  permission.ModeBypass,
		EnableWebFetch:  true,
		WebFetchProxy:   h.proxyAddr,
		WebFetchCACert:  h.proxyCACert,
		BashEnv:         proxyEnv(h.proxyAddr, h.proxyCACert),
		WorkingDir:      taskDir,
		ToolOutputDir:   cmdOutDir(taskDir),
		MaxTurns:        h.maxTurns,
		Compaction:      compactionConfig(h.window),
		NonStreaming:    h.nonStreaming(),
		MaxTokens:       h.maxTokens(),
	}
	if h.tx != nil {
		opts.Transcript = h.tx
		opts.SessionID = fmt.Sprintf("exp%d-hypothesizer", ts.ID())
	}
	input := "通读下方态势，做横向综合，提出 planner 可能漏掉的态势级假设（没有有价值的新假设就直接结束本轮，0 条正常）：" + situational
	_, _, err := captureRun(ctx, opts, input,
		func(r db.Activity) {
			if emit != nil {
				r.Worker = "hypothesizer"
				emit(r)
			}
		})
	return err
}

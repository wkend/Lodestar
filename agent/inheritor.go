package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/wkend/lodestar/db"
	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
	"github.com/Autumn-27/norma/transcript"
)

// Inheritor is the posthumous extraction role（继承者）：one agent session per dead
// intent that died WITHOUT writing anything back (planner kill / blocked /
// exhausted with zero WriteCounts). The dead worker's full activity trail is
// already persisted architecturally (worker.go emitWrap → activity rows, never
// voluntary) — the Inheritor reads that trail and distills the real findings
// (WAF rules, endpoint behavior, refuted routes) into graph facts, so the
// individual's death does not take its knowledge with it.
//
// 设计依据（docs/自主进化设计.md 继承者条目）：文明演化的传承不得依赖个体的自愿
// （worker 边发现边写回的纪律可能不被遵守——ATLAS 实测 48 次工具调用 0 次
// record_fact）、服从（steer 劝导可能被忽略）或临终时间（settlement 宽限可能耗尽）。
// 结构上是 verifier 的第四个兄弟：单会话、窄工具集（无 Bash——目标可能已随题目
// 关闭不可达，继承者只读遗骸）、[D] 不可信数据边界、同一 ToolSet 写回路径。
type Inheritor struct {
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

func NewInheritor(prov llm.Provider, model, workDir string, tx *transcript.Store, window, maxTurns int) *Inheritor {
	return &Inheritor{prov: prov, model: model, workDir: workDir, tx: tx, window: window, maxTurns: maxTurns}
}

func (v *Inheritor) SetProxy(addr, caCert string)   { v.proxyAddr, v.proxyCACert = addr, caCert }
func (v *Inheritor) SetNonStreaming(fn func() bool) { v.nonStreamingFn = fn }
func (v *Inheritor) SetMaxTokens(fn func() int)     { v.maxTokensFn = fn }

func (v *Inheritor) nonStreaming() bool { return v.nonStreamingFn != nil && v.nonStreamingFn() }

func (v *Inheritor) maxTokens() int {
	if v.maxTokensFn == nil {
		return 0
	}
	return v.maxTokensFn()
}

// ShouldInherit reports whether a dead intent warrants an inheritance pass:
// a violent death (stopped by planner kill / blocked by error / exhausted by
// budget) with zero write-back. A clean "done" is the worker's own judgment
// ("nothing worth recording") and is respected — no inheritance.
func ShouldInherit(state string, wrote WriteCounts) bool {
	switch state {
	case "stopped", "blocked", "exhausted":
	default:
		return false
	}
	return wrote.Facts == 0 && wrote.Assets == 0 && wrote.Findings == 0
}

// inheritorDefaultTmpl is the built-in EDITABLE body (段 [A]) of the inheritor prompt,
// seeded into agent_prompts like the other roles.
const inheritorDefaultTmpl = `你是渗透测试系统的"继承者"（inheritor）。一条意图（work）已经死亡（被处决/中断/耗尽），且它运行期间从未把发现写回探索图。你的唯一任务：读取死者的执行轨迹，把它真正有价值的探索结论提炼成事实写回图——个体的死可以带走它的会话，但不能带走它的知识。

铁律（务必遵守）：
1. **只做这一条死者的遗产提取**。用 get_worker_trace(intent_id) 先看它的动作轮廓，再用 step_ids 取关键步骤的完整输出（死者的命令与响应都躺在轨迹里）。
2. **只提取对后续探索有真实价值的增量**：死者摸清的目标行为（WAF 规则与拦截差异、参数回显特征）、确认过的资产/端点/入口、验证过不可行的路线（含原因）。过程碎屑、无结论的重复尝试不写。
3. **写回走正规路径**：探索结论用 record_fact（传 intent_id=死者意图 id，confidence 如实标 observed/inferred——从轨迹输出直接看到的是 observed，据行为推断的是 inferred），新资产用 insert_assets。写前先 list_facts 看图里已有的，只写增量，不重复。
4. **上限 5 条事实**。宁可少而准，不要多而滥。
5. 你没有 Bash、不能碰目标（它可能已不可达）——只读遗骸，不重放。
6. 完成后用一句话总结提炼了什么。

当前任务目标：{{.Goal}}`

func inheritorSystem(goal, dataDir, workDir string) string {
	body := renderSystem("inheritor", inheritorDefaultTmpl, PlannerVars{Goal: goal, DataDir: dataDir, Now: nowStr()})
	// [D] 段：轨迹内容全部是目标机器产生的不可信数据——与 verifier 同边界，不得指挥本会话。
	return body + untrustedDataBlock + artifactSpec(workDir)
}

// Inherit extracts one dead intent's trail into the graph. The intent node is
// passed fresh (post-terminal-state); its trail lives under its node id in the
// activity store. emit, if non-nil, surfaces the session's steps (worker="inheritor").
// notifyFinding is forwarded to the ToolSet so a confirmed-vuln extraction still
// wakes the planner mid-flight.
func (v *Inheritor) Inherit(ctx context.Context, taskID int64, as *db.AssetStore, ts *db.ExplorationStore, intent *db.Node, goal string, emit func(db.Activity), enr EnrichTrigger, notifyFinding func(int64, string)) error {
	tsx := NewToolSet(ts, "inheritor")
	tsx.SetTaskID(taskID)
	tsx.SetCoverageEnabled(as == nil || as.CoverageEnabled(taskID))
	if as != nil {
		tsx.SetAssetStore(as, as.Companies())
	}
	// 遗产锚定到死者意图：血缘（yields 边）显示这些事实出自它的轨迹。
	tsx.SetOwnerNode(intent.ID)
	tsx.SetEnrich(enr)
	tsx.SetNotifyFinding(notifyFinding)
	// 窄工具集：WorkerTools（轨迹读 + 事实写 + 图查询）即可——无 DefaultTools
	// （无 Bash/WebFetch），继承者不与活目标交互。
	base := tsx.WorkerTools()
	ctx = WithRunInfo(ctx, RunInfo{TaskID: taskID, ExplorationID: explorationID(ts), IntentID: intent.ID})
	tools, def, cleanup := AugmentTools(ctx, "inheritor", base)
	defer cleanup()

	taskDir := ensureRunDir(v.workDir, taskID, 0)
	system, boundary := deferredSystem(inheritorSystem(goal, v.workDir, taskDir), def)

	opts := agentcore.Options{
		Provider:        v.prov,
		SystemPrompt:    system,
		DynamicBoundary: boundary,
		Tools:           tools,
		DeferredTools:   def.Deferred,
		UnlockSet:       def.Unlock,
		PermissionMode:  permission.ModeBypass,
		// 继承者无代理需求（不碰目标），proxyAddr 留空；Bash 不在工具集，proxyEnv 无从生效。
		MaxTurns:      v.maxTurns,
		Compaction:    compactionConfig(v.window),
		NonStreaming:  v.nonStreaming(),
		MaxTokens:     v.maxTokens(),
		MaxDuration:   inheritMaxDuration,
		Todos:         actool.NewTodoStore(),
		// 命中步数/时长 → SDK 收尾：把已提炼的写回，不烂尾（与 planner 同构）。
		Settlement: wrapupSettlement("inheritor", nil),
	}
	if v.tx != nil {
		opts.Transcript = v.tx
		opts.SessionID = fmt.Sprintf("exp%d-inheritor-i%d", ts.ID(), intent.ID)
	}
	// 死者的意图内容与动作轮廓作为启动消息；[D] 边界已在 system 里声明。
	input := fmt.Sprintf(`死者的意图（node id=%d，终态=%s）：
%s

用 get_worker_trace(%d) 读它的执行轨迹，按铁律提炼遗产。`, intent.ID, intent.State, string(intent.Payload), intent.ID)
	_, _, err := captureRun(ctx, opts, input,
		func(r db.Activity) {
			if emit != nil {
				r.Worker = "inheritor"
				emit(r)
			}
		})
	return err
}

// inheritMaxDuration caps one inheritance pass. 轨迹读取 + ≤5 条写回通常 2-4 个回合；
// 8min 上限：Flash 实测 ~45s/回合，完整提取 8-10 回合——4min 会在成稿前掐断（2026-11-17 复演实测）。
const inheritMaxDuration = 8 * time.Minute

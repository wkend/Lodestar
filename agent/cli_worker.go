package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/harness"
	actool "github.com/Autumn-27/norma/tool"
)

// CLIWorker delegates one intent to a general-purpose agent CLI subprocess
// (claude / codex / …) instead of running norma in-process. It implements
// AgentBackend so the engine drives it identically to *Worker.
//
// Batch model (Cairn-style, validated there): inject a directed slice of the
// graph into the prompt → let the CLI run its full agentic loop → parse the
// structured JSON it emits at the end → write results back through THIS
// process's ToolSet, so anchoring / lineage edges / notifyFinding / write
// counting are reused verbatim (never reimplemented).
//
// Deliberate ceilings (see design audit):
//   - No live cross-worker trace: the CLI's internal steps don't become
//     activity rows, so search_all_worker_traces can't see them. Intents that
//     need process-level sharing must be routed to NormaWorker (pickBackend).
//   - Guard/intercept hooks are in-process; a CLI subprocess's own Bash is NOT
//     gated by them. proxyEnv still forces the subprocess's HTTP through the
//     recording MITM. Authorized-lab use only.
type CLIWorker struct {
	// command + args template for the CLI. {{PROMPT}} is replaced by the built
	// prompt; if no arg contains {{PROMPT}} the prompt is piped via stdin.
	command string
	args    []string
	env     []string // extra env (e.g. ANTHROPIC_MODEL, API keys) merged over os.Environ

	proxyAddr   string
	proxyCACert string
	workDir     string
	runTimeout  time.Duration
}

// NewCLIWorker builds a CLI-backed worker. command/args select the CLI binary;
// use the literal "{{PROMPT}}" in args to pass the prompt as an argument
// (e.g. claude -p "{{PROMPT}}"), otherwise it is written to the child's stdin.
func NewCLIWorker(command string, args []string, workDir string) *CLIWorker {
	return &CLIWorker{command: command, args: args, workDir: workDir}
}

// compile-time assertion: CLIWorker must satisfy AgentBackend.
var _ AgentBackend = (*CLIWorker)(nil)

func (w *CLIWorker) SetProxy(addr, caCert string) { w.proxyAddr, w.proxyCACert = addr, caCert }
func (w *CLIWorker) SetEnv(env []string)          { w.env = env }
func (w *CLIWorker) SetRunTimeout(d time.Duration) { w.runTimeout = d }

// ExecuteWithMessage: CLIWorker is batch/stateless, so a mid-run human message
// is prepended to the prompt as an operator note; there is no live session to
// inject a turn into. requestID is unused (no transcript dedup needed — one
// subprocess per call).
func (w *CLIWorker) ExecuteWithMessage(ctx context.Context, name string, taskID int64, as *db.AssetStore,
	ts *db.ExplorationStore, intent *db.Node, hooks harness.HookRunner, emit func(db.Activity),
	enr EnrichTrigger, notifyFinding func(int64, string), requestID, message string,
) (harness.TerminalReason, WriteCounts, error) {
	return w.run(ctx, name, taskID, as, ts, intent, emit, enr, notifyFinding, message)
}

func (w *CLIWorker) Execute(ctx context.Context, name string, taskID int64, as *db.AssetStore,
	ts *db.ExplorationStore, intent *db.Node, hooks harness.HookRunner, emit func(db.Activity),
	enr EnrichTrigger, notifyFinding func(int64, string),
) (harness.TerminalReason, WriteCounts, error) {
	return w.run(ctx, name, taskID, as, ts, intent, emit, enr, notifyFinding, "")
}

func (w *CLIWorker) run(ctx context.Context, name string, taskID int64, as *db.AssetStore,
	ts *db.ExplorationStore, intent *db.Node, emit func(db.Activity), enr EnrichTrigger,
	notifyFinding func(int64, string), message string,
) (harness.TerminalReason, WriteCounts, error) {
	// One ToolSet bound to this task+intent — the single write path (same as the
	// norma worker). Writes reuse its CoreTool closures, so anchoring/lineage/
	// notify/counting are identical.
	tsx := NewToolSet(ts, name)
	tsx.SetTaskID(taskID)
	tsx.SetCoverageEnabled(as == nil || as.CoverageEnabled(taskID))
	if as != nil {
		tsx.SetAssetStore(as, as.Companies())
	}
	tsx.SetOwnerNode(intent.ID)
	tsx.SetEnrich(enr)
	tsx.SetNotifyFinding(notifyFinding)

	prompt := w.buildPrompt(ctx, tsx, intent, message)

	// ① delegate to the CLI subprocess
	stdout, err := w.exec(ctx, prompt)
	if err != nil {
		// ctx cancellation (kill_work / pause / timeout) → let the engine's
		// existing branch handle it; report as timeout so it isn't retried as a
		// model_error loop.
		if ctx.Err() != nil {
			return harness.ReasonTimeout, tsx.Writes(), ctx.Err()
		}
		return harness.ReasonModelError, tsx.Writes(), fmt.Errorf("cli exec: %w", err)
	}

	// ② parse the trailing JSON block; ③ write back through the ToolSet.
	result, perr := parseCLIResult(stdout)
	if perr != nil {
		// two-phase salvage: re-ask ONLY for the JSON (Cairn conclude idea).
		// Stateless CLIs have no session to resume, so we re-run with a
		// JSON-only instruction over the same prompt tail.
		salvage, serr := w.exec(ctx, prompt+"\n\n"+jsonOnlyReprompt)
		if serr == nil {
			result, perr = parseCLIResult(salvage)
		}
		if perr != nil {
			if emit != nil {
				emit(db.Activity{Kind: "note", Summary: "CLIWorker: no parseable JSON result"})
			}
			// Nothing salvageable → treat like a run that explored but wrote
			// nothing (engine marks the intent accordingly). Not an error loop.
			return harness.ReasonCompleted, tsx.Writes(), nil
		}
	}

	w.writeBack(ctx, tsx, intent, result, emit)
	return harness.ReasonCompleted, tsx.Writes(), nil
}

// ---- ② exec -----------------------------------------------------------------

func (w *CLIWorker) exec(ctx context.Context, prompt string) (string, error) {
	if w.runTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, w.runTimeout)
		defer cancel()
	}
	// substitute {{PROMPT}} in args; else pipe via stdin
	args := make([]string, len(w.args))
	viaStdin := true
	for i, a := range w.args {
		if strings.Contains(a, "{{PROMPT}}") {
			args[i] = strings.ReplaceAll(a, "{{PROMPT}}", prompt)
			viaStdin = false
		} else {
			args[i] = a
		}
	}
	cmd := exec.CommandContext(ctx, w.command, args...)
	cmd.Dir = w.workDir
	// os env + caller env + proxyEnv (force child HTTP through recording MITM)
	cmd.Env = append(append(os.Environ(), w.env...), proxyEnv(w.proxyAddr, w.proxyCACert)...)
	if viaStdin {
		cmd.Stdin = strings.NewReader(prompt)
	}
	// capture stderr so a failing CLI (bad key / proxy / model 5xx) surfaces its
	// reason instead of a bare "exit status 1".
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output() // stdout carries the result
	if err != nil {
		if s := strings.TrimSpace(stderr.String()); s != "" {
			err = fmt.Errorf("%w: %s", err, lastLine(s))
		}
	}
	return string(out), err
}

// lastLine returns the last non-empty line of s, capped, for compact error
// context (CLIs usually put the real failure on the final stderr line).
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	if len(last) > 500 {
		last = last[:500]
		// drop trailing bytes until valid UTF-8, so a multi-byte char (stderr is
		// often Chinese) isn't left sliced mid-sequence.
		for len(last) > 0 && !utf8.ValidString(last) {
			last = last[:len(last)-1]
		}
	}
	return last
}

// ---- ① inject: build the prompt with a directed graph slice ------------------

func (w *CLIWorker) buildPrompt(ctx context.Context, tsx *ToolSet, intent *db.Node, message string) string {
	var b strings.Builder
	b.WriteString(cliSystemPreamble)
	if message != "" {
		b.WriteString("\n\n【操作员实时提示】：" + message + "\n")
	}
	b.WriteString("\n\n【你要执行的意图】：\n")
	b.WriteString(string(intent.Payload))
	b.WriteString(fmt.Sprintf("\n(intent_id=%d — 写 finding 时用它)\n", intent.ID))

	// directed context: reuse the existing read tools' output verbatim.
	b.WriteString("\n【全局态势(仅供了解，不扩大你的职责)】：\n")
	b.WriteString(callRead(ctx, tsx.GraphOverviewTool(), `{}`))
	b.WriteString("\n【相关已知事实(top facts)】：\n")
	b.WriteString(callRead(ctx, tsx.ListFactsTool(), `{"limit":15}`))

	b.WriteString("\n\n" + cliOutputContract)
	return b.String()
}

// callRead invokes a read CoreTool and returns its text output (best-effort;
// empty string on error). tc is nil — the graph read closures don't use it.
func callRead(ctx context.Context, t actool.CoreTool, in string) string {
	if t == nil {
		return ""
	}
	res, err := t.Call(ctx, json.RawMessage(in), nil)
	if err != nil {
		return ""
	}
	return res.Flatten()
}

// ---- ③ write back through the ToolSet ---------------------------------------

// cliResult mirrors the record_fact / report_finding / insert_assets tool
// schemas so each item is fed straight back into the matching CoreTool.
type cliResult struct {
	Facts     []json.RawMessage `json:"facts"`
	Findings  []json.RawMessage `json:"findings"`
	NewAssets json.RawMessage   `json:"new_assets"` // passed as-is to insert_assets
}

func (w *CLIWorker) writeBack(ctx context.Context, tsx *ToolSet, intent *db.Node, r *cliResult, emit func(db.Activity)) {
	byName := map[string]actool.CoreTool{}
	for _, t := range tsx.WorkerTools() {
		byName[t.Name()] = t
	}
	// call feeds one JSON payload to a write tool. A write can fail silently in
	// ways that lose data (e.g. an asset_id referencing a non-existent asset
	// rolls back the whole AddNode via FK) — surface those as a note so the run
	// isn't reported clean while data was dropped.
	call := func(name string, in json.RawMessage) {
		t := byName[name]
		if t == nil || len(in) == 0 {
			return
		}
		res, err := t.Call(ctx, in, nil)
		if err == nil && res.IsError {
			err = fmt.Errorf("%s", res.Flatten())
		}
		if err != nil && emit != nil {
			emit(db.Activity{Kind: "note", Summary: "CLIWorker 写回失败(" + name + "): " + lastLine(err.Error())})
		}
	}
	for _, f := range r.Facts {
		call("record_fact", injectIntentID(f, intent.ID))
	}
	for _, f := range r.Findings {
		call("report_finding", injectIntentID(f, intent.ID))
	}
	if len(r.NewAssets) > 0 {
		call("insert_assets", r.NewAssets)
	}
}

// injectIntentID sets intent_id on a fact/finding object if the CLI omitted it,
// so lineage edges chain to the intent this worker is executing.
func injectIntentID(obj json.RawMessage, intentID int64) json.RawMessage {
	var m map[string]any
	if err := json.Unmarshal(obj, &m); err != nil {
		return obj
	}
	if _, ok := m["intent_id"]; !ok {
		m["intent_id"] = intentID
	}
	out, err := json.Marshal(m)
	if err != nil {
		return obj
	}
	return out
}

// ---- parsing -----------------------------------------------------------------

// jsonFence matches the LAST ```json ... ``` fenced block (or a bare trailing
// {...}) in the CLI stdout.
var jsonFence = regexp.MustCompile("(?s)```json\\s*(.*?)\\s*```")

// lastBalancedObject returns the last top-level {...} in s (braces inside
// string literals are ignored), or "" if none. Used when the CLI emits a bare
// trailing object instead of a ```json fence.
func lastBalancedObject(s string) string {
	depth, start, inStr, esc := 0, -1, false, false
	var best string
	for i, c := range s {
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			if depth == 0 {
				start = i
			}
			depth++
		case '}':
			if depth > 0 {
				depth--
				if depth == 0 && start >= 0 {
					best = s[start : i+1] // keep the last complete one
				}
			}
		}
	}
	return best
}

func parseCLIResult(stdout string) (*cliResult, error) {
	var raw string
	if m := jsonFence.FindAllStringSubmatch(stdout, -1); len(m) > 0 {
		raw = m[len(m)-1][1] // last fenced block
	} else {
		raw = lastBalancedObject(stdout) // no fence: trailing {...}
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("no json block in cli output")
	}
	var r cliResult
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return nil, fmt.Errorf("cli json unmarshal: %w", err)
	}
	return &r, nil
}

const cliSystemPreamble = "你是一个渗透测试执行 agent。你会领到【一条意图】，用你的全部工具能力去完成它，" +
	"然后把结论以规定的 JSON 格式输出。你只做领到的这条意图，不要扩散到其它方向。"

const jsonOnlyReprompt = "现在【只】输出上面结论的 JSON（按给定格式，用 ```json 包裹），不要任何其它文字。"

const cliOutputContract = "【输出格式（必须遵守）】：在你所有工作完成后，输出且仅输出一个 ```json 代码块，形如：\n" +
	"```json\n" +
	"{\n" +
	"  \"facts\": [{\"summary\":\"一句话结论\",\"detail\":\"细节\",\"evidence\":\"一行关键证据\",\"confidence\":\"observed|inferred\",\"asset_ids\":[]}],\n" +
	"  \"findings\": [{\"vulnclass\":\"SQL Injection\",\"name\":\"标题\",\"severity\":\"critical|high|medium|low\",\"summary\":\"摘要\",\"evidence\":\"PoC 文本\",\"asset_ids\":[]}],\n" +
	"  \"new_assets\": {\"assets\": []}\n" +
	"}\n" +
	"```\n" +
	"字段说明：\n" +
	"• confidence：observed（工具输出里直接看到）| inferred（据现象推断）。\n" +
	"• asset_ids：可选，只填【上面态势里确实出现过的】资产 id；不确定就留空数组，不要编造 id。\n" +
	"• new_assets.assets：本次新发现的资产数组（每个元素含 type 及对应字段），没有就留空数组。\n" +
	"三个顶层字段都可为空。没有漏洞时 findings 留空。"

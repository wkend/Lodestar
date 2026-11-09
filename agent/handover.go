package agent

import (
	"context"
	"iter"
	"strings"

	"github.com/Autumn-27/norma/compaction"
	"github.com/Autumn-27/norma/harness"
	"github.com/Autumn-27/norma/llm"
)

// handoverGuard (stage 7d) forces an agent to checkpoint its working state into the
// GRAPH before context compaction destroys the detail.
//
// Why a mechanism and not a prompt line: norma's compaction is lossy by design — it
// summarizes the head of the conversation and keeps the recent tail. A model that
// only "remembers" a nuance in that head loses it. The graph, by contrast, survives
// compaction, restarts and replacement of the individual agent. So the moment before
// compaction is exactly when a durable write pays off most.
//
// Two injections, both into the OUTGOING request only (never the stored history):
//
//  1. Pre-compaction (tokens cross triggerRatio of the auto-compact threshold):
//     tell the agent to write a handover fact NOW — key findings, open threads, next
//     step, all as one record_fact with a 【交接】 marker.
//  2. Post-compaction (observed input tokens drop sharply): tell it to re-read that
//     handover from the graph before continuing. Detected by a large token drop
//     rather than by norma's internal summary preamble string, so this keeps working
//     if that wording changes.
//
// ponytail: thresholds mirror compaction's math by hand (window − reserved − buffer);
// if norma changes the threshold formula this drifts. Import stays shallow on purpose —
// there is no exported hook into Pre, and forking the SDK is worse than this heuristic.
type handoverGuard struct {
	window     int  // model context window (tokens); <=0 disables the guard
	triggerPct int  // percent of the auto-compact threshold that trips the pre-write
	lastTokens int  // input tokens seen on the previous request
	wrote      bool // pre-write instruction already delivered this run
	reminded   bool // post-compaction reminder already delivered this run
}

const (
	handoverDefaultTriggerPct = 85
	// handoverDropRatio: a drop to below this fraction of the previous input size
	// means the working set was compacted away (a normal turn only grows).
	handoverDropRatio = 0.6
)

const handoverWriteInstruction = "\n\n【上下文即将压缩·立刻写交接】你的上下文已接近上限，马上要执行有损压缩。" +
	"在回答之前或下一步动作中，**立即**调用 record_fact 把你当前的状态写进图（这一步不能省）：" +
	"summary 以「【交接】」开头并一句话概括当前进度；detail 写清四件事——" +
	"(1) 已经确认的关键结论（含证据要点）；(2) 尚未完成、下一步该做什么；(3) 走过的死路与原因；(4) 任何后续接手者会需要的上下文。" +
	"图不会随上下文压缩而丢失，写进去的部分才算保住。写完之后继续正常工作。"

const handoverResumeInstruction = "\n\n【上下文已压缩·先找回交接】你的上文刚被压缩，细节可能已丢失。" +
	"继续之前先用 list_facts 找到 summary 以「【交接】」开头的那条事实，需要时用 node_detail 读全文，" +
	"据此恢复你的进度再往下做。不要凭压缩后的模糊记忆重复已做过的步骤。"

// handoverConfigFor returns the trigger threshold in tokens. Mirrors compaction's
// autoThreshold: effective = window − reservedForSummary(min(maxOutput,20000)),
// then minus a buffer tiered off the effective window.
func handoverThreshold(window int) int {
	if window <= 0 {
		return 0
	}
	effective := window - 20000
	if effective <= 0 {
		return window
	}
	var buf int
	switch {
	case effective >= 800000:
		buf = 50000
	case effective >= 400000:
		buf = 30000
	default:
		buf = 13000
	}
	return effective - buf
}

// tokensOf mirrors compaction's count(): the same 4/3 fudge over the API view.
func tokensOf(msgs []llm.Message) int {
	return compaction.EstimateTokens(msgs) * 4 / 3
}

// beforeRequest decides what (if anything) to inject. Pure apart from the guard's
// own bookkeeping, so it is the unit-testable core.
//
// The "already sent" flags are set ONLY when the injection will actually land
// (canAppend). Setting them earlier would silently burn the one shot whenever the
// request happened to end on an unanswered tool_use — the write would never be
// asked for, and compaction would destroy the context unhanded-over.
func (g *handoverGuard) beforeRequest(msgs []llm.Message) (inject bool, text string) {
	if g.window <= 0 || len(msgs) == 0 {
		return false, ""
	}
	tok := tokensOf(msgs)
	prev := g.lastTokens
	g.lastTokens = tok // always track, so a drop is detectable even on blocked turns

	// Post-compaction: the working set shrank sharply (a normal turn only grows).
	if prev > 0 && tok < int(float64(prev)*handoverDropRatio) {
		if g.wrote && !g.reminded && canAppend(msgs) {
			g.reminded = true
			return true, handoverResumeInstruction
		}
		return false, ""
	}

	// Pre-compaction: crossing the trigger threshold, once per run.
	thr := handoverThreshold(g.window)
	pct := g.triggerPct
	if pct <= 0 {
		pct = handoverDefaultTriggerPct
	}
	if thr > 0 && !g.wrote && tok >= thr*pct/100 && canAppend(msgs) {
		g.wrote = true
		return true, handoverWriteInstruction
	}
	return false, ""
}

// canAppend reports whether a trailing user message can be added without breaking a
// tool_use/tool_result pair. Anthropic rejects a history whose assistant tool_use is
// not immediately answered, so injecting there would 400 the whole request.
func canAppend(msgs []llm.Message) bool {
	if len(msgs) == 0 {
		return true
	}
	last := msgs[len(msgs)-1]
	return !(last.Role == llm.RoleAssistant && len(last.ToolUses()) > 0)
}

// appendInject adds the instruction as a trailing user message.
func appendInject(msgs []llm.Message, text string) []llm.Message {
	if !canAppend(msgs) {
		return msgs
	}
	out := make([]llm.Message, len(msgs), len(msgs)+1)
	copy(out, msgs)
	return append(out, llm.UserText(strings.TrimSpace(text)))
}

// handoverDeps wraps the harness's model-call boundaries so the guard sees every
// request. Both streaming and non-streaming paths are covered; a nil/zero inner is
// fine because agentcore would have defaulted these to the same provider calls.
func handoverDeps(prov llm.Provider, window int) harness.QueryDeps {
	g := &handoverGuard{window: window, triggerPct: handoverDefaultTriggerPct}
	return harness.QueryDeps{
		CallModel: func(ctx context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
			if inject, text := g.beforeRequest(req.Messages); inject {
				req.Messages = appendInject(req.Messages, text)
			}
			return prov.Stream(ctx, req)
		},
		CallModelSync: func(ctx context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
			if inject, text := g.beforeRequest(req.Messages); inject {
				req.Messages = appendInject(req.Messages, text)
			}
			return prov.Complete(ctx, req)
		},
	}
}

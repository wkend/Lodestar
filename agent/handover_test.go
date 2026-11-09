package agent

import (
	"strings"
	"testing"

	"github.com/Autumn-27/norma/llm"
)

// The guard's whole job is: trip once before compaction, remind once after. These
// cover the three ways it can silently fail to do that — never tripping, tripping
// repeatedly, and burning its one shot on a request it cannot actually modify.

func bigMsgs(tokens int) []llm.Message {
	// EstimateTokens is a heuristic; pad until we're clearly over the mark.
	unit := strings.Repeat("x", 400) // ~100 tokens per message
	return []llm.Message{llm.UserText(strings.Repeat(unit, tokens/100))}
}

func TestHandoverTripsOnceBeforeCompaction(t *testing.T) {
	// 200k window → threshold ≈ (200000-20000) - 13000 = 167000; 85% ≈ 141950.
	g := &handoverGuard{window: 200000}
	if inject, _ := g.beforeRequest(bigMsgs(50000)); inject {
		t.Fatal("small history must not trip the pre-compaction write")
	}
	inject, text := g.beforeRequest(bigMsgs(160000))
	if !inject || !strings.Contains(text, "交接") {
		t.Fatalf("crossing the threshold must ask for a handover, got inject=%v text=%q", inject, text)
	}
	// once per run: a second over-threshold turn must stay quiet (no nagging)
	if inject, _ := g.beforeRequest(bigMsgs(170000)); inject {
		t.Fatal("pre-compaction write must fire at most once per run")
	}
}

func TestHandoverRemindsAfterShrink(t *testing.T) {
	g := &handoverGuard{window: 200000, wrote: true}
	g.lastTokens = 180000
	// a sharp drop means the working set was compacted away
	inject, text := g.beforeRequest(bigMsgs(20000))
	if !inject || !strings.Contains(text, "已压缩") {
		t.Fatalf("post-compaction shrink must prompt a re-read, got inject=%v text=%q", inject, text)
	}
	if inject, _ := g.beforeRequest(bigMsgs(21000)); inject {
		t.Fatal("resume reminder must fire at most once per run")
	}
}

// A drop with nothing written means there is no handover to read back — reminding
// would send the agent hunting for a fact that does not exist.
func TestHandoverNoReminderWithoutHandover(t *testing.T) {
	g := &handoverGuard{window: 200000, wrote: false}
	g.lastTokens = 180000
	if inject, _ := g.beforeRequest(bigMsgs(20000)); inject {
		t.Fatal("must not ask to re-read a handover that was never written")
	}
}

// The one-shot must not be consumed by a request it cannot modify: a history ending
// on an unanswered tool_use would 400 if a user message were appended there.
func TestHandoverDoesNotBurnShotOnUnappendableTurn(t *testing.T) {
	g := &handoverGuard{window: 200000}
	msgs := bigMsgs(160000)
	msgs = append(msgs, llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
		{Type: llm.BlockToolUse, ID: "t1", Name: "Bash", Input: []byte(`{}`)},
	}})
	if inject, _ := g.beforeRequest(msgs); inject {
		t.Fatal("must not inject after an unanswered tool_use")
	}
	if g.wrote {
		t.Fatal("a blocked turn must not consume the single pre-compaction shot")
	}
	// the next, appendable turn still gets it
	if inject, _ := g.beforeRequest(bigMsgs(170000)); !inject {
		t.Fatal("the handover must still fire once the history is appendable")
	}
}

func TestHandoverThresholdMonotonic(t *testing.T) {
	// bigger windows must not produce a smaller trigger (a reversed formula would
	// silently make compaction-triggering turns unhanded-over)
	prev := 0
	for _, w := range []int{100000, 200000, 400000, 800000, 1000000} {
		thr := handoverThreshold(w)
		if thr <= prev {
			t.Fatalf("threshold not increasing at window=%d: %d <= %d", w, thr, prev)
		}
		prev = thr
	}
	if handoverThreshold(0) != 0 {
		t.Fatal("window<=0 must disable the guard")
	}
}

func TestAppendInjectKeepsHistoryShape(t *testing.T) {
	msgs := []llm.Message{llm.UserText("a"), llm.UserText("b")}
	out := appendInject(msgs, " 提示 ")
	if len(out) != 3 || out[2].Text() != "提示" {
		t.Fatalf("injection should append one trimmed user message, got %+v", out)
	}
	if len(msgs) != 2 {
		t.Fatal("appendInject must not mutate the caller's slice")
	}
}

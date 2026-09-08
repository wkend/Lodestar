package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// parseCLIResult is the only non-trivial logic in CLIWorker: pull the trailing
// JSON out of noisy agentic stdout. These check the three cases that matter.
func TestParseCLIResult(t *testing.T) {
	// ① fenced block buried in chatter, and LAST fence wins (agent may print a
	//    draft then a final).
	noisy := "let me think...\n```json\n{\"facts\":[{\"summary\":\"draft\"}]}\n```\n" +
		"actually, final answer:\n```json\n{\"facts\":[{\"summary\":\"real\"}],\"findings\":[]}\n```\ndone."
	r, err := parseCLIResult(noisy)
	if err != nil {
		t.Fatalf("noisy fenced: unexpected err %v", err)
	}
	if len(r.Facts) != 1 {
		t.Fatalf("noisy fenced: want 1 fact, got %d", len(r.Facts))
	}
	var m map[string]any
	mustUnmarshal(t, r.Facts[0], &m)
	if m["summary"] != "real" {
		t.Fatalf("last-fence-wins broken: got %q", m["summary"])
	}

	// ② no fence, bare trailing object.
	r, err = parseCLIResult("blah blah\n{\"findings\":[{\"name\":\"x\"}]}")
	if err != nil {
		t.Fatalf("bare object: unexpected err %v", err)
	}
	if len(r.Findings) != 1 {
		t.Fatalf("bare object: want 1 finding, got %d", len(r.Findings))
	}

	// ③ empty result is VALID (explored, found nothing).
	r, err = parseCLIResult("```json\n{\"facts\":[],\"findings\":[]}\n```")
	if err != nil {
		t.Fatalf("empty: unexpected err %v", err)
	}
	if len(r.Facts) != 0 || len(r.Findings) != 0 {
		t.Fatalf("empty: want 0/0, got %d/%d", len(r.Facts), len(r.Findings))
	}

	// ④ no JSON at all → error (so caller triggers salvage reprompt).
	if _, err = parseCLIResult("I could not complete the task."); err == nil {
		t.Fatal("no-json: expected error, got nil")
	}
}

// injectIntentID must add intent_id when missing and leave it when present.
func TestInjectIntentID(t *testing.T) {
	var m map[string]any

	mustUnmarshal(t, injectIntentID([]byte(`{"summary":"a"}`), 42), &m)
	if m["intent_id"] != float64(42) {
		t.Fatalf("missing intent_id not injected: got %v", m["intent_id"])
	}

	mustUnmarshal(t, injectIntentID([]byte(`{"summary":"a","intent_id":7}`), 42), &m)
	if m["intent_id"] != float64(7) {
		t.Fatalf("present intent_id overwritten: got %v", m["intent_id"])
	}
}

func mustUnmarshal(t *testing.T, b []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("unmarshal %s: %v", b, err)
	}
}

// lastBalancedObject must ignore braces inside string literals (and escaped
// quotes), else a "}" in evidence text truncates the object early — which is
// exactly how a bare-object CLI reply loses data.
func TestLastBalancedObject(t *testing.T) {
	cases := []struct{ in, want string }{
		// brace inside a string value must not close the object
		{`noise {"evidence":"got 500 } here"} tail`, `{"evidence":"got 500 } here"}`},
		// escaped quote inside string must not end the string early
		{`{"detail":"he said \"hi }\" ok"}`, `{"detail":"he said \"hi }\" ok"}`},
		// nested object: outer one is returned whole
		{`x {"a":{"b":1}} y`, `{"a":{"b":1}}`},
		// last complete top-level object wins
		{`{"n":1} then {"n":2}`, `{"n":2}`},
		// no object at all
		{`plain text`, ``},
	}
	for _, c := range cases {
		if got := lastBalancedObject(c.in); got != c.want {
			t.Errorf("lastBalancedObject(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// lastLine returns the last non-empty line, capped at a rune boundary. The cap
// must not slice a multi-byte char (stderr is often Chinese) into invalid UTF-8.
func TestLastLine(t *testing.T) {
	if got := lastLine(""); got != "" {
		t.Errorf("empty: got %q", got)
	}
	if got := lastLine("only line"); got != "only line" {
		t.Errorf("single: got %q", got)
	}
	if got := lastLine("first\n  second  \n"); got != "second" {
		t.Errorf("multiline+trim: got %q", got)
	}
	// a long line of 3-byte Chinese runes (600 bytes) must cap without producing
	// invalid UTF-8.
	got := lastLine(strings.Repeat("错", 200)) // 200*3 = 600 bytes
	if len(got) > 500 {
		t.Errorf("cap: len=%d > 500", len(got))
	}
	if !utf8.ValidString(got) {
		t.Errorf("cap produced invalid UTF-8: %q", got)
	}
}

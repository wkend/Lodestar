package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/harness"
)

// TestCLIWorkerEndToEnd is the smoke test for the whole CLIWorker chain:
// exec (stdin branch) → parse the trailing ```json block → write facts/findings
// back through the real ToolSet into a real exploration graph. A stub command
// stands in for the agent CLI: it drains stdin (the injected prompt) and prints
// a fixed result, so no LLM / network is involved.
func TestCLIWorkerEndToEnd(t *testing.T) {
	d := testDB(t)
	defer d.Close()

	task, err := d.CreateTask("cli-smoke", "smoke goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })
	store := d.Exploration(task.ExplorationID)

	intentID, err := store.AddIntent(map[string]any{"summary": "smoke intent"}, 1, nil, "planner")
	if err != nil {
		t.Fatal(err)
	}
	intent, err := store.GetNode(intentID)
	if err != nil || intent == nil {
		t.Fatalf("get intent: %v", err)
	}

	// The stub CLI's stdout: chatter + a fenced JSON result (1 fact, 1 finding).
	cliOut := "探索完成。\n```json\n" +
		`{"facts":[{"summary":"冒烟事实","detail":"d","evidence":"e","confidence":"observed"}],` +
		`"findings":[{"vulnclass":"Test","name":"冒烟漏洞","severity":"low","summary":"s","evidence":"poc"}],` +
		`"new_assets":{"assets":[]}}` + "\n```\n"
	outFile := filepath.Join(t.TempDir(), "cli_out.txt")
	if err := os.WriteFile(outFile, []byte(cliOut), 0o600); err != nil {
		t.Fatal(err)
	}
	// stub: drain stdin (the prompt), then emit the fixed result. No {{PROMPT}}
	// in args → prompt is piped via stdin, exercising that branch.
	w := NewCLIWorker("sh", []string{"-c", "cat >/dev/null; cat " + outFile}, t.TempDir())

	var notified []int64
	notify := func(id int64, _ string) { notified = append(notified, id) }
	emit := func(db.Activity) {}

	reason, writes, err := w.Execute(context.Background(), "cli-worker", task.ID,
		d.Assets(), store, intent, nil, emit, nil, notify)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if reason != harness.ReasonCompleted {
		t.Errorf("reason = %q, want completed", reason)
	}
	if writes.Facts != 1 || writes.Findings != 1 {
		t.Errorf("writes = %+v, want 1 fact / 1 finding", writes)
	}

	// the fact and finding must actually be in the graph, tied to the intent.
	facts, _ := store.ListByKind(db.KindFact, 100)
	if !hasPayloadField(facts, "summary", "冒烟事实") {
		t.Errorf("fact not written to graph: %+v", facts)
	}
	findings, _ := store.ListByKind(db.KindFinding, 100)
	if !hasPayloadField(findings, "name", "冒烟漏洞") {
		t.Errorf("finding not written to graph: %+v", findings)
	}
	// report_finding notifies with the finding id.
	if len(notified) != 1 {
		t.Errorf("notifyFinding calls = %d, want 1", len(notified))
	}
}

// hasPayloadField reports whether any node's payload has key == want.
func hasPayloadField(nodes []*db.Node, key, want string) bool {
	for _, n := range nodes {
		var m map[string]any
		if json.Unmarshal(n.Payload, &m) == nil && m[key] == want {
			return true
		}
	}
	return false
}

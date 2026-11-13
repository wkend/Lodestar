package server

import (
	"context"
	"strconv"
	"testing"

	"github.com/wkend/lodestar/db"
)

// N7 终局复核扫尾：落终态前同步清一遍未复核积压（含跨图继承）。verify 是参数
// seam，单测注入 fake 断言路由/去重/失败不阻塞；生产行为由 provider 级 e2e 覆盖。
func TestRunVerifierSweep(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer m.Close()
	e := NewEngine(m)

	source, err := m.CreateTask("sweep-source", "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = m.DeleteTask(source.ID, DeleteTaskOptions{}) })
	inheritedID, err := source.Store.AddNode(db.KindFinding, map[string]any{"summary": "source RCE"}, 0, "confirmed", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	// already settled by the heartbeat path earlier this process → sweep must skip it
	settledID, err := source.Store.AddNode(db.KindFinding, map[string]any{"summary": "settled SQLi"}, 0, "confirmed", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := source.Store.SetFindingVerification(settledID, true, "", "verifier"); err != nil {
		t.Fatal(err)
	}
	srcTaskID, _ := strconv.ParseInt(source.ID, 10, 64)
	downstream, err := m.CreateTaskWithOptions("sweep-down", "goal", db.TaskCreateOptions{SourceTaskIDs: []int64{srcTaskID}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = m.DeleteTask(downstream.ID, DeleteTaskOptions{}) })
	ownID, err := downstream.Store.AddNode(db.KindFinding, map[string]any{"summary": "own SSRF"}, 0, "confirmed", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	tk, ok := m.Task(downstream.ID)
	if !ok {
		t.Fatal("task handle not found")
	}

	type call struct {
		node  int64
		store int64
	}
	var calls []call
	verify := func(_ context.Context, _ int64, store *db.ExplorationStore, node *db.Node) error {
		calls = append(calls, call{node: node.ID, store: store.ID()})
		if node.ID == ownID {
			return context.Canceled // failed session: must NOT block the sweep
		}
		return nil
	}
	e.runVerifierSweep(context.Background(), tk, verify)

	if len(calls) != 2 {
		t.Fatalf("calls=%d want 2 (own + inherited; settled excluded)", len(calls))
	}
	if calls[0].node != ownID || calls[0].store != downstream.ExpID {
		t.Fatalf("own finding must verify against the own store: %+v", calls[0])
	}
	if calls[1].node != inheritedID || calls[1].store != source.ExpID {
		t.Fatalf("inherited finding must verify against the OWNING store: %+v", calls[1])
	}
	// the failed item is not stamped; the succeeded inherited one is → a rerun retries only the failure
	e.runVerifierSweep(context.Background(), tk, verify)
	if len(calls) != 3 || calls[2].node != ownID {
		t.Fatalf("rerun must retry only the unstamped failure: %+v", calls)
	}
	// nil verify (verifier disabled) → no-op, no panic
	e.runVerifierSweep(context.Background(), tk, nil)
}

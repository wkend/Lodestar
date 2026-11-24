package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/wkend/lodestar/db"
)

// callGetWorkerOutput invokes the get_worker_output tool and decodes the JSON result.
func callGetWorkerOutput(t *testing.T, ts *ToolSet, intentID int64) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"intent_id": intentID})
	res, err := ts.getWorkerOutput().Call(context.Background(), raw, nil)
	if err != nil {
		t.Fatalf("getWorkerOutput Call error: %v", err)
	}
	text := res.Flatten()
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("unmarshal result: %v\nraw: %s", err, text)
	}
	return out
}

// setupIntentWithText creates an exploration + one intent node in the given state
// with a single text activity (no result row).
func setupIntentWithText(t *testing.T, state string) (*db.DB, *db.ExplorationStore, *ToolSet, int64) {
	t.Helper()
	d := testDB(t)
	t.Cleanup(func() { d.Close() })
	expID, err := d.CreateExploration("get_worker_output test", state)
	if err != nil {
		t.Fatalf("CreateExploration: %v", err)
	}
	store := d.Exploration(expID)
	nid, err := store.AddIntent(map[string]any{"summary": "test intent"}, 0, nil, "planner")
	if err != nil {
		t.Fatalf("AddIntent: %v", err)
	}
	if _, err := d.Exec(`UPDATE exploration_nodes SET state=$1 WHERE id=$2`, state, nid); err != nil {
		t.Fatalf("set state: %v", err)
	}
	if _, err := store.AppendActivity(db.Activity{NodeID: &nid, Worker: "work#1", Kind: "text", Summary: "进行中的输出"}); err != nil {
		t.Fatalf("AppendActivity: %v", err)
	}
	return d, store, NewToolSet(store, "planner"), nid
}

// TestGetWorkerOutputRunningNotTerminated 是发现 18 的回归测试：正在运行的意图
// 没有 result 行，但 terminated 必须为 false（曾误报 true 导致 planner 处决活 work）。
func TestGetWorkerOutputRunningNotTerminated(t *testing.T) {
	_, _, ts, id := setupIntentWithText(t, "running")
	out := callGetWorkerOutput(t, ts, id)
	if out["terminated"] != false {
		t.Errorf("running intent terminated = %v, want false", out["terminated"])
	}
	if out["state"] != "running" {
		t.Errorf("state = %v, want running", out["state"])
	}
	if out["final_text"] != "进行中的输出" {
		t.Errorf("final_text = %v", out["final_text"])
	}
}

// TestGetWorkerOutputStoppedTerminated 终态且无 result 行（stopped）= 真正的
// "死于无输出"，terminated=true。
func TestGetWorkerOutputStoppedTerminated(t *testing.T) {
	_, _, ts, id := setupIntentWithText(t, "stopped")
	out := callGetWorkerOutput(t, ts, id)
	if out["terminated"] != true {
		t.Errorf("stopped intent terminated = %v, want true", out["terminated"])
	}
	if out["state"] != "stopped" {
		t.Errorf("state = %v, want stopped", out["state"])
	}
}

// TestGetWorkerOutputDoneWithResult 有 result 行的正常结束：final_text 取结论，
// terminated=false。
func TestGetWorkerOutputDoneWithResult(t *testing.T) {
	d, store, ts, id := setupIntentWithText(t, "running")
	if _, err := store.AppendActivity(db.Activity{NodeID: &id, Worker: "work#1", Kind: "result", Summary: "最终结论"}); err != nil {
		t.Fatalf("AppendActivity result: %v", err)
	}
	if _, err := d.Exec(`UPDATE exploration_nodes SET state='done' WHERE id=$1`, id); err != nil {
		t.Fatalf("set done: %v", err)
	}
	out := callGetWorkerOutput(t, ts, id)
	if out["terminated"] != false {
		t.Errorf("done intent terminated = %v, want false", out["terminated"])
	}
	if out["state"] != "done" {
		t.Errorf("state = %v, want done", out["state"])
	}
	if out["final_text"] != "最终结论" {
		t.Errorf("final_text = %v, want 最终结论", out["final_text"])
	}
}

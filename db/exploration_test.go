package db

import (
	"fmt"
	"strings"
	"testing"
)

// AddNode 的 kind/payload 兜底在触库前执行，故零值 store 即可测——不需要 Postgres。
// 这是"未注册 kind / 空 payload"这类未来写入者(如第二个 brain 发假设)会踩的最后一道闸。
func TestAddNodeRejectsGarbage(t *testing.T) {
	es := &ExplorationStore{} // 校验先于 s.db.Begin()，零值 store 安全

	if _, err := es.AddNode("nonsense-kind", map[string]any{"x": 1}, 0, "open", "brain", nil); err == nil ||
		!strings.Contains(err.Error(), "未知 kind") {
		t.Fatalf("未知 kind 应被拒绝, got err=%v", err)
	}
	if _, err := es.AddNode(KindFact, map[string]any{}, 0, "confirmed", "worker", nil); err == nil ||
		!strings.Contains(err.Error(), "payload 不能为空") {
		t.Fatalf("空 payload 应被拒绝, got err=%v", err)
	}
	if _, err := es.AddNode(KindFact, nil, 0, "confirmed", "worker", nil); err == nil {
		t.Fatalf("nil payload 应被拒绝, got err=%v", err)
	}
}

// stage 6a: hypothesis 是合法 kind，state 机 open/confirmed/refuted。新库上测的是
// base CREATE TABLE 的双 CHECK 接受它、并拒绝越界 state;老库的迁移 DO 块路径靠手测
// (docker)覆盖。base 与迁移块的 state 集合逐字一致,故两者等价有效。
func TestAddNodeHypothesis(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()
	expID, err := d.CreateExploration("test", "hypothesis kind")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Exec(`DELETE FROM explorations WHERE id=$1`, expID)
	es := d.Exploration(expID)

	for _, st := range []string{"open", "confirmed", "refuted"} {
		if _, err := es.AddNode(KindHypothesis, map[string]any{"summary": "h " + st}, 0, st, "brain", nil); err != nil {
			t.Fatalf("hypothesis state=%q 应被接受, got err=%v", st, err)
		}
	}
	// 越界 state 应被 PG 的 ck_node_state 拒绝(证明 CHECK 真的按 kind 分支)。
	if _, err := es.AddNode(KindHypothesis, map[string]any{"summary": "bad"}, 0, "running", "brain", nil); err == nil {
		t.Fatal("hypothesis state=running 应被 ck_node_state 拒绝")
	}
}

func TestExplorationFlow(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()

	expID, err := d.CreateExploration("test", "拿下测试目标")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Exec(`DELETE FROM explorations WHERE id=$1`, expID) // cascades nodes/edges/activity
	es := d.Exploration(expID)

	// goal node + two intents
	goal, err := es.AddGoal(map[string]any{"text": "getadmin", "vulnclass": "authz"}, "human")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := es.AddIntent(map[string]any{"summary": "enumerate endpoints"}, 5, nil, "planner"); err != nil {
		t.Fatal(err)
	}
	i2, err := es.AddIntent(map[string]any{"summary": "test idor"}, 8, nil, "planner")
	if err != nil {
		t.Fatal(err)
	}

	// frontier ordered by priority desc → i2(8) before i1(5)
	fr, err := es.Frontier(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(fr) != 2 || fr[0].ID != i2 {
		t.Fatalf("frontier order wrong: %+v", fr)
	}

	// atomic claim: first wins, second on same id fails
	ok, err := es.ClaimIntent(i2, "worker-1")
	if err != nil || !ok {
		t.Fatalf("claim i2: ok=%v err=%v", ok, err)
	}
	ok2, _ := es.ClaimIntent(i2, "worker-2")
	if ok2 {
		t.Fatalf("double-claim should fail")
	}

	// finding yields from intent, proves goal
	find, err := es.AddNode("finding", map[string]any{"vulnclass": "idor", "severity": "high"}, 9, "confirmed", "worker-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := es.Link(i2, "yields", find); err != nil {
		t.Fatal(err)
	}
	if err := es.Link(find, "proves", goal); err != nil {
		t.Fatal(err)
	}
	if err := es.SetNodeState(goal, "met"); err != nil {
		t.Fatal(err)
	}

	// lineage: ancestors of the finding traced backward — here {i2, find} joined by
	// the yields edge. The proves→goal edge is DOWNSTREAM (goal must be excluded),
	// and the unrelated intent i1 is not on any path to the finding (excluded too).
	lnNodes, lnEdges, err := es.FindingLineage(find)
	if err != nil {
		t.Fatal(err)
	}
	got := map[int64]bool{}
	for _, n := range lnNodes {
		got[n.ID] = true
	}
	if len(lnNodes) != 2 || !got[find] || !got[i2] {
		t.Fatalf("lineage nodes: want {i2,find}, got %+v", lnNodes)
	}
	if got[goal] {
		t.Fatalf("lineage must exclude the proved goal (it is downstream of the finding)")
	}
	if len(lnEdges) != 1 || lnEdges[0].From != i2 || lnEdges[0].To != find || lnEdges[0].Rel != "yields" {
		t.Fatalf("lineage edges: want i2-yields->find, got %+v", lnEdges)
	}

	// activity poll by id cursor
	id1, err := es.AppendActivity(Activity{Worker: "worker-1", Kind: "tool_use", Tool: "Bash", Summary: "ran curl", Detail: "full output"})
	if err != nil {
		t.Fatal(err)
	}
	items, cursor, err := es.ActivityList(nil, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || cursor != id1 {
		t.Fatalf("activity list: items=%d cursor=%d", len(items), cursor)
	}
	det, _ := es.ActivityDetail(id1)
	if det != "full output" {
		t.Fatalf("detail want 'full output', got %q", det)
	}
	// incremental: nothing new after cursor
	items2, _, _ := es.ActivityList(nil, cursor, 100)
	if len(items2) != 0 {
		t.Fatalf("incremental poll should be empty, got %d", len(items2))
	}

	// stats
	st, _ := es.Stats()
	if st["intent"] != 2 || st["goal"] != 1 || st["finding"] != 1 {
		t.Fatalf("stats: %+v", st)
	}
}

func TestIntentPauseResumeAndCancelCleanup(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()

	expID, err := d.CreateExploration("test", "worker control cleanup")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Exec(`DELETE FROM explorations WHERE id=$1`, expID)
	es := d.Exploration(expID)

	assetID, err := d.Assets().UpsertRootDomain(UpsertRootDomainReq{
		Domain: fmt.Sprintf("cancel-intent-%d.invalid", expID),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer deleteAsset(d, assetID)

	otherIntent, err := es.AddIntent(map[string]any{"summary": "keep intent"}, 1, nil, "planner")
	if err != nil {
		t.Fatal(err)
	}
	intentID, err := es.AddIntent(map[string]any{"summary": "cancel intent"}, 10, nil, "planner")
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := es.ClaimIntent(intentID, "worker-control-test"); err != nil || !claimed {
		t.Fatalf("initial claim: claimed=%v err=%v", claimed, err)
	}
	if err := es.SetIntentState(intentID, "paused"); err != nil {
		t.Fatal(err)
	}

	frontier, err := es.Frontier(10)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range frontier {
		if node.ID == intentID {
			t.Fatalf("paused intent %d must not enter frontier", intentID)
		}
	}
	if claimed, err := es.ClaimIntent(intentID, "worker-while-paused"); err != nil || claimed {
		t.Fatalf("paused intent claim: claimed=%v err=%v", claimed, err)
	}
	if err := es.SetIntentState(intentID, "open"); err != nil {
		t.Fatal(err)
	}
	if claimed, err := es.ClaimIntent(intentID, "worker-after-resume"); err != nil || !claimed {
		t.Fatalf("resumed intent claim: claimed=%v err=%v", claimed, err)
	}
	if err := es.SetIntentState(intentID, "paused"); err != nil {
		t.Fatal(err)
	}

	directFact, err := es.AddNode("fact", map[string]any{"summary": "remove fact"}, 0, "confirmed", "worker", []int64{assetID})
	if err != nil {
		t.Fatal(err)
	}
	directFinding, err := es.AddNode("finding", map[string]any{"summary": "remove finding"}, 0, "confirmed", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	keptFact, err := es.AddNode("fact", map[string]any{"summary": "keep fact"}, 0, "confirmed", "other-worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := es.Link(intentID, RelYields, directFact); err != nil {
		t.Fatal(err)
	}
	if err := es.Link(intentID, RelYields, directFinding); err != nil {
		t.Fatal(err)
	}
	findingRowID, err := es.AddStandaloneFinding(0, directFinding, "test", "cancelled finding", SeverityHigh, "summary", "evidence", "worker", []int64{assetID})
	if err != nil {
		t.Fatal(err)
	}
	activityID, err := es.AppendActivity(Activity{NodeID: &intentID, Worker: "worker", Kind: "result", Summary: "remove activity"})
	if err != nil {
		t.Fatal(err)
	}
	keptActivityID, err := es.AppendActivity(Activity{NodeID: &otherIntent, Worker: "other-worker", Kind: "result", Summary: "keep activity"})
	if err != nil {
		t.Fatal(err)
	}

	cleanup, err := es.CancelIntent(intentID)
	if err != nil {
		t.Fatal(err)
	}
	if cleanup.Intents != 1 || cleanup.Facts != 1 || cleanup.Findings != 1 || cleanup.Activities != 1 {
		t.Fatalf("unexpected cleanup counts: %+v", cleanup)
	}
	for _, nodeID := range []int64{intentID, directFact, directFinding} {
		node, err := es.GetNode(nodeID)
		if err != nil {
			t.Fatal(err)
		}
		if node != nil {
			t.Fatalf("node %d survived intent cancellation", nodeID)
		}
	}
	for _, nodeID := range []int64{otherIntent, keptFact} {
		node, err := es.GetNode(nodeID)
		if err != nil || node == nil {
			t.Fatalf("unrelated node %d removed: node=%v err=%v", nodeID, node, err)
		}
	}

	assertCount := func(query string, want int, args ...any) {
		t.Helper()
		var got int
		if err := d.QueryRow(query, args...).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("query count=%d, want %d: %s", got, want, query)
		}
	}
	assertCount(`SELECT COUNT(*) FROM findings WHERE id=$1`, 0, findingRowID)
	assertCount(`SELECT COUNT(*) FROM activity WHERE id=$1`, 0, activityID)
	assertCount(`SELECT COUNT(*) FROM activity WHERE id=$1`, 1, keptActivityID)
	assertCount(`SELECT COUNT(*) FROM exploration_edges WHERE exploration_id=$1 AND (src_id=$2 OR dst_id=$2)`, 0, expID, intentID)
	assertCount(`SELECT COUNT(*) FROM exploration_anchors WHERE node_id=$1`, 0, directFact)
	assertCount(`SELECT COUNT(*) FROM assets WHERE id=$1`, 1, assetID)
}

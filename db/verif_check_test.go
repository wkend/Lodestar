package db

import (
	"strconv"
	"testing"
)

// ListFindingsPage / ListFindings 必须带出 verification（全局发现页列表徽章的
// 数据源）；GetFinding 的详情路径不得回归。
func TestListFindingsPageVerification(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v)", err)
	}
	defer d.Close()
	task, err := d.CreateTask("verif", "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = d.Exec(`DELETE FROM tasks WHERE id=$1`, task.ID) }() // cascades
	es := d.Exploration(task.ExplorationID)
	nodeID, err := es.AddNode(KindFinding, map[string]any{"summary": "s", "vulnclass": "SQLi", "severity": "high", "evidence": "e"}, 0, "confirmed", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := es.SetFindingVerification(nodeID, false, "PoC 不可复现", "verifier"); err != nil {
		t.Fatal(err)
	}
	// findings 表行由 report_finding 正常写入；此处直接插一行关联 node（等价路径）
	var rowID int64
	if err := d.QueryRow(`INSERT INTO findings (task_id, node_id, vulnclass, name, severity, summary, evidence, worker)
		VALUES ($1, $2, 'SQLi', 'n', 'high', 's', 'e', 'worker') RETURNING id`, task.ID, nodeID).Scan(&rowID); err != nil {
		t.Fatal(err)
	}
	page, total, err := d.ListFindingsPage(FindingFilter{TaskID: strconv.FormatInt(task.ID, 10)}, 1, 10)
	if err != nil || total != 1 {
		t.Fatalf("page: %v total=%d err=%v", page, total, err)
	}
	got := page[0]
	if got.Verification == nil || got.Verification.Verified || got.Verification.Issues != "PoC 不可复现" {
		t.Fatalf("list verification missing/wrong: %+v", got.Verification)
	}
	one, err := d.GetFinding(got.ID)
	if err != nil || one == nil || one.Verification == nil || one.Verification.Verified {
		t.Fatalf("detail verification regressed: %+v err=%v", one, err)
	}
	// 导出路径有自己的 Scan 循环（列数变更曾在此漏扫导致 500）——锁死四个入口。
	exports, err := d.ListFindingsForExport(FindingFilter{TaskID: strconv.FormatInt(task.ID, 10)}, nil)
	if err != nil || len(exports) != 1 {
		t.Fatalf("export: %v err=%v", exports, err)
	}
	if exports[0].Verification == nil || exports[0].Verification.Verified {
		t.Fatalf("export verification missing: %+v", exports[0].Verification)
	}
}

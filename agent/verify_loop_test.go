package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/wkend/lodestar/db"
	actool "github.com/Autumn-27/norma/tool"
)

// callToolJSON invokes a CoreTool and returns (text, isError).
func callToolJSON(t *testing.T, tool actool.CoreTool, payload any) (string, bool) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	res, err := tool.Call(context.Background(), raw, nil)
	if err != nil {
		t.Fatalf("tool call error: %v", err)
	}
	return res.Flatten(), res.IsError
}

// newTestExploration opens a fresh exploration on the test DB (skips without PG).
func newTestExploration(t *testing.T) *db.ExplorationStore {
	t.Helper()
	d := testDB(t)
	t.Cleanup(func() { d.Close() })
	expID, err := d.CreateExploration("verify loop", "test goal")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM explorations WHERE id=$1`, expID) }) // cascades nodes
	return d.Exploration(expID)
}

// B1: graphOverviewData must surface the OWN task's recent findings with their
// verification verdicts (cap 10) — before, only other tasks' findings carried
// verification status and the own task showed a bare count.
func TestGraphOverviewRecentFindings(t *testing.T) {
	es := newTestExploration(t)
	ids := make([]int64, 0, 12)
	for i := 0; i < 12; i++ {
		id, err := es.AddNode(db.KindFinding, map[string]any{"summary": "vuln", "severity": "high"}, 0, "confirmed", "worker", nil)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	// newest finding (ids[11]) failed verification; ids[10] passed.
	if err := es.SetFindingVerification(ids[11], false, "复现失败", "verifier"); err != nil {
		t.Fatal(err)
	}
	if err := es.SetFindingVerification(ids[10], true, "", "verifier"); err != nil {
		t.Fatal(err)
	}

	tsx := NewToolSet(es, "planner")
	data := tsx.graphOverviewData()
	recent, ok := data["recent_findings"].([]map[string]any)
	if !ok {
		t.Fatalf("recent_findings missing, got %T", data["recent_findings"])
	}
	if len(recent) != 10 {
		t.Fatalf("recent_findings cap: got %d want 10", len(recent))
	}
	// newest first: [0] = ids[11] (failed), [1] = ids[10] (passed)
	if recent[0]["id"] != ids[11] || recent[0]["verified"] != false || recent[0]["verify_issues"] != "复现失败" {
		t.Fatalf("refuted finding not surfaced: %+v", recent[0])
	}
	if recent[1]["id"] != ids[10] || recent[1]["verified"] != true {
		t.Fatalf("passed verdict not surfaced: %+v", recent[1])
	}
	if _, hasIssues := recent[1]["verify_issues"]; hasIssues {
		t.Fatalf("verified=true must not carry verify_issues: %+v", recent[1])
	}
	if data["findings"] != 12 {
		t.Fatalf("findings count: got %v want 12", data["findings"])
	}
}

// B2: record_verification verified=false must fire the notifyVerify callback
// (verified=true must not) so the verifier wakes a planner that would otherwise
// never re-plan — the verdict write changes no graph fingerprint.
func TestRecordVerificationNotifiesOnFailure(t *testing.T) {
	es := newTestExploration(t)
	fRefuted, err := es.AddNode(db.KindFinding, map[string]any{"summary": "RCE"}, 0, "confirmed", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	fPassed, err := es.AddNode(db.KindFinding, map[string]any{"summary": "SQLi"}, 0, "confirmed", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}

	tsx := NewToolSet(es, "verifier")
	var gotID int64
	var gotIssues string
	calls := 0
	tsx.SetNotifyVerify(func(findingID int64, issues string) {
		calls++
		gotID, gotIssues = findingID, issues
	})

	if _, isErr := callToolJSON(t, tsx.recordVerification(fRefuted), map[string]any{"verified": false, "issues": "重放返回 403"}); isErr {
		t.Fatal("record_verification failed=false should succeed")
	}
	if calls != 1 || gotID != fRefuted || gotIssues != "重放返回 403" {
		t.Fatalf("notify not fired on failure: calls=%d id=%d issues=%q", calls, gotID, gotIssues)
	}
	if _, isErr := callToolJSON(t, tsx.recordVerification(fPassed), map[string]any{"verified": true}); isErr {
		t.Fatal("record_verification verified=true should succeed")
	}
	if calls != 1 {
		t.Fatalf("verified=true must not notify: calls=%d", calls)
	}
}

// B2: the planner's trigger renderer spells out a verify_failed event.
func TestRenderTriggersVerifyFailed(t *testing.T) {
	es := newTestExploration(t)
	out := renderTriggers(es, []TriggerEvent{{Kind: "verify_failed", Detail: "finding #42 核验未通过：复现失败"}})
	if !strings.Contains(out, "finding #42 核验未通过：复现失败") || !strings.Contains(out, "prove_goal") {
		t.Fatalf("verify_failed trigger render: %q", out)
	}
}

// B3: prove_goal rejects ONLY an explicitly refuted finding — verified=true and
// unverified findings (verifier may be disabled) must still pass, and plain
// facts are unaffected.
func TestProveGoalRejectsRefutedFinding(t *testing.T) {
	es := newTestExploration(t)
	goalID, err := es.AddNode(db.KindGoal, map[string]any{"text": "拿到 RCE"}, 0, "open", "system", nil)
	if err != nil {
		t.Fatal(err)
	}
	newFinding := func(summary string) int64 {
		id, err := es.AddNode(db.KindFinding, map[string]any{"summary": summary}, 0, "confirmed", "worker", nil)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	refuted := newFinding("RCE via deserialization")
	passed := newFinding("SQLi in /login")
	unverified := newFinding("SSRF in /fetch")
	factID, err := es.AddNode(db.KindFact, map[string]any{"summary": "admin 后台可登录"}, 0, "confirmed", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := es.SetFindingVerification(refuted, false, "PoC 不可复现", "verifier"); err != nil {
		t.Fatal(err)
	}
	if err := es.SetFindingVerification(passed, true, "", "verifier"); err != nil {
		t.Fatal(err)
	}

	tsx := NewToolSet(es, "planner")
	prove := tsx.proveGoal()
	payload := func(evidence int64) map[string]any {
		return map[string]any{"goal_id": goalID, "evidence_id": evidence, "reason": "确认证据"}
	}

	// refuted → rejected with the reason inline
	if text, isErr := callToolJSON(t, prove, payload(refuted)); !isErr || !strings.Contains(text, "核验未通过") {
		t.Fatalf("refuted finding must be rejected, got: %q", text)
	}
	// verified=true / unverified finding / plain fact → accepted
	for _, ev := range []int64{passed, unverified, factID} {
		if _, isErr := callToolJSON(t, prove, payload(ev)); isErr {
			t.Fatalf("evidence %d must be accepted", ev)
		}
	}
}

// N5: list_facts q must match detail content too (technique names / CVE ids often
// live in detail), not only the one-line summary.
func TestListFactsQMatchesDetail(t *testing.T) {
	es := newTestExploration(t)
	if _, err := es.AddNode(db.KindFact, map[string]any{
		"summary": "接口安全检查完成",
		"detail":  "配置文件中发现 JWT 密钥硬编码 (CVE-2026-1234)",
	}, 0, "confirmed", "worker", nil); err != nil {
		t.Fatal(err)
	}

	tsx := NewToolSet(es, "planner")
	listFacts := tsx.listFacts()

	text, _ := callToolJSON(t, listFacts, map[string]any{"q": "JWT"})
	var page struct {
		Facts []map[string]any `json:"facts"`
		Total int             `json:"total"`
	}
	if err := json.Unmarshal([]byte(text), &page); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, text)
	}
	if page.Total != 1 || len(page.Facts) != 1 || page.Facts[0]["summary"] != "接口安全检查完成" {
		t.Fatalf("q must hit detail-only content: total=%d %v", page.Total, page.Facts)
	}
	if text, _ := callToolJSON(t, listFacts, map[string]any{"q": "绝不存在的关键词"}); !strings.Contains(text, `"total":0`) {
		t.Fatalf("non-matching q must return total 0: %s", text)
	}
}

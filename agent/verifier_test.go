package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
)

// Pure (no database) checks for the verifier role: the verdict tool's argument
// contract and the code-owned prompt boundary.

func TestRecordVerificationRejectsFalseWithoutIssues(t *testing.T) {
	tools := NewToolSet(nil, "verifier")
	rec := tools.recordVerification(42)
	res, err := rec.Call(context.Background(), json.RawMessage(`{"verified":false,"issues":""}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Flatten(), "必须在 issues 中写明") {
		t.Fatalf("verified=false without issues must be rejected, got %q", res.Flatten())
	}
}

func TestRecordVerificationNeedsTaskContext(t *testing.T) {
	tools := NewToolSet(nil, "verifier")
	rec := tools.recordVerification(42)
	res, err := rec.Call(context.Background(), json.RawMessage(`{"verified":true}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Flatten(), "需要任务上下文") {
		t.Fatalf("verdict without a task store must not silently succeed, got %q", res.Flatten())
	}
}

func TestVerifierSystemCarriesBoundaryAndGoal(t *testing.T) {
	got := verifierSystem("拿下X", "/work")
	for _, want := range []string{"复核者", "拿下X", "不可信数据边界", "中间产物输出规约"} {
		if !strings.Contains(got, want) {
			t.Fatalf("verifier system prompt missing %q", want)
		}
	}
}

// cleanDeviations must not let a model that fills the field mechanically
// (`deviations: [{}]`) pollute every fact payload with an empty record.
func TestCleanDeviationsDropsEmptyEntries(t *testing.T) {
	got := cleanDeviations([]deviation{
		{},
		{Planned: " 直接打 /admin ", Actual: "", Reason: "404"},
		{Planned: "", Actual: "", Reason: "   "},
	})
	if len(got) != 1 {
		t.Fatalf("expected 1 surviving deviation, got %d", len(got))
	}
	if got[0].Planned != "直接打 /admin" || got[0].Reason != "404" {
		t.Fatalf("entries not trimmed: %+v", got[0])
	}
	if len(cleanDeviations(nil)) != 0 {
		t.Fatal("no deviations must yield zero entries so the payload omits the key")
	}
}

// The verdict must actually reach the planner: a finding the verifier could not
// re-check has to surface verified=false + the reason, or the whole role is dead data.
func TestCompactFindingCarriesVerification(t *testing.T) {
	pass := &db.Node{ID: 1, Kind: db.KindFinding, State: "confirmed",
		Payload: []byte(`{"summary":"A","severity":"high","verification":{"verified":true}}`)}
	got := compactFinding(pass)
	if v, ok := got["verified"].(bool); !ok || !v {
		t.Fatalf("verified=true must be surfaced: %+v", got)
	}
	if _, ok := got["verify_issues"]; ok {
		t.Fatalf("a passing verdict must not carry issues: %+v", got)
	}

	fail := &db.Node{ID: 2, Kind: db.KindFinding, State: "confirmed",
		Payload: []byte(`{"summary":"B","verification":{"verified":false,"issues":"重放返回 403"}}`)}
	got = compactFinding(fail)
	if v, ok := got["verified"].(bool); !ok || v {
		t.Fatalf("verified=false must be surfaced: %+v", got)
	}
	if got["verify_issues"] != "重放返回 403" {
		t.Fatalf("failure reason must reach the planner: %+v", got)
	}

	// unverified finding: no verdict keys at all, so an un-run check is distinguishable
	// from a failed one.
	bare := &db.Node{ID: 3, Kind: db.KindFinding, State: "confirmed", Payload: []byte(`{"summary":"C"}`)}
	got = compactFinding(bare)
	if _, ok := got["verified"]; ok {
		t.Fatalf("unverified finding must not claim a verdict: %+v", got)
	}
}

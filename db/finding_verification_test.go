package db

import (
	"encoding/json"
	"testing"
)

// Stage 7b store contract: the verifier scans unverified findings, then records a
// verdict without touching the node's kind/state.

func TestFindingVerificationRoundTrip(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()
	expID, err := d.CreateExploration("test", "verifier store")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Exec(`DELETE FROM explorations WHERE id=$1`, expID) // cascades nodes
	es := d.Exploration(expID)

	f1, err := es.AddNode(KindFinding, map[string]any{"summary": "A"}, 0, "confirmed", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	f2, err := es.AddNode(KindFinding, map[string]any{"summary": "B"}, 0, "confirmed", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	// a fact must never show up in the verifier's backlog
	if _, err := es.AddNode(KindFact, map[string]any{"summary": "not a finding"}, 0, "confirmed", "worker", nil); err != nil {
		t.Fatal(err)
	}

	// oldest first, findings only
	got, err := es.ListUnverifiedFindings(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != f1 || got[1].ID != f2 {
		t.Fatalf("expected [f1,f2] oldest-first, got %v", nodeIDs(got))
	}

	// record a verdict → leaves the backlog, payload gains verification, state unchanged
	if err := es.SetFindingVerification(f1, false, "重放返回 403", "verifier"); err != nil {
		t.Fatal(err)
	}
	got, err = es.ListUnverifiedFindings(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != f2 {
		t.Fatalf("f1 should leave the backlog, got %v", nodeIDs(got))
	}

	n, err := es.GetNode(f1)
	if err != nil {
		t.Fatal(err)
	}
	if n.Kind != KindFinding || n.State != "confirmed" {
		t.Fatalf("verdict must not alter kind/state, got kind=%q state=%q", n.Kind, n.State)
	}
	var p struct {
		Summary      string `json:"summary"`
		Verification struct {
			Verified bool   `json:"verified"`
			Issues   string `json:"issues"`
			By       string `json:"by"`
		} `json:"verification"`
	}
	if err := json.Unmarshal(n.Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.Summary != "A" || p.Verification.Verified || p.Verification.Issues != "重放返回 403" || p.Verification.By != "verifier" {
		t.Fatalf("verification not merged alongside existing payload: %s", n.Payload)
	}

	// non-finding id is rejected (kind-scoped UPDATE), so a verdict can't land elsewhere
	if err := es.SetFindingVerification(f2, true, "", "verifier"); err != nil {
		t.Fatal(err)
	}
	factID, err := es.AddNode(KindFact, map[string]any{"summary": "x"}, 0, "confirmed", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := es.SetFindingVerification(factID, true, "", "verifier"); err == nil {
		t.Fatal("verdict on a fact node must be rejected")
	}
}

func nodeIDs(ns []*Node) []int64 {
	out := make([]int64, 0, len(ns))
	for _, n := range ns {
		out = append(out, n.ID)
	}
	return out
}

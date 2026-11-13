package db

import (
	"encoding/json"
	"testing"
)

// N7: the verifier backlog spans direct sources — an unverified finding
// inherited from a finished source task must surface here (with its owning
// store) or no one would ever verify it; a verdict written through that owning
// store lands in the source graph and clears the backlog.
func TestListUnverifiedFindingsWithSources(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()

	source, err := d.CreateTask("verify-source", "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.DeleteTask(source.ID) }()
	src := d.Exploration(source.ExplorationID)

	inherited, err := src.AddNode(KindFinding, map[string]any{"summary": "source RCE"}, 0, "confirmed", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	// already verified → never in the backlog
	verifiedID, err := src.AddNode(KindFinding, map[string]any{"summary": "source SQLi"}, 0, "confirmed", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := src.SetFindingVerification(verifiedID, true, "", "verifier"); err != nil {
		t.Fatal(err)
	}

	downstream, err := d.CreateTaskWithOptions("verify-downstream", "goal", TaskCreateOptions{SourceTaskIDs: []int64{source.ID}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.DeleteTask(downstream.ID) }()
	down := d.Exploration(downstream.ExplorationID)

	ownID, err := down.AddNode(KindFinding, map[string]any{"summary": "own SSRF"}, 0, "confirmed", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}

	got, err := down.ListUnverifiedFindingsWithSources(20)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("backlog=%d want 2 (own + inherited; verified excluded)", len(got))
	}
	if got[0].Node.ID != ownID || got[0].Store != nil {
		t.Fatalf("own finding must come first with nil Store: %+v", got[0])
	}
	if got[1].Node.ID != inherited || got[1].Store == nil || got[1].Store.ID() != source.ExplorationID {
		t.Fatalf("inherited finding must carry the OWNING store: %+v", got[1])
	}

	// verdict written through the owning store lands in the source graph …
	if err := got[1].Store.SetFindingVerification(inherited, false, "PoC 不可复现", "verifier"); err != nil {
		t.Fatal(err)
	}
	n, err := src.GetNode(inherited)
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		Verification struct {
			Verified bool `json:"verified"`
		} `json:"verification"`
	}
	if err := json.Unmarshal(n.Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.Verification.Verified {
		t.Fatal("verdict must be visible in the source graph")
	}
	// … and clears the backlog
	if got, err = down.ListUnverifiedFindingsWithSources(20); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Node.ID != ownID {
		t.Fatalf("verdict must clear the inherited backlog entry: %+v", got)
	}
}

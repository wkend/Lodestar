package server

import (
	"testing"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
)

// seedPrompts iterates agent.BuiltinPromptSeeds() and skips any key whose agent row
// is missing — silently. A role that ships a code-owned default prompt but has no
// builtinAgents entry therefore never reaches agent_prompts: the UI can't show it,
// edit it, or restore it. This asserts the two lists stay in sync, so adding a
// prompt for a new role without its agent row fails here instead of failing quietly
// in production.
func TestBuiltinPromptSeedsHaveAgentRows(t *testing.T) {
	dsn, _, err := db.DSN()
	if err != nil {
		t.Skipf("no database config (%v) — skipping", err)
	}
	pg, err := db.Open(dsn)
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer pg.Close()

	for key := range agent.BuiltinPromptSeeds() {
		a, err := pg.GetAgentByKey(key)
		if err != nil {
			t.Fatalf("lookup agent %q: %v", key, err)
		}
		if a == nil {
			t.Fatalf("prompt catalog has %q but no agents row exists — seedPrompts will skip it silently", key)
		}
	}
}

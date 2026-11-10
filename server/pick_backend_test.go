package server

import (
	"context"
	"testing"

	"github.com/wkend/lodestar/agent"
	"github.com/wkend/lodestar/db"
	"github.com/Autumn-27/norma/harness"
)

// stubBackend is a no-op AgentBackend used to check pickBackend's selection.
type stubBackend struct{ tag string }

func (s *stubBackend) Execute(context.Context, string, int64, *db.AssetStore, *db.ExplorationStore,
	*db.Node, harness.HookRunner, func(db.Activity), agent.EnrichTrigger, func(int64, string),
) (harness.TerminalReason, agent.WriteCounts, error) {
	return harness.ReasonCompleted, agent.WriteCounts{}, nil
}

func (s *stubBackend) ExecuteWithMessage(context.Context, string, int64, *db.AssetStore, *db.ExplorationStore,
	*db.Node, harness.HookRunner, func(db.Activity), agent.EnrichTrigger, func(int64, string), string, string,
) (harness.TerminalReason, agent.WriteCounts, error) {
	return harness.ReasonCompleted, agent.WriteCounts{}, nil
}

// pickBackend: CLI worker wins when set; norma is the primary (and self-fallback)
// otherwise. This is the whole stage-3 dispatch decision.
func TestPickBackend(t *testing.T) {
	t.Parallel()
	norma := &stubBackend{tag: "norma"}
	e := &Engine{}

	if got := e.pickBackend(norma); got != norma {
		t.Fatalf("no CLI worker: primary must be norma, got %v", got)
	}

	cli := &stubBackend{tag: "cli"}
	e.SetCLIWorker(cli)
	if got := e.pickBackend(norma); got != cli {
		t.Fatalf("CLI worker set: primary must be CLI, got %v", got)
	}
}

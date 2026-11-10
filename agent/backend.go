package agent

import (
	"context"

	"github.com/wkend/lodestar/db"
	"github.com/Autumn-27/norma/harness"
)

// AgentBackend is the seam that lets the engine drive different worker
// implementations behind one interface. It is lifted verbatim from *Worker's
// existing Execute / ExecuteWithMessage signatures, so *Worker satisfies it with
// no changes.
//
// The two implementations Lodestar targets:
//   - *Worker (NormaWorker): in-process norma harness. Full Lodestar capability —
//     real-time graph writeback, cross-worker trace search, guard hooks.
//   - CLIWorker (planned): delegates one intent to a general-purpose agent CLI
//     subprocess (claude/codex) and writes the batched result back through the
//     same in-process ToolSet.
//
// The engine picks the backend per intent (see pickBackend) but calls it only
// through this interface, so the worker loop and retry logic stay backend-blind.
type AgentBackend interface {
	// Execute runs one intent. See (*Worker).Execute for the full contract of
	// hooks / emit / notifyFinding and the return tuple.
	Execute(ctx context.Context, name string, taskID int64, as *db.AssetStore,
		ts *db.ExplorationStore, intent *db.Node, hooks harness.HookRunner,
		emit func(db.Activity), enr EnrichTrigger, notifyFinding func(int64, string),
	) (harness.TerminalReason, WriteCounts, error)

	// ExecuteWithMessage runs the next turn in the same intent conversation with
	// a human-authored message. See (*Worker).ExecuteWithMessage.
	ExecuteWithMessage(ctx context.Context, name string, taskID int64, as *db.AssetStore,
		ts *db.ExplorationStore, intent *db.Node, hooks harness.HookRunner,
		emit func(db.Activity), enr EnrichTrigger, notifyFinding func(int64, string),
		requestID, message string,
	) (harness.TerminalReason, WriteCounts, error)
}

// compile-time assertion: *Worker must satisfy AgentBackend.
var _ AgentBackend = (*Worker)(nil)

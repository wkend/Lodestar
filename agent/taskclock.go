package agent

import (
	"context"
	"fmt"
	"math"
	"time"
)

// TaskClock carries a task's absolute deadline into a worker/planner run so the run
// can clamp its own wall-clock budget to the task's remaining time and pick the
// right wrap-up words (per-run vs task-timeout). Attached to the run ctx by the
// engine. Zero value = no task-level timeout (behaves exactly as before).
type TaskClock struct {
	DeadlineUnix int64 // absolute deadline (unix seconds); 0 = no task timeout
	Final        bool  // coordinator-driven FINAL planner round (task ending now)
}

type taskClockKey struct{}

// WithTaskClock attaches a TaskClock to ctx for the run.
func WithTaskClock(ctx context.Context, tc TaskClock) context.Context {
	return context.WithValue(ctx, taskClockKey{}, tc)
}

// taskClockFrom reads the TaskClock (zero value if none attached).
func taskClockFrom(ctx context.Context) TaskClock {
	if v, ok := ctx.Value(taskClockKey{}).(TaskClock); ok {
		return v
	}
	return TaskClock{}
}

// clampMaxDuration folds a task deadline into a run's own wall-clock budget.
//   - ownBudget = the agent's own run_seconds (0 = unlimited).
//   - Returns eff = the MaxDuration to use (floored at 1s so we never pass ≤0, which
//     harness reads as "unlimited"), and clamped = whether the TASK deadline is the
//     binding constraint (remaining ≤ ownBudget, or ownBudget unlimited). When there
//     is no deadline, returns (ownBudget, false) unchanged.
func clampMaxDuration(deadlineUnix int64, ownBudget time.Duration) (eff time.Duration, clamped bool) {
	if deadlineUnix <= 0 {
		return ownBudget, false
	}
	remaining := time.Until(time.Unix(deadlineUnix, 0))
	if remaining < time.Second {
		remaining = time.Second // max(1, …): never pass ≤0 (harness treats 0 as unlimited)
	}
	// clamped when the task deadline binds this run's time: remaining ≤ own budget,
	// or the agent has no own time budget (then remaining always binds).
	clamped = ownBudget <= 0 || remaining <= ownBudget
	eff = remaining
	if ownBudget > 0 && ownBudget < remaining {
		eff = ownBudget
	}
	return eff, clamped
}

// remainingBudgetLine renders the time left before the task deadline as a
// strategy nudge for the planner's situational block (sapper-v2's time_budget):
// near the deadline the planner should prefer cheap high-yield work over deep
// rabbit holes. The deadline machinery clamps run budgets, but without this
// line the planner never SEES the clock until the final round — too late to
// change strategy.
func remainingBudgetLine(deadlineUnix int64) string {
	remaining := time.Until(time.Unix(deadlineUnix, 0))
	var left string
	switch {
	case remaining <= 0:
		left = "已到点"
	case remaining >= time.Hour:
		left = fmt.Sprintf("约剩 %.1f 小时", remaining.Hours())
	default:
		// 向上取整：剩 20 秒显示"1 分钟"而不是误导性的"0 分钟"
		left = fmt.Sprintf("约剩 %.0f 分钟", math.Ceil(remaining.Minutes()))
	}
	return left + "。时间紧时优先高性价比动作（凭据复用/已知 CVE 直打/未授权接口），放弃深坑探索。"
}

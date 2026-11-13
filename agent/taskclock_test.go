package agent

import (
	"strings"
	"testing"
	"time"
)

// N6: the planner's situational time-budget line renders the remaining time and
// the cheap-wins-first nudge. Injected into Plan() only when a deadline exists
// and the round is not the final one.
func TestRemainingBudgetLine(t *testing.T) {
	withDeadline := remainingBudgetLine(time.Now().Add(90 * time.Minute).Unix())
	if !strings.Contains(withDeadline, "1.5 小时") || !strings.Contains(withDeadline, "高性价比") {
		t.Fatalf("90min budget line: %q", withDeadline)
	}
	minutes := remainingBudgetLine(time.Now().Add(10 * time.Minute).Unix())
	if !strings.Contains(minutes, "10 分钟") {
		t.Fatalf("10min budget line: %q", minutes)
	}
	// 剩 20 秒：向上取整显示"1 分钟"，不显示误导性的"0 分钟"
	seconds := remainingBudgetLine(time.Now().Add(20 * time.Second).Unix())
	if !strings.Contains(seconds, "1 分钟") {
		t.Fatalf("20s budget line: %q", seconds)
	}
	over := remainingBudgetLine(time.Now().Add(-time.Minute).Unix())
	if !strings.Contains(over, "已到点") {
		t.Fatalf("past-deadline budget line: %q", over)
	}
}

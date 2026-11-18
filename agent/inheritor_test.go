package agent

import "testing"

// TestShouldInherit 验证继承触发判定：只有零写回的横死才继承；干净完成是 worker
// 自己的判断，尊重不提。
func TestShouldInherit(t *testing.T) {
	zero := WriteCounts{}
	cases := []struct {
		name  string
		state string
		wrote WriteCounts
		want  bool
	}{
		{"planner 处决+零写回", "stopped", zero, true},
		{"错误+零写回", "blocked", zero, true},
		{"预算耗尽+零写回", "exhausted", zero, true},
		{"干净完成不继承", "done", zero, false},
		{"暂停回退不继承", "open", zero, false},
		{"处决但已有写回不继承", "stopped", WriteCounts{Facts: 1}, false},
		{"处决但只写了资产不继承", "stopped", WriteCounts{Assets: 2}, false},
		{"错误但已报 finding 不继承", "blocked", WriteCounts{Findings: 1}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ShouldInherit(c.state, c.wrote); got != c.want {
				t.Fatalf("ShouldInherit(%q, %+v)=%v want %v", c.state, c.wrote, got, c.want)
			}
		})
	}
}

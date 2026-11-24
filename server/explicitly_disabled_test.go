package server

import "testing"

// TestExplicitlyDisabled 覆盖"默认启用"开关的判据语义（发现 16 回归）：
// 未设置（空串）与空白的非 falsy 值都不算关闭；仅显式假值关闭。
func TestExplicitlyDisabled(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false}, // 未设置 = 默认启用（isFalsy 对空串返回 true，不能混用）
		{"1", false},
		{"true", false},
		{"yes", false},
		{"on", false},
		{"  ", false},
		{"0", true},
		{"false", true},
		{"no", true},
		{"off", true},
		{" OFF ", true},
		{" False ", true},
	}
	for _, c := range cases {
		if got := explicitlyDisabled(c.in); got != c.want {
			t.Errorf("explicitlyDisabled(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

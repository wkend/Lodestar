package agent

import (
	"testing"
	"time"
)

// TestSettleGraceFloorFromLatency 宽限自适应映射（判读报告发现 12）：
// 底 240s（无观测/低延迟），3× 延迟上浮（长尾回合），上限 10min。
func TestSettleGraceFloorFromLatency(t *testing.T) {
	cases := []struct {
		latency time.Duration
		want    time.Duration
	}{
		{0, 240 * time.Second},                        // 无观察 → 底
		{10 * time.Second, 240 * time.Second},         // 低延迟 3×=30s < 底 → 底
		{64 * time.Second, 240 * time.Second},         // 尾部延迟 3×=192s < 底 → 底
		{130 * time.Second, 390 * time.Second},  // p95 长尾 3×=390s → 上浮
		{200 * time.Second, 600 * time.Second},  // 3×=600s → 10min 上限内
		{392 * time.Second, 600 * time.Second},  // 超长回合 3×=1176s → 上限 10min
	}
	for _, c := range cases {
		if got := settleGraceFloorFromLatency(c.latency); got != c.want {
			t.Fatalf("settleGraceFloorFromLatency(%v)=%v want %v", c.latency, got, c.want)
		}
	}
}

// TestWorkerSettleHardGraceAdaptive 验证 Worker 的观察器驱动宽限上浮。
func TestWorkerSettleHardGraceAdaptive(t *testing.T) {
	w := NewWorker(nil, "m", "", nil, 0, 0)
	if got := w.settleHardGrace(); got != 240*time.Second {
		t.Fatalf("无观察应为底 240s, got %v", got)
	}
	w.observeLatency(150 * time.Second)
	if got := w.settleHardGrace(); got != 450*time.Second {
		t.Fatalf("3×150s=450s, got %v", got)
	}
	// 单调不衰减：更小的观察不降低宽限。
	w.observeLatency(5 * time.Second)
	if got := w.settleHardGrace(); got != 450*time.Second {
		t.Fatalf("观察值只升不降, got %v", got)
	}
	// 零/负观察忽略。
	w.observeLatency(0)
	w.observeLatency(-time.Second)
	if got := w.settleHardGrace(); got != 450*time.Second {
		t.Fatalf("零负观察忽略, got %v", got)
	}
}

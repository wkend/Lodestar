package server

import (
	"testing"
	"time"
)

// 心跳空转门控：图没变、也没有待领/在跑的意图时，一个 30 分钟窗口内不规划，
// 满两个窗口（60 分钟）才规划一次。任何变化都清零计数。
func TestPlannerIdleGate(t *testing.T) {
	t0 := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	at := func(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }

	t.Run("未规划过的任务首个心跳必须规划", func(t *testing.T) {
		var g plannerIdleGate
		if !g.dueHeartbeat(at(10), "fp-a", false) {
			t.Fatal("no baseline yet: heartbeat must plan")
		}
	})

	t.Run("一个窗口内图无变化的心跳不规划", func(t *testing.T) {
		var g plannerIdleGate
		g.planned(at(0), "fp-a")
		for _, m := range []int{10, 20, 30, 40, 50} {
			if g.dueHeartbeat(at(m), "fp-a", false) {
				t.Fatalf("idle heartbeat at +%dm must be skipped", m)
			}
		}
	})

	t.Run("满两个窗口 60 分钟图无变化则规划一次", func(t *testing.T) {
		var g plannerIdleGate
		g.planned(at(0), "fp-a")
		if !g.dueHeartbeat(at(60), "fp-a", false) {
			t.Fatal("idle for 60m must plan once")
		}
		g.planned(at(60), "fp-a") // 规划后计数重新开始
		if g.dueHeartbeat(at(70), "fp-a", false) {
			t.Fatal("counter must restart after a forced round")
		}
	})

	t.Run("图指纹变化立即规划并清零计数", func(t *testing.T) {
		var g plannerIdleGate
		g.planned(at(0), "fp-a")
		if g.dueHeartbeat(at(20), "fp-b", false) != true {
			t.Fatal("changed graph must plan at once")
		}
		g.planned(at(20), "fp-b") // 变化后的那轮规划把基线挪到 +20
		if g.dueHeartbeat(at(60), "fp-b", false) {
			t.Fatal("60m is only 40m after the change: must still skip")
		}
		if !g.dueHeartbeat(at(80), "fp-b", false) {
			t.Fatal("60m after the change, idle: must plan")
		}
	})

	t.Run("有待领或在跑的意图时不跳过", func(t *testing.T) {
		var g plannerIdleGate
		g.planned(at(0), "fp-a")
		if !g.dueHeartbeat(at(10), "fp-a", true) {
			t.Fatal("active intent must never be skipped")
		}
	})
}

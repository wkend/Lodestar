package server

import "time"

// plannerIdleWindow 是空转的一个窗口。图无变化且没有待领/在跑的意图时，心跳
// 唤醒先不规划；满两个窗口（60 分钟）仍无变化才规划一次，兜住资产库等图外变化。
const plannerIdleWindow = 30 * time.Minute

const plannerIdleLimit = 2 * plannerIdleWindow

// plannerIdleGate 记录任务上一次规划时的图指纹与时间，判断心跳是否需要真正规划。
// 每个 plannerLoop 持有一份，不跨任务共享。
type plannerIdleGate struct {
	started bool
	fp      string    // 上一次规划结束时的图指纹
	since   time.Time // 上一次规划结束的时间（计数起点）
}

// dueHeartbeat 报告心跳在 now 时是否应当规划。active 表示仍有待领或在跑的意图。
// 指纹变化、有活跃意图、或空转满 plannerIdleLimit，都要规划。
func (g *plannerIdleGate) dueHeartbeat(now time.Time, fp string, active bool) bool {
	return !g.started || active || fp != g.fp || now.Sub(g.since) >= plannerIdleLimit
}

// planned 在一轮规划结束后调用，把当时的图记为新基线并重开计数。
func (g *plannerIdleGate) planned(now time.Time, fp string) {
	g.started = true
	g.fp = fp
	g.since = now
}

// heartbeatDue 为心跳唤醒读取图指纹与活跃意图，交给门控判断。查询出错时按需规划，不吞掉工作。
func heartbeatDue(t *Task, g *plannerIdleGate) bool {
	fp, err := t.Store.GraphFingerprint()
	if err != nil {
		return true
	}
	active, err := t.Store.HasActiveIntent()
	if err != nil {
		return true
	}
	return g.dueHeartbeat(time.Now(), fp, active)
}

package server

// L1 代谢端到端验证（env 门控，默认 skip）：
//   LODESTAR_L1_E2E=1 时运行。用 juice-shop（127.0.0.1:3000，需先 docker start
//   lodestar-juiceshop）跑一个极短预算真实任务，验证代谢闭环第一步：
//     ① 意图终态 → campaign_yield 表有原始计数行（记账写入路径）
//   后续步骤（② 饿瘦触发 ③ 倍率浮动）需 10+ 战役运行积累，见继承者_e2e 同款模式。
//
// 用真实 LLM（.env 的 GLM/Anthropic 兼容端点）。任务用极短预算（120s）控制成本。
// 注意：本测试会消耗真实 LLM token。

import (
	"context"
	"os"
	stdstrconv "strconv"
	"testing"
	"time"

	"github.com/wkend/lodestar/agent"
	"github.com/wkend/lodestar/db"
)

func TestL1MetabolismE2E(t *testing.T) {
	if os.Getenv("LODESTAR_L1_E2E") == "" {
		t.Skip("LODESTAR_L1_E2E 未设置——跳过 L1 代谢端到端验证")
	}
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v)", err)
	}
	defer m.Close()

	cfg, ok := agent.FromEnv()
	if !ok {
		t.Skip("LLM env 未配置——跳过")
	}
	_ = cfg

	// 跑一个极短预算任务（120s）：worker 全程真实干活，撞任务墙自然终态。
	// 终态后验证 campaign_yield 表有行（记账组件 ①）。
	goal := "对 http://127.0.0.1:3000 做最小化渗透测试侦察：确认技术栈和主要页面，不做利用。"
	task, err := m.CreateTaskWithOptions("L1 代谢 e2e 任务", goal, db.TaskCreateOptions{
		TimeoutSeconds: 120,
	})
	if err != nil {
		t.Fatal(err)
	}
	taskID64, _ := stdstrconv.ParseInt(task.ID, 10, 64)

	// 完整 Server 装配（engine/planner/worker 都要真活——launchTask 走 admitTask）。
	// 借用 archives 测试的模式：先取消 ctx 让后台协程快速退出，但引擎循环在
	// launchTask 路径上按需启动，不受影响。
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dataDir := t.TempDir()
	s := New(ctx, m, dataDir, dataDir, dataDir)
	s.launchTask(task, goal, true)

	// 轮询终态。
	deadline := time.Now().Add(4 * time.Minute)
	for time.Now().Before(deadline) {
		if t2, ok := m.Task(task.ID); ok {
			if status := s.resolvedTaskStatus(t2); status == "done" || status == "failed" || status == "timeout" {
				break
			}
		}
		time.Sleep(5 * time.Second)
	}

	// 验证 ①：campaign_yield 有行。
	pg := m.PG()
	var n int
	pg.QueryRow(`SELECT count(*) FROM campaign_yield WHERE exploration_id=$1`, task.Store.ID()).Scan(&n)
	if n == 0 {
		t.Fatalf("campaign_yield 表零行——记账未触发（意图终态没走 recordCampaignYield？）")
	}
	t.Logf("✓ 记账组件 ①：%d 条战役行落表", n)

	// 打印战役分布（诊断视图）。
	rows, err := pg.Query(`SELECT campaign, facts, assets, findings FROM campaign_yield WHERE exploration_id=$1 ORDER BY id`, task.Store.ID())
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var camp string
		var f, a, fi int
		rows.Scan(&camp, &f, &a, &fi)
		t.Logf("  战役 %.60s: facts=%d assets=%d findings=%d", camp, f, a, fi)
	}
	_ = taskID64
}


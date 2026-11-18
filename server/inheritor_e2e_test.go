package server

// 继承者端到端复演（env 门控，默认 skip）：
//   LODESTAR_INHERITOR_E2E=1 + LODESTAR_INHERITOR_E2E_DSN=<显微镜库 DSN> 时运行。
//   DSN 走独立变量而非 LODESTAR_PG_DSN——TestMain 的 PrepareTestDatabase 会改写
//   LODESTAR_PG_DSN 指向一次性测试库（db.Open 在测试进程内永远连测试库）。
//
// 复演对象是 2026-11-16 ATLAS 深挖跑里横死的意图 #2：48 次工具调用（WAF 规则摸清）
// 零写回、被 planner 盲杀——它的 WAF 情报在轨迹里躺了一天。本测试对它跑一遍真实的
// 继承者（LLM provider 来自 env），断言：遗产（fact 节点）真的进了图。
//
// 注意：会向该库的探索图 1 写入 fact（正是复演的目的）。

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/wkend/lodestar/agent"
	"github.com/wkend/lodestar/db"
)

func TestInheritorE2ERecoverDeadIntent(t *testing.T) {
	if os.Getenv("LODESTAR_INHERITOR_E2E") == "" {
		t.Skip("LODESTAR_INHERITOR_E2E 未设置——跳过端到端复演")
	}
	dsn := strings.TrimSpace(os.Getenv("LODESTAR_INHERITOR_E2E_DSN"))
	if dsn == "" {
		t.Skip("LODESTAR_INHERITOR_E2E_DSN 未设置——需指向显微镜库")
	}
	pg, err := db.Open(dsn)
	if err != nil {
		t.Fatalf("连接显微镜库失败: %v", err)
	}
	defer pg.Close()

	// 任务 #1 → 探索图 1；死者 = 意图 #2（stopped，48 步轨迹零写回）。
	ts := pg.Exploration(1)
	node, err := ts.GetNode(2)
	if err != nil || node == nil {
		t.Fatalf("取死者意图 #2 失败: %v", err)
	}
	if node.State != "stopped" {
		t.Fatalf("意图 #2 状态 %q 非 stopped——库不对或已被处理", node.State)
	}
	goalNode, err := ts.GetNode(3)
	if err != nil || goalNode == nil {
		t.Fatalf("取任务目标节点失败: %v", err)
	}
	var gp struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(goalNode.Payload, &gp)

	// 真实 provider（.env 的 GLM/Anthropic 兼容端点）+ 真实继承者。
	cfg, ok := agent.FromEnv()
	if !ok {
		t.Skip("LLM env 未配置——跳过")
	}
	prov, err := cfg.NewProvider()
	if err != nil {
		t.Fatal(err)
	}
	inh := agent.NewInheritor(prov, cfg.Model, t.TempDir(), nil, cfg.CompactionWindow(), 12)

	ctx, cancel := context.WithTimeout(context.Background(), 14*time.Minute)
	defer cancel()
	emit := func(r db.Activity) { t.Logf("[继承者步骤] kind=%s tool=%s %.80s", r.Kind, r.Tool, r.Summary) }
	if err := inh.Inherit(ctx, 1, nil, ts, node, gp.Text, emit, nil, nil); err != nil {
		t.Fatalf("继承者执行失败: %v", err)
	}

	// 断言：图 1 里出现了新的 fact 节点（死者轨迹的遗产）。
	nodes, err := ts.ListByKind(db.KindFact, 100)
	if err != nil {
		t.Fatal(err)
	}
	var recovered []string
	for _, n := range nodes {
		if n.ID == 1 { // #1 是任务 origin fact，排除
			continue
		}
		var p struct {
			Summary string `json:"summary"`
		}
		_ = json.Unmarshal(n.Payload, &p)
		recovered = append(recovered, p.Summary)
	}
	if len(recovered) == 0 {
		t.Fatalf("继承者零产出：死者的 48 步轨迹没有任何 fact 被提取——机制或提示词失败")
	}
	t.Logf("回收遗产 %d 条:", len(recovered))
	for i, s := range recovered {
		t.Logf("  [%d] %s", i+1, clipStr(s, 120))
	}
	// 软断言：ATLAS 的关键情报（/api/search 的 WAF）应出现在遗产里。
	joined := strings.Join(recovered, "\n")
	if !strings.Contains(joined, "search") && !strings.Contains(joined, "WAF") && !strings.Contains(joined, "waf") {
		t.Errorf("遗产未包含 /api/search 的 WAF 情报（软断言）——检查提取质量:\n%s", joined)
	}
}

func clipStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

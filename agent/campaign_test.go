package agent

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/wkend/lodestar/db"
)

// memLedger 内存版记账，测 BudgetFor 用。
type memLedger struct{ runs map[string][]CampaignRun }

func (m *memLedger) Append(c string, y CampaignYield) error {
	if m.runs == nil {
		m.runs = map[string][]CampaignRun{}
	}
	m.runs[c] = append(m.runs[c], CampaignRun{Campaign: c, RunIndex: len(m.runs[c]) + 1, Yield: y, At: time.Now()})
	return nil
}
func (m *memLedger) Recent(c string, n int) ([]CampaignRun, error) {
	r := m.runs[c]
	if len(r) > n {
		r = r[len(r)-n:]
	}
	return r, nil
}

func runs(yields ...CampaignYield) []CampaignRun {
	var out []CampaignRun
	base := time.Now().Add(-time.Hour)
	for i, y := range yields {
		out = append(out, CampaignRun{RunIndex: i + 1, Yield: y, At: base.Add(time.Duration(i) * time.Minute)})
	}
	return out
}

// TestBudgetFor 验证 L1 代谢倍率全路径（设计文档 §2.1）。
func TestBudgetFor(t *testing.T) {
	zero := CampaignYield{}
	yield := CampaignYield{Facts: 1}

	cases := []struct {
		name   string
		runs   []CampaignRun
		serial bool
		want   float64
	}{
		{"无记录 → 基础", nil, false, 1.0},
		{"观察期(9 次零产出) → 基础", runs(make([]CampaignYield, 9)...), false, 1.0},
		{"观察期满(10 次零产出) → 饿瘦", runs(make([]CampaignYield, 10)...), false, 0.75},
		{"近 3 次有产出 → 基础", runs(zero, zero, zero, zero, zero, zero, zero, zero, zero, zero, zero, yield), false, 1.0},
		{"连续 2 次零产出 → 饿瘦 0.75", runs(make([]CampaignYield, 11)...), false, 0.75},
		{"最近产出 finding → 加码 1.5", runs(append(make([]CampaignYield, 9), CampaignYield{Findings: 1})...), false, 1.5},
		{"串行链豁免代谢（不饿不加）", runs(make([]CampaignYield, 11)...), true, 1.0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := BudgetFor(c.runs, c.serial); got != c.want {
				t.Fatalf("BudgetFor(serial=%v)=%.2f want %.2f", c.serial, got, c.want)
			}
		})
	}
}

// TestScoreYield 验证折分：verified finding 全权重、未复核计 1、fact 2、asset 1。
func TestScoreYield(t *testing.T) {
	y := CampaignYield{Facts: 3, Assets: 2, Findings: 2}
	// 1 verified finding: 5 + 1(未复核) + 3×2 + 2×1 = 14
	if got := ScoreYield(y, 1); got != 14 {
		t.Fatalf("ScoreYield(1 verified)=%d want 14", got)
	}
	// 全 verified: 2×5 + 3×2 + 2×1 = 18
	if got := ScoreYield(y, 2); got != 18 {
		t.Fatalf("ScoreYield(2 verified)=%d want 18", got)
	}
	// verified 超过 findings 数不加分（防御）：同全 verified。
	if got := ScoreYield(y, 5); got != 18 {
		t.Fatalf("ScoreYield(超 verified 防御)=%d want 18", got)
	}
}

// TestParseSerial 验证 serial 标记提取。
func TestParseSerial(t *testing.T) {
	if !ParseSerial([]byte(`{"serial":true,"summary":"x"}`)) {
		t.Fatal("serial:true 应解析为 true")
	}
	if ParseSerial([]byte(`{"summary":"x"}`)) {
		t.Fatal("无 serial 字段应为 false")
	}
}

// TestCampaignLedgerRoundTrip 内存 ledger 经 BudgetFor 全链（DB 版由 server 集成测试覆盖）。
func TestCampaignLedgerRoundTrip(t *testing.T) {
	var l memLedger
	for i := 0; i < 11; i++ {
		if err := l.Append("camp", CampaignYield{}); err != nil {
			t.Fatal(err)
		}
	}
	r, err := l.Recent("camp", 16)
	if err != nil || len(r) != 11 {
		t.Fatalf("Recent 返回 %d 行 err=%v", len(r), err)
	}
	if got := BudgetFor(r, false); got != 0.75 {
		t.Fatalf("11 次零产出应饿瘦 0.75, got %.2f", got)
	}
	// 恢复：追加一次有产出 → 回基础。
	_ = l.Append("camp", CampaignYield{Facts: 1})
	r, _ = l.Recent("camp", 16)
	if got := BudgetFor(r, false); got != 1.0 {
		t.Fatalf("产出后应恢复基础, got %.2f", got)
	}
}

// TestCampaignMultiplierSerialCaste 验证寿命种姓表型的落点语义（审查纠错 3）：
// serial 加成在 campaignMultiplier 的 ledger 判空之前——无 ledger 的 worker 的
// serial 意图也必须拿到加成（路径属性不依赖台账存在性）。
func TestCampaignMultiplierSerialCaste(t *testing.T) {
	w := NewWorker(nil, "m", "", nil, 0, 0) // 无 provider 无 ledger
	mkIntent := func(payload string) *db.Node {
		return &db.Node{ID: 1, Kind: db.KindIntent, Payload: json.RawMessage(payload)}
	}
	// serial 意图，无 ledger → 仍 × serialBudgetFactor。
	if got := w.campaignMultiplier(mkIntent(`{"summary":"链步骤","serial":true}`)); got != serialBudgetFactor {
		t.Fatalf("无 ledger 的 serial 意图应 ×%.1f, got %.2f（路径属性不得依赖台账）", serialBudgetFactor, got)
	}
	// 非 serial 意图，无 ledger → 1.0（代谢降级路径不变）。
	if got := w.campaignMultiplier(mkIntent(`{"summary":"普通意图"}`)); got != 1.0 {
		t.Fatalf("无 ledger 的非 serial 意图应 1.0, got %.2f", got)
	}
	// payload 解析失败 → 1.0（降级永不阻断）。
	if got := w.campaignMultiplier(mkIntent(`{不是json`)); got != 1.0 {
		t.Fatalf("坏 payload 应降级 1.0, got %.2f", got)
	}
	// nil 意图 → 1.0。
	if got := w.campaignMultiplier(nil); got != 1.0 {
		t.Fatalf("nil 意图应 1.0, got %.2f", got)
	}
}

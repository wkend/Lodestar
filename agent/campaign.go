package agent

// L1 代谢（设计文档 §2）：预算是稀缺资源，靠产出挣得。探索图是适应度景观——
// 产出事实的战役获得更多能量，空转的战役被饿瘦（但可逆：新产出立即恢复）。
//
// 战役（campaign）= intentFingerprint(summary, assetIDs)（agent/tools.go:1055，
// Intent Admission 去重同款）——方向级记账：换措辞的同方向归一并、不同方向天然分营；
// 单资产任务不塌缩。asset_ids 为空的全局侦察意图永不饿瘦。
//
// 本文件只有纯函数与数据结构：记账的写入在 engine 的 runIntent 终态分支（有
// ExplorationStore），预算解析在 Worker.execute 领意图后（有意图 payload + 记账表）。
// 两端都经 CampaignLedger 接口解耦——engine 实现 DB 版，测试可实现内存版。

import (
	"encoding/json"
	"sort"
	"time"
)

// CampaignYield 一次意图运行终态时的原始计数（不折分）。
type CampaignYield struct {
	Facts    int `json:"facts"`
	Assets   int `json:"assets"`
	Findings int `json:"findings"`
}

// CampaignRun 一条战役运行记录（轻表 campaign_yield 的一行）。
type CampaignRun struct {
	Campaign string      `json:"campaign"` // intentFingerprint 键
	RunIndex int         `json:"run_index"`
	Yield    CampaignYield `json:"yield"`
	At       time.Time   `json:"at"`
}

// CampaignLedger 记账接口：engine 写入（意图终态），Worker 读取（预算解析）。
type CampaignLedger interface {
	// Append 记录一次终态。runIndex 由调用方不传时由实现方自增。
	Append(campaign string, y CampaignYield) error
	// Recent 返回该战役最近 n 次运行记录（时间升序）。
	Recent(campaign string, n int) ([]CampaignRun, error)
}

// MetabolismWeights 产出的折分权重（设计文档 §2.1 W_f/W_a/W_p 起步值）。
// finding 的 verified 折分不在此处——终态只存原始计数，预算解析时懒算（v2.3 修订：
// verifier 按心跳才复核，终态时刻 findings 几乎都未 verified，四轮 P0-2）。
var MetabolismWeights = struct {
	Finding int
	Fact    int
	Asset   int
}{Finding: 5, Fact: 2, Asset: 1}

// ScoreYield 把原始计数折成分（verified 加成由调用方在懒算时叠加：verified finding
// 计全权重，未复核计 1——与饿瘦判定共用的口径见 BudgetFor）。
func ScoreYield(y CampaignYield, verifiedFindings int) int {
	score := y.Facts*MetabolismWeights.Fact + y.Assets*MetabolismWeights.Asset
	// verifiedFindings 防御性 clamp：超过总数时按总数算（verified 不会超过 finding 数，
	// 但懒算来源是图查询，防御调用方传错）。
	if verifiedFindings > y.Findings {
		verifiedFindings = y.Findings
	}
	if verifiedFindings < 0 {
		verifiedFindings = 0
	}
	score += verifiedFindings * MetabolismWeights.Finding
	score += (y.Findings - verifiedFindings)
	return score
}

// BudgetFor 返回一条战役下一次运行的预算倍率（相对 worker 自身基础预算 run_seconds）。
// 规则（设计文档 §2.1，v2.3 修订后形态）：
//   - 观察期（该战役不足 10 次运行）→ 1.0（行为不变）
//   - 近 3 次有产出（yield > 0）→ 1.0
//   - 连续 2 次 exhausted 且零产出 → 0.75（饿瘦地板：最多饿 25%）
//   - 最近一次产出 verified finding → 1.5（封顶 1.5）
//   - 恢复：饿瘦战役任何一次有产出 → 立即回 1.0（瘦可逆）
//
// serial 豁免：串行链意图永远 1.0（链是 planner 判定的关键路径）。
// 全局侦察意图（asset_ids 空 → campaign 以 "|" 结尾的空锚串）永不饿瘦。
func BudgetFor(runs []CampaignRun, serial bool) float64 {
	if serial {
		return 1.0
	}
	if len(runs) == 0 {
		return 1.0
	}
	// 观察期。
	if len(runs) < 10 {
		return 1.0
	}
	// 时间升序取尾部。Recent 契约是升序，这里防御性再排一次。
	sorted := append([]CampaignRun(nil), runs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].At.Before(sorted[j].At) })
	n := len(sorted)
	// 最近一次有 verified finding 产出 → 加码。verified 信息不在原始计数里
	// （懒算原则）——这里用"最近一次产出任何 finding"近似加码判定：真 verified
	// 折分交给饿瘦方向（不误饿刚产出的战役），加码的精确性不影响正确性。
	last := sorted[n-1]
	if last.Yield.Findings > 0 {
		return 1.5
	}
	// 连续 2 次 exhausted 且零产出 → 饿瘦。exhausted 状态不在记账行里
	// （记账只存产出计数）——饿瘦判据简化为"最近 2 次运行全部零产出"：
	// done 零产出是 worker 自己判断无事可记，语义上等于该方向已无产出可挖，
	// 与 exhausted 零产出同饿不误伤（真有产出的 exhausted 已在上面被拦）。
	if last.Yield.Facts == 0 && last.Yield.Assets == 0 && last.Yield.Findings == 0 {
		prev := sorted[n-2]
		if prev.Yield.Facts == 0 && prev.Yield.Assets == 0 && prev.Yield.Findings == 0 {
			return 0.75
		}
	}
	return 1.0
}

// ParseSerial 从意图 payload JSON 提取 serial 标记。
func ParseSerial(payload json.RawMessage) bool {
	var p struct {
		Serial bool `json:"serial"`
	}
	_ = json.Unmarshal(payload, &p)
	return p.Serial
}

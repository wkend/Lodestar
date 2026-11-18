package server

// DBCampaignLedger 把 agent.CampaignLedger 适配到 PG 轻表 campaign_yield。
// 每个任务一个实例（expID 绑定）——Worker 的 SetCampaignLedger 由 engine 在
// per-task worker 构造时注入（engine.SetAgentResolver 闭包内，闭包捕获 t.Store）。

import (
	"github.com/wkend/lodestar/agent"
	"github.com/wkend/lodestar/db"
)

type dbCampaignLedger struct {
	pg    *db.DB
	expID int64
}

// Append implements agent.CampaignLedger.
func (l dbCampaignLedger) Append(campaign string, y agent.CampaignYield) error {
	return l.pg.AppendCampaignYield(l.expID, campaign, y.Facts, y.Assets, y.Findings)
}

// Recent implements agent.CampaignLedger（时间升序 → agent.CampaignRun）。
func (l dbCampaignLedger) Recent(campaign string, n int) ([]agent.CampaignRun, error) {
	rows, err := l.pg.RecentCampaignYields(l.expID, campaign, n)
	if err != nil || rows == nil {
		return nil, err
	}
	out := make([]agent.CampaignRun, 0, len(rows))
	for _, r := range rows {
		out = append(out, agent.CampaignRun{
			Campaign: campaign,
			RunIndex: r.RunIndex,
			Yield:    agent.CampaignYield{Facts: r.Facts, Assets: r.Assets, Findings: r.Findings},
			At:       r.At,
		})
	}
	return out, nil
}

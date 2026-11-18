package db

// L1 代谢轻表（设计文档 §2.1）：意图终态时的战役原始计数。
// 战役键 = intentFingerprint(summary, asset_ids)（agent 包同构函数算出后存字符串）。
// 意图可重跑多次，历史留痕；预算解析（BudgetFor）读最近 N 条。

import "time"

// EnsureCampaignYieldTable creates the campaign_yield ledger table (idempotent).
func (d *DB) EnsureCampaignYieldTable() error {
	_, err := d.Exec(`
CREATE TABLE IF NOT EXISTS campaign_yield (
	id BIGSERIAL PRIMARY KEY,
	exploration_id BIGINT NOT NULL,
	campaign TEXT NOT NULL,
	run_index INT NOT NULL DEFAULT 0,
	facts INT NOT NULL DEFAULT 0,
	assets INT NOT NULL DEFAULT 0,
	findings INT NOT NULL DEFAULT 0,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`)
	if err != nil {
		return err
	}
	_, err = d.Exec(`CREATE INDEX IF NOT EXISTS idx_campaign_yield_recent ON campaign_yield (exploration_id, campaign, id DESC)`)
	return err
}

// AppendCampaignYield records one terminal-state write-back count for a campaign.
func (d *DB) AppendCampaignYield(explorationID int64, campaign string, facts, assets, findings int) error {
	if campaign == "" {
		return nil
	}
	_, err := d.Exec(`INSERT INTO campaign_yield(exploration_id, campaign, facts, assets, findings)
VALUES ($1,$2,$3,$4,$5)`, explorationID, campaign, facts, assets, findings)
	return err
}

// CampaignYieldRow 是 RecentCampaignYields 的返回行（时间升序）。
type CampaignYieldRow struct {
	RunIndex int
	Facts    int
	Assets   int
	Findings int
	At       time.Time
}

// RecentCampaignYields returns the last n yield rows for one campaign (ascending time).
func (d *DB) RecentCampaignYields(explorationID int64, campaign string, n int) ([]CampaignYieldRow, error) {
	if campaign == "" || n <= 0 {
		return nil, nil
	}
	rows, err := d.Query(`SELECT run_index_f, facts, assets, findings, created_at FROM (
  SELECT facts, assets, findings, created_at,
         row_number() OVER (ORDER BY id DESC) AS run_index_f
  FROM campaign_yield WHERE exploration_id=$1 AND campaign=$2
  ORDER BY id DESC LIMIT $3
) t ORDER BY created_at ASC, run_index_f ASC`, explorationID, campaign, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CampaignYieldRow
	for rows.Next() {
		var r CampaignYieldRow
		if err := rows.Scan(&r.RunIndex, &r.Facts, &r.Assets, &r.Findings, &r.At); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

package db

import (
	"context"
	"testing"
	"time"
)

// TestReopenRateLimitedIntents 时间门控复位（发现 13）：
// rate_limited 且 blocked 超过 minAge 的意图回 open；新鲜的、其他标签的、
// 非 blocked 的都不动。
func TestReopenRateLimitedIntents(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()
	expID, err := d.CreateExploration("test", "rate limited reopen")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Exec(`DELETE FROM explorations WHERE id=$1`, expID) // cascades nodes
	es := d.Exploration(expID)

	// exploration_nodes 有 BEFORE UPDATE 触发器强制 updated_at=now()（schema.sql:238），
	// 回溯时间戳需在专用连接上以 replica 角色绕过触发器（lodestar 为超级用户）。
	ctx := context.Background()
	conn, err := d.Conn(ctx)
	if err != nil {
		t.Fatalf("conn: %v", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SET session_replication_role = replica`); err != nil {
		t.Skipf("cannot bypass updated_at trigger (需要超级用户): %v", err)
	}
	defer conn.ExecContext(ctx, `SET session_replication_role = origin`)

	add := func(summary, state, reason string, ageMinutes int) int64 {
		t.Helper()
		id, err := es.AddIntent(map[string]any{"summary": summary}, 0, nil, "planner")
		if err != nil {
			t.Fatalf("AddIntent(%s): %v", summary, err)
		}
		if _, err := conn.ExecContext(ctx, `UPDATE exploration_nodes SET state=$1, blocked_reason=NULLIF($2,''), updated_at=now() - make_interval(mins => $3) WHERE id=$4`,
			state, reason, ageMinutes, id); err != nil {
			t.Fatalf("seed state: %v", err)
		}
		return id
	}

	oldRL := add("老限流意图", "blocked", IntentBlockedRateLimited, 10)    // 应复位
	freshRL := add("新限流意图", "blocked", IntentBlockedRateLimited, 1)   // 未到年龄，不动
	oldQuota := add("老额度意图", "blocked", IntentBlockedLLMQuota, 10)    // 其他标签，不动
	oldOpen := add("老 open 意图", "open", IntentBlockedRateLimited, 10) // 非 blocked，不动

	n, err := es.ReopenRateLimitedIntents(5 * time.Minute)
	if err != nil {
		t.Fatalf("ReopenRateLimitedIntents: %v", err)
	}
	if n != 1 {
		t.Errorf("reopened = %d, want 1", n)
	}

	stateOf := func(id int64) (state, reason string) {
		t.Helper()
		var r *string
		if err := d.QueryRow(`SELECT state, blocked_reason FROM exploration_nodes WHERE id=$1`, id).Scan(&state, &r); err != nil {
			t.Fatalf("query state: %v", err)
		}
		if r != nil {
			reason = *r
		}
		return
	}

	if state, reason := stateOf(oldRL); state != "open" || reason != "" {
		t.Errorf("old rate-limited: state=%s reason=%q, want open/空", state, reason)
	}
	if state, _ := stateOf(freshRL); state != "blocked" {
		t.Errorf("fresh rate-limited: state=%s, want blocked", state)
	}
	if state, _ := stateOf(oldQuota); state != "blocked" {
		t.Errorf("quota-blocked: state=%s, want blocked（不归本修法管）", state)
	}
	if state, _ := stateOf(oldOpen); state != "open" {
		t.Errorf("already open: state=%s, want open", state)
	}
}

package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestReportFindingEvidenceChain 覆盖 v2.9 §5.5 C2/C3 的三条行为：
// 结构化证据链落 payload、无任何证据软拒、旧格式（纯 poc）兼容。
func TestReportFindingEvidenceChain(t *testing.T) {
	d := testDB(t)
	t.Cleanup(func() { d.Close() })
	expID, err := d.CreateExploration("report_finding test", "evidence chain")
	if err != nil {
		t.Fatalf("CreateExploration: %v", err)
	}
	store := d.Exploration(expID)
	nid, err := store.AddIntent(map[string]any{"summary": "chain intent"}, 0, nil, "planner")
	if err != nil {
		t.Fatalf("AddIntent: %v", err)
	}
	ts := NewToolSet(store, "work#1")

	call := func(payload map[string]any) string {
		t.Helper()
		raw, _ := json.Marshal(payload)
		res, err := ts.addFinding().Call(context.Background(), raw, nil)
		if err != nil {
			t.Fatalf("Call error: %v", err)
		}
		return res.Flatten()
	}
	lastFindingPayload := func() map[string]any {
		t.Helper()
		var raw []byte
		if err := d.QueryRow(`SELECT payload FROM exploration_nodes
WHERE exploration_id=$1 AND kind='finding' ORDER BY id DESC LIMIT 1`, expID).Scan(&raw); err != nil {
			t.Fatalf("query finding payload: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		return m
	}

	// 1) 结构化证据链：payload 应含 evidence.chain 四段。
	out := call(map[string]any{
		"vulnclass": "SQL Injection", "severity": "high", "summary": "注入确认", "intent_id": nid,
		"evidence_chain": map[string]any{
			"request": "GET /?q=1", "response": "SQL syntax error",
			"steps": "重放同一 GET", "conclusion": "报错注入可触发",
		},
	})
	if strings.Contains(out, "需要证据自证") {
		t.Fatalf("带证据链不应被拒: %s", out)
	}
	ev, _ := lastFindingPayload()["evidence"].(map[string]any)
	chain, _ := ev["chain"].(map[string]any)
	if chain == nil || chain["request"] != "GET /?q=1" || chain["conclusion"] != "报错注入可触发" {
		t.Fatalf("evidence.chain 缺失或字段不符: %v", ev)
	}

	// 2) 无任何证据 → 软拒（带理由）。
	out = call(map[string]any{"vulnclass": "XSS", "severity": "low", "summary": "无证据"})
	if !strings.Contains(out, "需要证据自证") {
		t.Fatalf("无证据的申报应被软拒，got: %s", out)
	}

	// 3) 旧格式纯 poc → 兼容接受，payload 无 chain。
	out = call(map[string]any{"vulnclass": "IDOR", "severity": "medium", "summary": "旧格式", "evidence": "curl ... 返回他人订单"})
	if strings.Contains(out, "需要证据自证") {
		t.Fatalf("旧格式 poc 不应被拒: %s", out)
	}
	ev, _ = lastFindingPayload()["evidence"].(map[string]any)
	if _, hasChain := ev["chain"]; hasChain {
		t.Fatalf("旧格式不应写入 chain: %v", ev)
	}
	if ev["poc"] != "curl ... 返回他人订单" {
		t.Fatalf("poc 字段丢失: %v", ev)
	}
}

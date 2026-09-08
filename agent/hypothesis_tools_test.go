package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
)

// TestHypothesisToolsLifecycle 端到端覆盖第二 brain(6b)的两把工具 +意图锚点放宽:
//  1. record_hypothesis 只接受 fact/finding 作父锚(拒 goal/intent),建 open 假设并连血缘;
//  2. addOneIntent 现在接受 hypothesis 作意图锚点(planner 据假设派验证意图);
//  3. resolve_hypothesis 校验目标是假设、confirmed 须给 evidence,并翻转 state。
func TestHypothesisToolsLifecycle(t *testing.T) {
	d := testDB(t)
	defer d.Close()

	task, err := d.CreateTask("hyp-tools", "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })
	es := d.Exploration(task.ExplorationID)

	// 一条已确认事实做父锚;一个 goal 做"非法锚点"的反例。
	factID, err := es.AddNode(db.KindFact, map[string]any{"summary": "内网 IP 10.0.0.5 泄露"}, 5, "confirmed", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	goalID, err := es.AddNode(db.KindGoal, map[string]any{"text": "拿下内网"}, 5, "open", "system", nil)
	if err != nil {
		t.Fatal(err)
	}

	tools := NewToolSet(es, "hypothesizer")
	tools.SetTaskID(task.ID)
	rec := tools.recordHypothesis()

	// record_hypothesis: goal 作父锚 → 拒绝。
	res, err := rec.Call(context.Background(), json.RawMessage(fmt.Sprintf(`{"summary":"h1","parent_ids":[%d]}`, goalID)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Flatten(), "不能作为假设锚点") {
		t.Fatalf("goal 作假设父锚应被拒, got=%q", res.Flatten())
	}

	// record_hypothesis: fact 作父锚 → 成功,建 open 假设 + fact--derived_from-->hyp。
	res, err = rec.Call(context.Background(), json.RawMessage(fmt.Sprintf(`{"summary":"IP 与回连地址一致,疑同主机","parent_ids":[%d]}`, factID)), nil)
	if err != nil {
		t.Fatal(err)
	}
	var hypID int64
	if _, e := fmt.Sscanf(res.Flatten(), "hypothesis recorded: %d", &hypID); e != nil || hypID == 0 {
		t.Fatalf("record_hypothesis 应返回 id, got=%q", res.Flatten())
	}
	if n, _ := es.GetNode(hypID); n == nil || n.Kind != db.KindHypothesis || n.State != "open" {
		t.Fatalf("假设节点应为 open hypothesis, got=%+v", n)
	}

	// addOneIntent: 用 hypothesis 作锚点应被接受(6b 放宽的白名单)。
	raw := func(id int64) []json.RawMessage { b, _ := json.Marshal(id); return []json.RawMessage{b} }
	iid, dup, err := tools.addOneIntent(intentItem{Summary: "验证两服务是否同主机", ParentIDs: raw(hypID)})
	if err != nil || dup || iid == 0 {
		t.Fatalf("hypothesis 作意图锚点应成功: id=%d dup=%v err=%v", iid, dup, err)
	}

	resolve := tools.resolveHypothesis()

	// resolve: confirmed 但缺 evidence → 拒。
	res, _ = resolve.Call(context.Background(), json.RawMessage(fmt.Sprintf(`{"hypothesis_id":%d,"state":"confirmed"}`, hypID)), nil)
	if !strings.Contains(res.Flatten(), "evidence_id") {
		t.Fatalf("confirmed 缺 evidence 应被拒, got=%q", res.Flatten())
	}

	// resolve: 目标是 fact 而非 hypothesis → 拒。
	res, _ = resolve.Call(context.Background(), json.RawMessage(fmt.Sprintf(`{"hypothesis_id":%d,"state":"refuted"}`, factID)), nil)
	if !strings.Contains(res.Flatten(), "假设(hypothesis)节点") {
		t.Fatalf("非 hypothesis 目标应被拒, got=%q", res.Flatten())
	}

	// resolve: confirmed + 合法 evidence(fact) → 翻转 state=confirmed。
	res, _ = resolve.Call(context.Background(), json.RawMessage(fmt.Sprintf(`{"hypothesis_id":%d,"state":"confirmed","evidence_id":%d}`, hypID, factID)), nil)
	if !strings.Contains(res.Flatten(), "marked confirmed") {
		t.Fatalf("合法 confirmed 应成功, got=%q", res.Flatten())
	}
	if n, _ := es.GetNode(hypID); n == nil || n.State != "confirmed" {
		t.Fatalf("假设 state 应翻为 confirmed, got=%+v", n)
	}
}

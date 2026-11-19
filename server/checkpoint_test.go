package server

// serialCheckpoint（v2.8 checkpoint 映射）单测：真 PG，测 helper 三条路径——
// 非 serial 不 checkpoint、serial 未达上限 checkpoint 并计数、达上限落终态。

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/wkend/lodestar/db"
)

func TestSerialCheckpoint(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v)", err)
	}
	defer m.Close()
	s := New(testCtx(t), m, t.TempDir(), t.TempDir(), t.TempDir())

	task, err := m.CreateTaskWithOptions("checkpoint 测试", "g", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// 建一条 serial 意图 + 一条普通意图。
	serialID, err := task.Store.AddIntent(map[string]any{"summary": "链步骤", "serial": true}, 8, nil, "planner")
	if err != nil {
		t.Fatal(err)
	}
	plainID, err := task.Store.AddIntent(map[string]any{"summary": "普通意图"}, 5, nil, "planner")
	if err != nil {
		t.Fatal(err)
	}
	serialNode, err := task.Store.GetNode(serialID)
	if err != nil || serialNode == nil {
		t.Fatalf("取 serial 意图失败: %v", err)
	}
	plainNode, err := task.Store.GetNode(plainID)
	if err != nil || plainNode == nil {
		t.Fatalf("取普通意图失败: %v", err)
	}

	// 路径 1：非 serial → false。
	if s.engine.serialCheckpoint(task, plainNode, "work#1") {
		t.Fatal("非 serial 意图不应 checkpoint")
	}

	// 路径 2：serial 未达上限 → true，checkpoint 活动落表。
	for i := 1; i <= serialCheckpointMax; i++ {
		if !s.engine.serialCheckpoint(task, serialNode, "work#1") {
			t.Fatalf("第 %d 次 checkpoint 应返回 true（上限 %d）", i, serialCheckpointMax)
		}
		// 状态必须是 open 语义的前提：验证计数确实在涨（活动流里有 i 条 checkpoint）。
		nid := serialID
		acts, _, err := task.Store.ActivityList(&nid, 0, 10000)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, a := range acts {
			if a.Kind == "checkpoint" {
				n++
			}
		}
		if n != i {
			t.Fatalf("checkpoint 计数应为 %d, got %d", i, n)
		}
	}

	// 路径 3：达上限 → false（钱包耗尽，调用方落 exhausted）。
	if s.engine.serialCheckpoint(task, serialNode, "work#1") {
		t.Fatalf("超上限（%d）后不应再 checkpoint", serialCheckpointMax)
	}

	// 路径 4：坏 payload → false（降级安全方向）。
	badNode := &db.Node{ID: serialID, Kind: db.KindIntent, Payload: json.RawMessage(`{不是json`)}
	if s.engine.serialCheckpoint(task, badNode, "work#1") {
		t.Fatal("坏 payload 应降级 false")
	}
	// 路径 5：nil 意图 → false。
	if s.engine.serialCheckpoint(task, nil, "work#1") {
		t.Fatal("nil 意图应 false")
	}
}

// testCtx 返回一个已取消的 context（后台协程不干扰测试断言——同 task_archives 模式）。
func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

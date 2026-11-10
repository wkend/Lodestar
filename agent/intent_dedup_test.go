package agent

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/wkend/lodestar/db"
)

// intentFingerprint 是 stage 4 去重的核心：字面归一化(大小写/语序/标点/去重词) +
// asset_ids。这些用例锁定它该拦什么、不该拦什么。
func TestIntentFingerprint(t *testing.T) {
	eq := func(a, b string, aIDs, bIDs []int64) bool {
		return intentFingerprint(a, aIDs) == intentFingerprint(b, bIDs)
	}

	// 大小写 + 多余空格 + 尾标点 → 同一指纹(纯换写法)
	if !eq("Test Login SQLi", "test  login sqli.", nil, nil) {
		t.Error("大小写/空格/标点差异应视为重复")
	}
	// 语序无关(词集排序)
	if !eq("sqli on login", "login on sqli", nil, nil) {
		t.Error("语序不同应视为重复")
	}
	// 同一句话打不同资产 → 不同指纹(不去重)
	if eq("test login", "test login", []int64{1}, []int64{2}) {
		t.Error("不同 asset_ids 不应视为重复")
	}
	// 同一句话打同一组资产(顺序不同) → 同一指纹
	if !eq("test login", "test login", []int64{2, 1}, []int64{1, 2}) {
		t.Error("相同 asset_ids(乱序)应视为重复")
	}
	// 同义改写 → 拦不住(设计如此,属语义层)
	if eq("测试登录接口SQL注入", "对登录点做SQLi", nil, nil) {
		t.Error("同义改写本就不该被字面指纹拦住(此断言若失败说明指纹意外命中)")
	}
	// 完全相同的中文句 → 同一指纹
	if !eq("枚举 /api 端点", "枚举 /api 端点", nil, nil) {
		t.Error("完全相同的中文句应视为重复")
	}
}

// TestAddOneIntentDedup 端到端覆盖 findDuplicateIntent 的两边比对：这正是纯函数
// 测试盖不到、且 n.Anchors bug 藏身的地方。验证(a)带 asset_ids 的等价方向去重，
// (b)同措辞但不同 asset_ids 不去重。
func TestAddOneIntentDedup(t *testing.T) {
	d := testDB(t)
	defer d.Close()

	task, err := d.CreateTask("dedup", "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })

	assets := d.Assets()
	a1, err := assets.UpsertRootDomain(db.UpsertRootDomainReq{Domain: fmt.Sprintf("dedup-%d.invalid", time.Now().UnixNano())})
	if err != nil {
		t.Fatal(err)
	}
	a2, err := assets.UpsertRootDomain(db.UpsertRootDomainReq{Domain: fmt.Sprintf("dedup-%d.invalid", time.Now().UnixNano()+1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = assets.DeleteByIDs([]int64{a1, a2}) })

	tools := NewToolSet(d.Exploration(task.ExplorationID), "planner")
	tools.SetTaskID(task.ID)
	tools.SetAssetStore(assets, assets.Companies())

	raw := func(id int64) []json.RawMessage { b, _ := json.Marshal(id); return []json.RawMessage{b} }

	// 首建带 asset_ids 的意图。
	id1, dup1, err := tools.addOneIntent(intentItem{Summary: "test login SQLi", AssetIDs: raw(a1)})
	if err != nil || dup1 || id1 == 0 {
		t.Fatalf("首建应成功且非重复: id=%d dup=%v err=%v", id1, dup1, err)
	}
	// 换措辞/语序/大小写 + 相同 asset_ids → 命中去重(带资产路径,正是 bug 破坏的场景)。
	id2, dup2, err := tools.addOneIntent(intentItem{Summary: "sqli LOGIN test.", AssetIDs: raw(a1)})
	if err != nil || !dup2 || id2 != id1 {
		t.Fatalf("等价方向+同资产应去重到 #%d: id=%d dup=%v err=%v", id1, id2, dup2, err)
	}
	// 同措辞但不同 asset_ids → 不去重,新建独立意图。
	id3, dup3, err := tools.addOneIntent(intentItem{Summary: "test login SQLi", AssetIDs: raw(a2)})
	if err != nil || dup3 || id3 == id1 {
		t.Fatalf("不同资产不应去重: id=%d dup=%v err=%v", id3, dup3, err)
	}
}

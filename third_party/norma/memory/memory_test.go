package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Autumn-27/norma/tool"
)

func TestSaveScanReadRelevant(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.Save(Memory{Name: "User Role", Description: "the user's job", Type: TypeUser, Content: "The user is a Go backend engineer."}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := s.Save(Memory{Name: "Test Policy", Description: "how to test", Type: TypeFeedback, Content: "Always run go test before claiming done."}); err != nil {
		t.Fatalf("save: %v", err)
	}

	mems, _ := s.Scan()
	if len(mems) != 2 {
		t.Fatalf("scan=%d", len(mems))
	}

	// Frontmatter round-trips.
	m, err := s.Read("User Role")
	if err != nil || m.Type != TypeUser || !strings.Contains(m.Content, "backend engineer") {
		t.Fatalf("read: %+v err=%v", m, err)
	}

	// Index lists both with a slug link.
	idx := s.Index()
	if !strings.Contains(idx, "[User Role](user-role.md)") || !strings.Contains(idx, "Test Policy") {
		t.Fatalf("index:\n%s", idx)
	}

	// Relevance picks the role memory for a role query.
	rel := s.Relevant("what is the user's role and job", 5)
	if len(rel) == 0 || rel[0].Name != "User Role" {
		t.Fatalf("relevant: %+v", rel)
	}

	// On-disk file has YAML frontmatter.
	data, _ := os.ReadFile(filepath.Join(s.Dir, "user-role.md"))
	if !strings.HasPrefix(string(data), "---\nname: User Role") {
		t.Fatalf("frontmatter:\n%s", data)
	}
}

func TestMemoryTools(t *testing.T) {
	s := NewStore(t.TempDir())
	rec, recall := RecordTool(s), RecallTool(s)

	res, _ := rec.Call(context.Background(), []byte(`{"name":"Deploy","description":"how to deploy","type":"project","content":"Deploy via make release."}`), &tool.ToolContext{})
	if res.IsError {
		t.Fatalf("record: %q", res.Flatten())
	}
	res, _ = recall.Call(context.Background(), []byte(`{"query":"how do I deploy"}`), &tool.ToolContext{})
	if !strings.Contains(res.Flatten(), "make release") {
		t.Fatalf("recall: %q", res.Flatten())
	}
}

func TestTokenizeCJK(t *testing.T) {
	toks := tokenize("后台弱口令 admin bypass")
	want := []string{"admin", "bypass", "后台", "台弱", "弱口", "口令"}
	if strings.Join(toks, ",") != strings.Join(want, ",") {
		t.Fatalf("tokenize=%v want=%v", toks, want)
	}
	if got := tokenize("文件上传绕过"); len(got) == 0 {
		t.Fatalf("pure-CJK query tokenized to nothing")
	}
}

func TestSlugKeepsCJK(t *testing.T) {
	a, b := slug("后台弱口令手法"), slug("文件上传绕过")
	if a == "" || b == "" || a == b {
		t.Fatalf("slug collapsed CJK names: %q vs %q", a, b)
	}
	if a != "后台弱口令手法" {
		t.Fatalf("slug(%q)=%q", "后台弱口令手法", a)
	}
}

func TestSaveRecallCJK(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.Save(Memory{Name: "后台弱口令打法", Description: "怎么打后台弱口令", Type: TypeProject, Content: "对管理后台先试 admin/admin123，再试目标域名前缀作密码。"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := s.Save(Memory{Name: "文件上传绕过", Description: "怎么绕过上传限制", Type: TypeProject, Content: "黑名单改双后缀 x.php.png 绕过。"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Two distinct files — the old ASCII-only slug collapsed both into ".md".
	mems, _ := s.Scan()
	if len(mems) != 2 {
		t.Fatalf("scan=%d (names collided?)", len(mems))
	}
	// A Chinese query reaches the matching Chinese memory.
	rel := s.Relevant("后台弱口令怎么打", 5)
	if len(rel) == 0 || rel[0].Name != "后台弱口令打法" {
		t.Fatalf("relevant: %+v", rel)
	}
	// And the recall tool round-trips through the same tokenizer.
	res, _ := RecallTool(s).Call(context.Background(), []byte(`{"query":"文件上传绕过"}`), &tool.ToolContext{})
	if !strings.Contains(res.Flatten(), "黑名单") {
		t.Fatalf("recall CJK: %q", res.Flatten())
	}
}

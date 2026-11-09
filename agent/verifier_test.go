package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Pure (no database) checks for the verifier role: the verdict tool's argument
// contract and the code-owned prompt boundary.

func TestRecordVerificationRejectsFalseWithoutIssues(t *testing.T) {
	tools := NewToolSet(nil, "verifier")
	rec := tools.recordVerification(42)
	res, err := rec.Call(context.Background(), json.RawMessage(`{"verified":false,"issues":""}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Flatten(), "必须在 issues 中写明") {
		t.Fatalf("verified=false without issues must be rejected, got %q", res.Flatten())
	}
}

func TestRecordVerificationNeedsTaskContext(t *testing.T) {
	tools := NewToolSet(nil, "verifier")
	rec := tools.recordVerification(42)
	res, err := rec.Call(context.Background(), json.RawMessage(`{"verified":true}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Flatten(), "需要任务上下文") {
		t.Fatalf("verdict without a task store must not silently succeed, got %q", res.Flatten())
	}
}

func TestVerifierSystemCarriesBoundaryAndGoal(t *testing.T) {
	got := verifierSystem("拿下X", "/work")
	for _, want := range []string{"复核者", "拿下X", "不可信数据边界", "中间产物输出规约"} {
		if !strings.Contains(got, want) {
			t.Fatalf("verifier system prompt missing %q", want)
		}
	}
}

package server

import (
	"context"
	"errors"
	"iter"
	"testing"
	"time"

	"github.com/Autumn-27/norma/llm"
)

// retryProvider 单元测试：验证者用的同 provider 退避重试层（task_llm.go 的
// sameProviderStreamRetries 语义，无 profile 链版本）。provider 是纯接口，无需 PG。

// fakeRetryProv 记录 Stream/Complete 的尝试次数，前 fails 次返回 retryable 的 429，
// 之后返回正常事件。
type fakeRetryProv struct {
	llm.Provider  // 满足接口其余部分（不会被调到）
	fails         int
	streamCalls   int
	completeCalls int
}

var err429 = errors.New("anthropic: status 429: {\"type\":\"rate_limit_error\"}")

func (f *fakeRetryProv) streamEvents() []llm.StreamEvent {
	return []llm.StreamEvent{
		{Type: llm.SEMessageStart, Usage: llm.Usage{InputTokens: 10}}, // 不提交输出
		{Type: llm.SETextDelta, Text: "ok"},                           // 提交输出
		{Type: llm.SEMessageStop},                                     // 提交输出
	}
}

func (f *fakeRetryProv) Stream(ctx context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	f.streamCalls++
	calls := f.streamCalls
	return func(yield func(llm.StreamEvent, error) bool) {
		if calls <= f.fails {
			yield(llm.StreamEvent{}, err429) // 建连即失败：零输出已交付
			return
		}
		for _, ev := range f.streamEvents() {
			if !yield(ev, nil) {
				return
			}
		}
	}
}

func (f *fakeRetryProv) Complete(ctx context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	f.completeCalls++
	if f.completeCalls <= f.fails {
		return llm.Message{}, "", llm.Usage{}, err429
	}
	return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{llm.TextBlock("done")}}, "end_turn", llm.Usage{InputTokens: 10, OutputTokens: 2}, nil
}

func collectStream(ctx context.Context, p retryProvider) (events []llm.StreamEvent, err error) {
	for ev, e := range p.Stream(ctx, llm.CompletionRequest{}) {
		if e != nil {
			return events, e
		}
		events = append(events, ev)
	}
	return events, nil
}

func TestRetryProviderStreamRetriesTransient429(t *testing.T) {
	origBackoff := sameProviderRetryBackoff
	sameProviderRetryBackoff = func(int) (d time.Duration) { return 0 }
	defer func() { sameProviderRetryBackoff = origBackoff }()

	f := &fakeRetryProv{fails: 2} // 前 2 次 429，第 3 次成功（重试上限=2 → 恰好成功）
	events, err := collectStream(context.Background(), retryProvider{inner: f, label: "test"})
	if err != nil {
		t.Fatalf("重试后应成功,得到: %v", err)
	}
	if f.streamCalls != 3 {
		t.Fatalf("尝试次数 = %d, want 3", f.streamCalls)
	}
	if len(events) != 3 {
		t.Fatalf("交付事件数 = %d, want 3 (start/text/stop)", len(events))
	}
}

func TestRetryProviderStreamRetriesExhausted(t *testing.T) {
	origBackoff := sameProviderRetryBackoff
	sameProviderRetryBackoff = func(int) (d time.Duration) { return 0 }
	defer func() { sameProviderRetryBackoff = origBackoff }()

	f := &fakeRetryProv{fails: 99} // 永远 429
	_, err := collectStream(context.Background(), retryProvider{inner: f, label: "test"})
	if !errors.Is(err, err429) {
		t.Fatalf("重试耗尽后应透传原错误,得到: %v", err)
	}
	if f.streamCalls != sameProviderStreamRetries+1 {
		t.Fatalf("尝试次数 = %d, want %d", f.streamCalls, sameProviderStreamRetries+1)
	}
}

func TestRetryProviderStreamNoRetryAfterCommit(t *testing.T) {
	origBackoff := sameProviderRetryBackoff
	sameProviderRetryBackoff = func(int) (d time.Duration) { return 0 }
	defer func() { sameProviderRetryBackoff = origBackoff }()

	// 提交输出后断流：重放会重复模型输出，不允许重试。
	fake := &commitThenFailProv{}
	events, err := collectStream(context.Background(), retryProvider{inner: fake, label: "test"})
	if err == nil {
		t.Fatal("提交后断流应把错误透传给调用方")
	}
	if fake.calls != 1 {
		t.Fatalf("提交后不得重试,尝试次数 = %d, want 1", fake.calls)
	}
	// 已交付的输出不能丢：text_delta 必须已交给调用方。
	sawText := false
	for _, ev := range events {
		if ev.Type == llm.SETextDelta && ev.Text == "partial" {
			sawText = true
		}
	}
	if !sawText {
		t.Fatal("提交过的输出在失败路径上也必须交付")
	}
}

// commitThenFailProv 第一次（也是唯一一次）调用交付一个 text_delta 后断流。
type commitThenFailProv struct {
	llm.Provider
	calls int
}

func (f *commitThenFailProv) Stream(ctx context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	f.calls++
	return func(yield func(llm.StreamEvent, error) bool) {
		if !yield(llm.StreamEvent{Type: llm.SETextDelta, Text: "partial"}, nil) {
			return
		}
		yield(llm.StreamEvent{}, errors.New("anthropic: status 429: mid-stream"))
	}
}

func TestRetryProviderCompleteRetries(t *testing.T) {
	origBackoff := sameProviderRetryBackoff
	sameProviderRetryBackoff = func(int) (d time.Duration) { return 0 }
	defer func() { sameProviderRetryBackoff = origBackoff }()

	f := &fakeRetryProv{fails: 1}
	msg, _, _, err := retryProvider{inner: f, label: "test"}.Complete(context.Background(), llm.CompletionRequest{})
	if err != nil {
		t.Fatalf("重试后应成功: %v", err)
	}
	if f.completeCalls != 2 {
		t.Fatalf("尝试次数 = %d, want 2", f.completeCalls)
	}
	if len(msg.Content) != 1 {
		t.Fatalf("消息块数 = %d, want 1", len(msg.Content))
	}
}

func TestRetryProviderStreamDeterministicErrorNoRetry(t *testing.T) {
	origBackoff := sameProviderRetryBackoff
	sameProviderRetryBackoff = func(int) (d time.Duration) { return 0 }
	defer func() { sameProviderRetryBackoff = origBackoff }()

	fake := &deterministicFailProv{}
	_, err := collectStream(context.Background(), retryProvider{inner: fake, label: "test"})
	if err == nil {
		t.Fatal("400 应透传")
	}
	if fake.calls != 1 {
		t.Fatalf("确定性 4xx 不得重试,尝试次数 = %d, want 1", fake.calls)
	}
}

type deterministicFailProv struct {
	llm.Provider
	calls int
}

func (f *deterministicFailProv) Stream(ctx context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	f.calls++
	return func(yield func(llm.StreamEvent, error) bool) {
		yield(llm.StreamEvent{}, errors.New("anthropic: status 401: unauthorized"))
	}
}

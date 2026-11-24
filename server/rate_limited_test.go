package server

import (
	"errors"
	"testing"
	"time"
)

// TestIsRateLimitError 覆盖限流分类判据（发现 13 回归）：
// 1302/瞬时限流 → true；1308/1310 额度耗尽（同样带 rate_limit_error 类型）→ false。
func TestIsRateLimitError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"1302 原文", errors.New(`anthropic: status 429: {"type":"error","error":{"type":"rate_limit_error","code":"1302","message":"[1302][您的账户已达到速率限制，请您控制请求频率]"}}`), true},
		{"1308 额度耗尽不是瞬时限流", errors.New(`429: {"type":"error","error":{"type":"rate_limit_error","code":"1308","message":"[1308][您已达到每周/每月使用上限]"}}`), false},
		{"1310 同上", errors.New(`429: {"error":{"code":"1310","message":"[1310][限额重置]"}}`), false},
		{"泛化 429 rate_limit", errors.New("status 429: rate_limit exceeded"), true},
		{"网络错误", errors.New("dial tcp: i/o timeout"), false},
		{"上下文超长", errors.New("context_length exceeded"), false},
	}
	for _, c := range cases {
		if got := isRateLimitError(c.err); got != c.want {
			t.Errorf("%s: isRateLimitError = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestRetryBackoffForRateLimitFloor 限流类退避下限 2s（+≤500ms 抖动）；
// 非限流类维持原指数退避；原退避已超下限时不改写。
func TestRetryBackoffForRateLimitFloor(t *testing.T) {
	rlErr := errors.New(`status 429: {"code":"1302"}`)
	b := retryBackoffFor(0, rlErr)
	if b < rateLimitRetryFloor || b >= rateLimitRetryFloor+500*time.Millisecond {
		t.Errorf("rate-limit attempt0 backoff = %v, want [2s, 2.5s)", b)
	}
	if got := retryBackoffFor(0, errors.New("i/o timeout")); got != 500*time.Millisecond {
		t.Errorf("non-rate-limit attempt0 backoff = %v, want 500ms", got)
	}
	if got := retryBackoffFor(3, rlErr); got != 4*time.Second {
		t.Errorf("rate-limit attempt3 backoff = %v, want 4s (原退避已超下限)", got)
	}
}

package server

import (
	"errors"
	"testing"
)

// TestIsRetryableAccountLimitShortCircuit 验证账号级使用限额（GLM code=1308）不重试
// （判读报告发现 11：2026-11-17 断粮轮 55 次 error 全为此条，重试全部白烧）。
func TestIsRetryableAccountLimitShortCircuit(t *testing.T) {
	notRetry := []string{
		// 第四轮实测原文（截取）
		`anthropic: status 429: {"type":"error","error":{"type":"rate_limit_error","code":"1308","message":"[1308][已达到 5 小时的使用上限。您的限额将在 2026-11-17 18:11:56 重置。]"}}`,
		`rate_limit_error code=1308 已达到 5 小时的使用上限`,
		`[1308][已达到 5 小时的使用上限。您的限额将在 2026-11-18 00:00:00 重置。]`,
	}
	retry := []string{
		`status 429 too many requests`,                    // 普通限流（重试）
		`anthropic: status 529 overloaded_error`,          // 供应商过载（重试）
		`transport reset by peer`,                        // 传输（重试）
		`rate_limit_error code=1309 别的码`,               // 同提供商但非 1308（重试）
	}
	for _, msg := range notRetry {
		if isRetryableStreamError(errors.New(msg)) {
			t.Errorf("账号限额应短路不重试: %.60s", msg)
		}
	}
	for _, msg := range retry {
		if !isRetryableStreamError(errors.New(msg)) {
			t.Errorf("普通瞬时错误应重试: %.60s", msg)
		}
	}
}

package server

import (
	"context"
	"iter"
	"log"

	"github.com/Autumn-27/norma/llm"
)

// retryProvider reuses the worker path's pre-commit retry window (sameProviderStreamRetries /
// isRetryableStreamError / sameProviderRetryBackoff, see task_llm.go) for single-provider callers
// that have no task profile chain — currently the verifier (stage 7b). Transient provider
// failures (transport reset / 429 / 5xx / in-stream overloaded) are replayed on the same provider
// with backoff, bounded by sameProviderStreamRetries; before any output is committed, such a
// replay cannot duplicate model output or tool execution. Deterministic (4xx except 429/408,
// context-length) and quota errors pass through unchanged: there is no fallback chain here to
// hand them to.
type retryProvider struct {
	inner llm.Provider
	label string
}

func (p retryProvider) Complete(ctx context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	var (
		msg   llm.Message
		sr    string
		usage llm.Usage
		err   error
	)
	for attempt := 0; ; attempt++ {
		msg, sr, usage, err = p.inner.Complete(ctx, req)
		if err != nil && ctx.Err() == nil &&
			attempt < sameProviderStreamRetries && isRetryableStreamError(err) {
			backoff := sameProviderRetryBackoff(attempt)
			log.Printf("[provider-retry] %s 非流式调用失败,%v 后同 provider 重试 (%d/%d): %v",
				p.label, backoff, attempt+1, sameProviderStreamRetries, err)
			if sleepCtx(ctx, backoff) {
				return msg, sr, usage, err // 退避期间 ctx 取消 → 停止重试,透传最后一次错误
			}
			continue
		}
		return msg, sr, usage, err
	}
}

func (p retryProvider) Stream(ctx context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		var pending []llm.StreamEvent
		var streamErr error
		for attempt := 0; ; attempt++ {
			committed := false
			pending = nil
			streamErr = nil
			for event, err := range p.inner.Stream(ctx, req) {
				if err != nil {
					streamErr = err
					break
				}
				if !committed && !streamEventCommitsOutput(event) {
					pending = append(pending, event)
					continue
				}
				if !committed {
					for _, buffered := range pending {
						if !yield(buffered, nil) {
							return
						}
					}
					pending = nil
					committed = true
				}
				if !yield(event, nil) {
					return
				}
			}
			if streamErr != nil && !committed && ctx.Err() == nil &&
				attempt < sameProviderStreamRetries && isRetryableStreamError(streamErr) {
				backoff := sameProviderRetryBackoff(attempt)
				log.Printf("[provider-retry] %s 提交前流失败,%v 后同 provider 重试 (%d/%d): %v",
					p.label, backoff, attempt+1, sameProviderStreamRetries, streamErr)
				if sleepCtx(ctx, backoff) {
					break // 退避期间 ctx 取消 → 停止重试
				}
				continue
			}
			break
		}
		if streamErr == nil {
			for _, buffered := range pending {
				if !yield(buffered, nil) {
					return
				}
			}
			return
		}
		yield(llm.StreamEvent{}, streamErr)
	}
}

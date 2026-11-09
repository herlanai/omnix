// S15 retry tests cover three overload failures with three retry events before success, immediate
// propagation of non-retryable refusal, and exhaustion passed to the M34 request-error waterfall.
package tests

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/herlanai/omnix/pkg/llm"
	"github.com/herlanai/omnix/pkg/session"
)

// flakyAdapter returns overload for failTimes calls, then succeeds.
type flakyAdapter struct {
	mu        sync.Mutex
	failTimes int
	calls     int
	failKind  llm.LlmFailureKind
}

func (f *flakyAdapter) Name() string { return "flaky" }

func (f *flakyAdapter) Chat(ctx context.Context, req llm.ChatRequest, cb func(llm.StreamChunk)) (llm.Usage, error) {
	f.mu.Lock()
	f.calls++
	r := f.calls
	f.mu.Unlock()
	if r <= f.failTimes {
		return llm.Usage{}, &llm.LlmFailure{Kind: f.failKind, Message: "overload"}
	}
	return llm.Usage{PromptTokens: 1, CompletionTokens: 1}, nil
}

// retryEventSink collects llm/retry events.
type retryEventSink struct {
	mu     sync.Mutex
	events []session.LLMRetryData
}

func (s *retryEventSink) write(d session.LLMRetryData) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, d)
}

func (s *retryEventSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.events)
}

// fastCfg uses fast deterministic backoff without jitter.
func fastCfg(maxAttempts int) llm.RetryConfig {
	return llm.RetryConfig{
		MaxAttempts: maxAttempts,
		BaseBackoff: 1 * time.Millisecond,
		MaxBackoff:  5 * time.Millisecond,
		Jitter:      0,
	}
}

// TestLLMRetryWritesEventsAndSucceeds records three retry events before succeeding on call four.
func TestLLMRetryWritesEventsAndSucceeds(t *testing.T) {
	inner := &flakyAdapter{failTimes: 3, failKind: llm.FailOverload}
	sink := &retryEventSink{}
	mw := llm.NewRetryMiddleware(inner, fastCfg(4), sink.write)

	usage, err := mw.Chat(context.Background(), llm.ChatRequest{}, func(llm.StreamChunk) {})
	if err != nil {
		t.Fatalf("第 4 次应成功, 实际 %v", err)
	}
	if usage.PromptTokens+usage.CompletionTokens <= 0 {
		t.Fatalf("应返回用量, 实际 %+v", usage)
	}
	if sink.count() != 3 {
		t.Fatalf("应写 3 条 retry 事件, 实际 %d", sink.count())
	}
	if inner.calls != 4 {
		t.Fatalf("应总共调用 4 次, 实际 %d", inner.calls)
	}
}

// TestLLMRetryNonRetryableNoRetry propagates non-retryable failures immediately.
func TestLLMRetryNonRetryableNoRetry(t *testing.T) {
	inner := &flakyAdapter{failTimes: 100, failKind: llm.FailResponseRefusal}
	sink := &retryEventSink{}
	mw := llm.NewRetryMiddleware(inner, fastCfg(5), sink.write)

	_, err := mw.Chat(context.Background(), llm.ChatRequest{}, func(llm.StreamChunk) {})
	if err == nil {
		t.Fatal("refusal 应上抛错误")
	}
	// Refusal produces one call and no retry events.
	if inner.calls != 1 {
		t.Fatalf("refusal 不应重试, 调用 %d 次", inner.calls)
	}
	if sink.count() != 0 {
		t.Fatalf("refusal 不应写 retry 事件, 实际 %d", sink.count())
	}
}

// TestLLMRetryExceedMaxReturnsError propagates exhaustion to the request-error waterfall.
func TestLLMRetryExceedMaxReturnsError(t *testing.T) {
	inner := &flakyAdapter{failTimes: 100, failKind: llm.FailOverload}
	sink := &retryEventSink{}
	mw := llm.NewRetryMiddleware(inner, fastCfg(2), sink.write) // Maximum of two attempts.

	_, err := mw.Chat(context.Background(), llm.ChatRequest{}, func(llm.StreamChunk) {})
	if err == nil {
		t.Fatal("超过 max 次应上抛错误")
	}
	if inner.calls != 2 {
		t.Fatalf("max=2 应只调用 2 次, 实际 %d", inner.calls)
	}
}

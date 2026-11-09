// This file implements S15 LLM retries, following upstream retry middleware.
// RetryMiddleware retries overload and rate-limit failures with exponential
// backoff and jitter; refusal and context overflow propagate immediately.
// Each retry records attempt, backoffMs, and error in llm/retry. Exhausting
// attempts returns the last error for the M34 request-error waterfall.
package llm

import (
	"context"
	"math"
	"math/rand"
	"time"

	"github.com/herlanai/omnix/pkg/session"
)

// RetryConfig configures retry behavior.
type RetryConfig struct {
	// MaxAttempts includes the initial call; nonpositive values default to three.
	MaxAttempts int
	// BaseBackoff is the initial backoff duration.
	BaseBackoff time.Duration
	// MaxBackoff caps the backoff duration.
	MaxBackoff time.Duration
	// Jitter is a fraction from zero to one; zero gives deterministic backoff.
	Jitter float64
}

// DefaultRetryConfig returns the default retry settings.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts: 3,
		BaseBackoff: 100 * time.Millisecond,
		MaxBackoff:  4 * time.Second,
		Jitter:      0.1,
	}
}

// RetryWriter records llm/retry events, typically through SessionLog.Append.
type RetryWriter func(data session.LLMRetryData)

// RetryMiddleware decorates an LLM adapter with retries.
type RetryMiddleware struct {
	inner LLMAdapter
	cfg   RetryConfig
	write RetryWriter
}

// NewRetryMiddleware creates a retry decorator; write can record each retry event.
func NewRetryMiddleware(inner LLMAdapter, cfg RetryConfig, write RetryWriter) *RetryMiddleware {
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 3
	}
	if cfg.BaseBackoff <= 0 {
		cfg.BaseBackoff = 100 * time.Millisecond
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 4 * time.Second
	}
	return &RetryMiddleware{inner: inner, cfg: cfg, write: write}
}

// Name forwards the wrapped adapter name.
func (m *RetryMiddleware) Name() string { return m.inner.Name() }

// Chat performs a streaming conversation with retries.
func (m *RetryMiddleware) Chat(ctx context.Context, req ChatRequest, cb func(StreamChunk)) (Usage, error) {
	var lastErr error
	for attempt := 1; attempt <= m.cfg.MaxAttempts; attempt++ {
		usage, err := m.inner.Chat(ctx, req, cb)
		if err == nil {
			return usage, nil
		}
		lastErr = err
		f := ClassifyLlmError(err)
		// Retry only overload and rate-limit failures; propagate all others.
		if f.Kind != FailOverload && f.Kind != FailRateLimit {
			return usage, err
		}
		if attempt == m.cfg.MaxAttempts {
			// At the attempt limit, return without backoff to the request-error waterfall.
			return usage, err
		}
		backoff := m.computeBackoff(attempt)
		if m.write != nil {
			m.write(session.LLMRetryData{
				Attempt:   attempt,
				BackoffMs: backoff.Milliseconds(),
				Error:     err.Error(),
			})
		}
		select {
		case <-ctx.Done():
			return usage, ctx.Err()
		case <-time.After(backoff):
		}
	}
	return Usage{}, lastErr
}

// computeBackoff calculates base * 2^(attempt-1), caps it, then applies jitter.
func (m *RetryMiddleware) computeBackoff(attempt int) time.Duration {
	base := float64(m.cfg.BaseBackoff) * math.Pow(2, float64(attempt-1))
	if base > float64(m.cfg.MaxBackoff) {
		base = float64(m.cfg.MaxBackoff)
	}
	ms := base
	if m.cfg.Jitter > 0 {
		span := m.cfg.Jitter * base
		ms = base - span + rand.Float64()*(2*span) // Apply jitter within [base-span, base+span].
	}
	if ms < 0 {
		ms = 0
	}
	return time.Duration(ms)
}

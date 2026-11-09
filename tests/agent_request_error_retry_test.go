// M34 request-error retry waterfall tests: retryable errors retry up to the limit, exhaustion
// aborts the turn, custom middleware can override actions, and non-retryable errors abort
// immediately.
package tests

import (
	"testing"

	"github.com/herlanai/omnix/pkg/agent"
	"github.com/herlanai/omnix/pkg/llm"
	"github.com/herlanai/omnix/pkg/waterfall"
)

// TestRequestErrorRetryWithinMax retries retryable errors within the configured limit.
func TestRequestErrorRetryWithinMax(t *testing.T) {
	err := &llm.LlmFailure{Kind: llm.FailOverload, Message: "overload"}
	p := agent.NewRequestErrorPayload(err, 1, 3)
	act := agent.ResolveRequestError(agent.NewRequestErrorWaterfall(), p)
	if act.Kind != agent.ActionRetry {
		t.Fatalf("第 1 次重试内应 retry, 实际 %s (%s)", act.Kind, act.Reason)
	}
}

// TestRequestErrorAbortAfterMax aborts with an error after the retry limit.
func TestRequestErrorAbortAfterMax(t *testing.T) {
	err := &llm.LlmFailure{Kind: llm.FailRateLimit, Message: "rate limited"}
	// Drive the retry loop after S15 transport retries have been exhausted.
	maxRetries := 3
	var last agent.RequestErrorAction
	for count := 0; count <= maxRetries; count++ {
		p := agent.NewRequestErrorPayload(err, count, maxRetries)
		last = agent.ResolveRequestError(agent.NewRequestErrorWaterfall(), p)
		if last.Kind == agent.ActionAbort {
			break
		}
	}
	if last.Kind != agent.ActionAbort {
		t.Fatalf("超过 max 次应 abort, 实际 %s", last.Kind)
	}
	if last.Reason == "" {
		t.Fatal("abort 应带关闭原因")
	}
}

// TestRequestErrorCustomWaterfall verifies custom action overrides through M02 short-circuiting.
func TestRequestErrorCustomWaterfall(t *testing.T) {
	err := &llm.LlmFailure{Kind: llm.FailOverload, Message: "overload"}
	// Custom middleware always aborts with an explicit reason.
	chain := agent.NewRequestErrorWaterfall(
		func(p *agent.RequestErrorPayload, next waterfall.NextFunc) error {
			a := agent.RequestErrorAction{Kind: agent.ActionAbort, Reason: "policy: never retry this route"}
			p.Action = &a
			return next() // Continue the waterfall with the action already fixed.
		},
	)
	p := agent.NewRequestErrorPayload(err, 0, 5)
	if act := agent.ResolveRequestError(chain, p); act.Kind != agent.ActionAbort {
		t.Fatalf("自定义中间件应固定 abort, 实际 %s", act.Kind)
	}
}

// TestRequestErrorNonRetryableAbort aborts non-retryable errors immediately.
func TestRequestErrorNonRetryableAbort(t *testing.T) {
	err := &llm.LlmFailure{Kind: llm.FailResponseRefusal, Message: "refused"}
	p := agent.NewRequestErrorPayload(err, 0, 10)
	act := agent.ResolveRequestError(agent.NewRequestErrorWaterfall(), p)
	if act.Kind != agent.ActionAbort {
		t.Fatalf("refusal 应直接 abort, 实际 %s", act.Kind)
	}
	if p.RetryableError {
		t.Fatal("refusal 不应标记可重试")
	}
}

// TestRequestErrorRecordEvent exercises recording request errors as session agent/error events.
func TestRequestErrorRecordEvent(t *testing.T) {
	// RecordRequestError appends agent/error using the M34 and M04 event-sourcing interfaces.
	err := &llm.LlmFailure{Kind: llm.FailOverload, Message: "overload"}
	// RecordRequestError writes through SessionLog.Append. Here only nil-log safety is checked; the
	// Agent loop supplies a real log for event recording.
	agent.RecordRequestError(nil, err) // Must not panic.
	agent.RecordRequestError(nil, nil) // Must not panic.
}

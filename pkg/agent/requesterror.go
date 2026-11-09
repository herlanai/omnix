// This file implements the agent/request-error retry waterfall (M34), following
// upstream agent-loop. Actions are retry or abort. Middleware uses the M02
// onion chain and may short-circuit; otherwise TerminalRetryDecision handles
// the error. After S15 exhausts LLM-level backoff retries and llm/retry events,
// this waterfall decides whether to retry again or abort. Exhausting the retry
// limit aborts the turn and records agent/error.
package agent

import (
	"context"

	"github.com/herlanai/omnix/pkg/llm"
	"github.com/herlanai/omnix/pkg/session"
	"github.com/herlanai/omnix/pkg/waterfall"
)

// RequestErrorActionKind identifies a request-error action.
type RequestErrorActionKind string

// The only request-error actions are retry and abort.
const (
	ActionRetry RequestErrorActionKind = "retry"
	ActionAbort RequestErrorActionKind = "abort"
)

// RequestErrorAction is the result of handling a request error.
type RequestErrorAction struct {
	// Kind identifies the action.
	Kind RequestErrorActionKind `json:"kind"`
	// Reason records the decision rationale for auditing.
	Reason string `json:"reason,omitempty"`
}

// RequestErrorPayload is shared by request-error middleware.
type RequestErrorPayload struct {
	// Err is the underlying request error.
	Err error
	// RetryCount counts retries already attempted.
	RetryCount int
	// MaxRetries limits retry attempts.
	MaxRetries int
	// RetryableError indicates overload or rate-limit errors that may be retried.
	RetryableError bool
	// Action is the final action selected by the waterfall.
	Action *RequestErrorAction
}

// ShouldRetryError uses LLM classification: only overload and rate-limit qualify.
func ShouldRetryError(err error) bool {
	if err == nil {
		return false
	}
	f := llm.ClassifyLlmError(err)
	return f.Kind == llm.FailOverload || f.Kind == llm.FailRateLimit
}

// NewRequestErrorPayload constructs the payload and computes retryability.
func NewRequestErrorPayload(err error, retryCount, maxRetries int) *RequestErrorPayload {
	return &RequestErrorPayload{
		Err:            err,
		RetryCount:     retryCount,
		MaxRetries:     maxRetries,
		RetryableError: ShouldRetryError(err),
	}
}

// TerminalRetryDecision retries eligible errors below the retry limit;
// otherwise it aborts so the turn can close with an error.
func TerminalRetryDecision(p *RequestErrorPayload) RequestErrorAction {
	if p.RetryableError && p.RetryCount < p.MaxRetries {
		return RequestErrorAction{Kind: ActionRetry, Reason: "retryable llm failure within max retries"}
	}
	if p.RetryableError {
		return RequestErrorAction{Kind: ActionAbort, Reason: "max request-error retries exceeded"}
	}
	return RequestErrorAction{Kind: ActionAbort, Reason: "non-retryable request error"}
}

// RequestErrorWaterfall handles request errors using the M02 waterfall.Chain.
// Middleware may set p.Action and stop the chain by not calling next.
type RequestErrorWaterfall = waterfall.Chain[RequestErrorPayload]

// NewRequestErrorWaterfall constructs the request-error chain.
func NewRequestErrorWaterfall(handlers ...waterfall.Handler[RequestErrorPayload]) *RequestErrorWaterfall {
	return waterfall.New(handlers...)
}

// ResolveRequestError returns the action set by middleware, or applies
// TerminalRetryDecision when the chain falls through without an action.
func ResolveRequestError(chain *RequestErrorWaterfall, p *RequestErrorPayload) RequestErrorAction {
	if chain == nil {
		return TerminalRetryDecision(p)
	}
	_ = chain.Run(context.Background(), p) // Middleware does not require a context here; fall through to the terminal decision.
	if p.Action != nil {
		return *p.Action
	}
	return TerminalRetryDecision(p)
}

// RecordRequestError writes agent/error to the session log for auditing.
// A nil log skips recording.
func RecordRequestError(log *session.SessionLog, err error) {
	if log == nil || err == nil {
		return
	}
	_, _ = log.Append(session.AgentErrorData{
		Message: err.Error(),
		Pkg:     "pkg/agent",
	})
}

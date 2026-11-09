// Cooperative timeouts mirror upstream guard/timeout-policy. Tools declare TimeoutMs and must
// honor ctx.Done(). The wrapper returns structured TOOL_TIMEOUT only when its own timer expires.
// Go cannot forcibly stop a goroutine: a tool ignoring cancellation may continue in the
// background and leak resources after the wrapper returns.
package tools

import (
	"context"
	"errors"
	"time"
)

// ToolTimeoutCode is the stable TOOL_TIMEOUT error code.
const ToolTimeoutCode = "TOOL_TIMEOUT"

// ErrToolTimeout reports that a tool exceeded its declared time budget.
var ErrToolTimeout = errors.New("tool call timed out")

// TimeoutError carries TOOL_TIMEOUT for retry and sandbox routing.
type TimeoutError struct {
	Code   string
	Budget time.Duration
	Cause  error
}

// Error implements error.
func (e *TimeoutError) Error() string {
	return "tool call timed out after " + e.Budget.String()
}

// Unwrap supports errors.Is and errors.As.
func (e *TimeoutError) Unwrap() error { return e.Cause }

// WrapTimeout applies timeoutMs to an execution function; nonpositive values leave it unchanged.
func WrapTimeout(exec ToolExecuteFunc, timeoutMs int) ToolExecuteFunc {
	if exec == nil || timeoutMs <= 0 {
		return exec
	}
	budget := time.Duration(timeoutMs) * time.Millisecond
	return func(ctx context.Context, input map[string]any) (any, error) {
		timerCtx, cancel := context.WithTimeout(ctx, budget)
		defer cancel()

		type outcome struct {
			value any
			err   error
		}
		done := make(chan outcome, 1)
		go func() {
			v, e := exec(timerCtx, input)
			done <- outcome{v, e}
		}()

		select {
		case o := <-done:
			return o.value, o.err
		case <-timerCtx.Done():
			// Map only this wrapper's expired timer to TOOL_TIMEOUT. Parent cancellation or an earlier
			// parent deadline remains an ordinary context error.
			if errors.Is(timerCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
				return nil, &TimeoutError{Code: ToolTimeoutCode, Budget: budget, Cause: ErrToolTimeout}
			}
			return nil, ctx.Err()
		}
	}
}

// Opt-in tool pipeline pooling (H06) reuses ExecContext and its Meta map through sync.Pool.
// ToolCallResult must never be reused: callers can retain it after Run returns, so later
// mutation would cause races and corrupt earlier results. Copy the result to a fresh object
// before returning, and reset all execution fields before reuse.
package tools

import (
	"context"
	"sync"
)

// execContextPool reuses execution contexts with empty metadata maps (H06).
var execContextPool = sync.Pool{
	New: func() any { return &ExecContext{Meta: map[string]any{}} },
}

// clearMeta removes all keys while retaining map capacity for reuse.
func clearMeta(m map[string]any) {
	for k := range m {
		delete(m, k)
	}
}

// RunPooled preserves Run semantics while reusing ExecContext and Meta to reduce allocation
// pressure. It returns a fresh result that callers may retain or modify.
func (p *Pipeline) RunPooled(ctx context.Context, req *ToolCallRequest, tool *Tool) *ToolCallResult {
	// Acquire an execution context and reset all fields before reuse.
	ec := execContextPool.Get().(*ExecContext)
	ec.Request = req
	ec.Signal = SignalContinue
	ec.Denied = false
	ec.Ctx = ctx
	if ec.Result == nil {
		ec.Result = &ToolCallResult{}
	}
	if ec.Meta == nil {
		ec.Meta = map[string]any{}
	} else {
		clearMeta(ec.Meta)
	}
	// Use the pooled result internally, then copy it before returning.
	inner := ec.Result
	inner.CallID = req.CallID
	inner.IsError = false
	inner.Value = nil
	inner.Error = ""
	inner.ErrorCode = ""

	inner = p.runStages(ctx, ec, tool)

	out := copyResult(inner)
	releaseExecContext(ec)
	return out
}

// copyResult returns a fresh copy of the pooled execution result.
func copyResult(r *ToolCallResult) *ToolCallResult {
	if r == nil {
		return nil
	}
	return &ToolCallResult{
		CallID:    r.CallID,
		IsError:   r.IsError,
		Value:     r.Value,
		Error:     r.Error,
		ErrorCode: r.ErrorCode,
	}
}

// releaseExecContext returns an execution context to the pool.
func releaseExecContext(ec *ExecContext) {
	if ec == nil {
		return
	}
	// Clear references before pooling so requests, results, and contexts are not retained
	// unnecessarily.
	ec.Request = nil
	ec.Result = nil
	ec.Ctx = nil
	execContextPool.Put(ec)
}

// PooledState reports whether pooling is enabled for diagnostics and tests.
func (p *Pipeline) PooledState() bool {
	if p == nil {
		return false
	}
	return p.pooled
}

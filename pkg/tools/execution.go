// ToolRunContext provides runtime metadata, deferred cleanup, and turn conclusion (M25),
// matching upstream tools/types. ConcludeTurn is authoritative: tools such as goal
// report_blocker can end the current turn, and the agent checks that state before continuing.
// Context propagation exposes the agent-level control inside the pipeline.
package tools

import (
	"context"
	"sync"
)

// concludeKey identifies the conclusion callback in a context.
type concludeKey struct{}

// ConcludeFunc ends the current turn and is registered by the agent.
type ConcludeFunc func(reason string)

// WithConclude attaches a conclusion callback to ctx.
func WithConclude(ctx context.Context, fn ConcludeFunc) context.Context {
	return context.WithValue(ctx, concludeKey{}, fn)
}

// ConcludeFrom returns the conclusion callback, or nil if absent.
func ConcludeFrom(ctx context.Context) ConcludeFunc {
	if fn, ok := ctx.Value(concludeKey{}).(ConcludeFunc); ok {
		return fn
	}
	return nil
}

// ToolRunContext holds runtime state for one tool execution.
type ToolRunContext struct {
	mu        sync.Mutex
	meta      map[string]any
	deferred  []func()
	concluded bool
	conclude  ConcludeFunc
}

// NewToolRunContext creates a tool runtime context.
func NewToolRunContext(conclude ConcludeFunc) *ToolRunContext {
	return &ToolRunContext{meta: map[string]any{}, conclude: conclude}
}

// SetMeta stores runtime metadata.
func (tc *ToolRunContext) SetMeta(k string, v any) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	tc.meta[k] = v
}

// GetMeta reads runtime metadata.
func (tc *ToolRunContext) GetMeta(k string) (any, bool) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	v, ok := tc.meta[k]
	return v, ok
}

// DeferContext registers cleanup to run when the tool finishes, like Go defer.
func (tc *ToolRunContext) DeferContext(fn func()) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	tc.deferred = append(tc.deferred, fn)
}

// runDeferred invokes all deferred callbacks in LIFO order.
func (tc *ToolRunContext) runDeferred() {
	tc.mu.Lock()
	defers := make([]func(), len(tc.deferred))
	copy(defers, tc.deferred)
	tc.deferred = nil
	tc.mu.Unlock()
	for i := len(defers) - 1; i >= 0; i-- {
		if defers[i] != nil {
			defers[i]()
		}
	}
}

// ConcludeTurn marks the current turn as concluded and runs deferred callbacks. Repeated calls
// do not invoke the conclusion callback again.
func (tc *ToolRunContext) ConcludeTurn(reason string) bool {
	tc.mu.Lock()
	if tc.concluded {
		tc.mu.Unlock()
		return false
	}
	tc.concluded = true
	conclude := tc.conclude
	tc.mu.Unlock()

	tc.runDeferred()
	if conclude != nil {
		conclude(reason)
	}
	return true
}

// IsConcluded reports whether the agent should stop advancing the turn.
func (tc *ToolRunContext) IsConcluded() bool {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	return tc.concluded
}

// Context returns a context carrying this ToolRunContext for tool implementations.
func (tc *ToolRunContext) Context() context.Context {
	return withRunContext(context.Background(), tc)
}

// runContextKey identifies ToolRunContext within a context.
type runContextKey struct{}

// withRunContext attaches ToolRunContext to ctx.
func withRunContext(ctx context.Context, tc *ToolRunContext) context.Context {
	return context.WithValue(ctx, runContextKey{}, tc)
}

// ToolRunContextFrom retrieves ToolRunContext from ctx.
func ToolRunContextFrom(ctx context.Context) (*ToolRunContext, bool) {
	tc, ok := ctx.Value(runContextKey{}).(*ToolRunContext)
	return tc, ok
}

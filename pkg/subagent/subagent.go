// Package subagent defines three delegation backends (S02), matching subagent
// and providers upstream. Provider separates launching from task execution.
// InProcess reuses agent context; ACP and ForkCopy are stable-interface stubs.
// ForkLineage records parent/child sessions for causal attribution (M33). Handle
// manages lifecycle: Drain waits for completion and persistence, while Dispose
// releases the child as part of cascading parent cleanup.
package subagent

import (
	"context"
	"sync"

	"github.com/herlanai/omnix/pkg/brand"
)

// Types.

// ForkLineage records a parent-to-child session relationship.
type ForkLineage struct {
	Parent  *brand.SessionID `json:"parent,omitempty"` // Parent session; nil for a top-level agent.
	Session brand.SessionID  `json:"session"`          // Child session.
}

// SpawnRequest requests a child agent.
type SpawnRequest struct {
	Parent    *brand.SessionID `json:"parent,omitempty"`
	Input     string           `json:"input"`
	MaxRounds int              `json:"maxRounds,omitempty"`
}

// SpawnResult contains a child agent's output.
type SpawnResult struct {
	Lineage ForkLineage `json:"lineage"`
	Output  string      `json:"output"`
	Err     string      `json:"err,omitempty"`
}

// Provider abstracts child-agent backends.
type Provider interface {
	// Name returns the backend name, such as in-process, acp, or fork-copy.
	Name() string
	// Spawn starts a child agent and returns a disposable handle.
	Spawn(ctx context.Context, req SpawnRequest) (*Handle, error)
}

// Handle manages a child agent's lifecycle.
type Handle struct {
	Provider string // Owning backend name.
	Lineage  ForkLineage
	mu       sync.Mutex
	disposed bool
	drained  bool
	done     chan struct{}
}

// newHandle constructs a handle.
func newHandle(provider string, lineage ForkLineage) *Handle {
	return &Handle{Provider: provider, Lineage: lineage, done: make(chan struct{})}
}

// markDrained marks the child persisted/completed when the receiver finishes.
func (h *Handle) markDrained() {
	h.mu.Lock()
	if !h.drained {
		h.drained = true
		close(h.done)
	}
	h.mu.Unlock()
}

// Drain waits for completion or context cancellation.
func (h *Handle) Drain(ctx context.Context) error {
	select {
	case <-h.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Dispose marks the child disposed for cascading parent cleanup.
func (h *Handle) Dispose() {
	h.mu.Lock()
	h.disposed = true
	h.mu.Unlock()
	// Signal receiver cleanup and mark drained so Drain returns immediately.
	h.markDrained()
}

// ACP and ForkCopy placeholder providers.

// acpProvider is a child-backend stub; replace Spawn for native protocol integration.
type acpProvider struct{}

func (acpProvider) Name() string { return "acp" }

func (acpProvider) Spawn(ctx context.Context, req SpawnRequest) (*Handle, error) {
	sid := brand.NewSessionID("acp-" + req.Input)
	h := newHandle("acp", ForkLineage{Parent: req.Parent, Session: sid})
	// The stub completes synchronously.
	h.markDrained()
	return h, nil
}

// forkCopyProvider is a fork-copy-process backend stub.
type forkCopyProvider struct{}

func (forkCopyProvider) Name() string { return "fork-copy" }

func (forkCopyProvider) Spawn(ctx context.Context, req SpawnRequest) (*Handle, error) {
	sid := brand.NewSessionID("fork-" + req.Input)
	h := newHandle("fork-copy", ForkLineage{Parent: req.Parent, Session: sid})
	h.markDrained()
	return h, nil
}

// ============================================================================
// Runtime
// ============================================================================

// Runtime owns provider registrations and active child handles.
type Runtime struct {
	mu        sync.RWMutex
	providers map[string]Provider
	handles   map[string]*Handle
}

// NewRuntime registers the built-in ACP and ForkCopy stubs.
func NewRuntime() *Runtime {
	r := &Runtime{providers: map[string]Provider{}, handles: map[string]*Handle{}}
	// Built-in runnable stubs.
	_ = r.RegisterProvider(acpProvider{})
	_ = r.RegisterProvider(forkCopyProvider{})
	return r
}

// RegisterProvider registers a backend.
func (r *Runtime) RegisterProvider(p Provider) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[p.Name()] = p
	return nil
}

// Provider looks up a backend.
func (r *Runtime) Provider(name string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[name]
	return p, ok
}

// Spawn launches through the selected backend and registers its handle.
func (r *Runtime) Spawn(ctx context.Context, providerName string, req SpawnRequest) (*Handle, error) {
	p, ok := r.Provider(providerName)
	if !ok {
		return nil, &ErrUnknownProvider{Name: providerName}
	}
	h, err := p.Spawn(ctx, req)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.handles[h.Lineage.Session.Raw()] = h
	r.mu.Unlock()
	return h, nil
}

// DisposeOwner disposes all children belonging to a parent session.
func (r *Runtime) DisposeOwner(parent brand.SessionID) {
	r.mu.RLock()
	handles := make([]*Handle, 0, len(r.handles))
	for _, h := range r.handles {
		if h.Lineage.Parent != nil && *h.Lineage.Parent == parent {
			handles = append(handles, h)
		}
	}
	r.mu.RUnlock()
	for _, h := range handles {
		h.Dispose()
	}
}

// ActiveCount returns the registered handle count for diagnostics.
func (r *Runtime) ActiveCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.handles)
}

// Errors.

// ErrUnknownProvider indicates an unregistered backend.
type ErrUnknownProvider struct{ Name string }

func (e *ErrUnknownProvider) Error() string {
	return "subagent: unknown provider " + e.Name
}

// IsUnknownProvider identifies unknown-backend errors.
func IsUnknownProvider(err error) bool {
	_, ok := err.(*ErrUnknownProvider)
	return ok
}

// This file follows upstream packages/core/agent/src/model-selection.ts.
// Upstream captures model selection before prompt assembly and applies that
// snapshot when routing the request, so concurrent changes affect only later
// steps. Omnix's synchronous assembler uses a thread-safe current/assembled
// state machine with explicit Capture and Apply calls instead of asynchronous
// Cordis waterfall events.
package agent

import "sync"

// ModelSelection identifies the provider, model, and optional reasoning effort for an active agent.
type ModelSelection struct {
	// Provider is the registered provider route.
	Provider string
	// Model is the provider-owned model identifier.
	Model string
	// ReasoningEffort is adapter-defined; empty selects provider defaults.
	ReasoningEffort string
}

// ModelSelectionRef holds mutable selection and the snapshot captured for the current step.
type ModelSelectionRef struct {
	mu sync.RWMutex
	// current is the selection used by the next step entering prompt assembly.
	current *ModelSelection
	// assembled is the selection captured when the current step entered prompt assembly.
	assembled *ModelSelection
}

// NewModelSelectionRef creates an empty selector.
func NewModelSelectionRef() *ModelSelectionRef {
	return &ModelSelectionRef{}
}

// Select updates the next step's selection concurrently; nil restores defaults.
func (r *ModelSelectionRef) Select(sel *ModelSelection) {
	r.mu.Lock()
	r.current = cloneSelection(sel)
	r.mu.Unlock()
}

// Current returns a copy of the current selection.
func (r *ModelSelectionRef) Current() *ModelSelection {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cloneSelection(r.current)
}

// Capture freezes current into assembled at the prompt assembly boundary.
// A nil snapshot means this step does not specify a model.
func (r *ModelSelectionRef) Capture() *ModelSelection {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.assembled = cloneSelection(r.current)
	return cloneSelection(r.assembled)
}

// Assembled returns a copy of the snapshot captured for this step.
func (r *ModelSelectionRef) Assembled() *ModelSelection {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cloneSelection(r.assembled)
}

// RequestConfig contains mutable request fields used for model routing.
type RequestConfig struct {
	Provider        string
	Model           string
	ReasoningEffort string
}

// Apply follows upstream agent/request semantics. Without a captured selection,
// it preserves inherited configuration. Otherwise it clears inherited reasoning
// effort, applies provider/model, and uses the captured effort if present.
// An empty captured effort restores the selected provider's default behavior.
func (r *ModelSelectionRef) Apply(cfg RequestConfig) RequestConfig {
	r.mu.RLock()
	sel := cloneSelection(r.assembled)
	r.mu.RUnlock()

	if sel == nil {
		return cfg
	}
	out := cfg
	out.ReasoningEffort = "" // Clear inherited reasoning effort.
	out.Provider = sel.Provider
	out.Model = sel.Model
	if sel.ReasoningEffort != "" {
		out.ReasoningEffort = sel.ReasoningEffort
	}
	return out
}

// cloneSelection copies selection to avoid sharing pointers with callers.
func cloneSelection(s *ModelSelection) *ModelSelection {
	if s == nil {
		return nil
	}
	cp := *s
	return &cp
}

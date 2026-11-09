// The session projection registry (M16) follows upstream session-projection.
// SDK consumers read derived state through registered projections rather than
// raw events. Each projection maintains state incrementally and exposes a
// changelog; snapshots should agree with their fold functions.
package session

import (
	"sort"
	"sync"

	"github.com/herlanai/omnix/pkg/brand"
)

// ProjectionState constrains projection state types.
type ProjectionState interface {
	// Equal detects state changes for incremental changelog entries.
	Equal(other ProjectionState) bool
}

// ProjectionDefinition defines one projection.
type ProjectionDefinition[S ProjectionState] struct {
	// ID uniquely identifies the projection.
	ID brand.ProjectionID
	// Init is the initial state.
	Init S
	// Apply incrementally applies an event to state.
	Apply func(state S, ev SessionEvent) S
	// Merge rebuilds state from complete event history, matching its fold function.
	Merge func(events []SessionEvent) S
}

// Projection is a registered projection instance.
type Projection[S ProjectionState] struct {
	def   *ProjectionDefinition[S]
	state S
}

// State returns the current projection state.
func (p *Projection[S]) State() S {
	return p.state
}

// projectionBox abstracts runtime projections within this package.
type projectionBox interface {
	apply(ev SessionEvent) bool
	projectionID() brand.ProjectionID
	stateValue() any
	rebuild(events []SessionEvent)
}

// These methods implement projectionBox for Projection[S].

func (p *Projection[S]) apply(ev SessionEvent) bool {
	if p.def == nil || p.def.Apply == nil {
		return false
	}
	newState := p.def.Apply(p.state, ev)
	changed := !newState.Equal(p.state)
	if changed {
		p.state = newState
	}
	return changed
}

func (p *Projection[S]) projectionID() brand.ProjectionID {
	return p.def.ID
}

func (p *Projection[S]) stateValue() any { return p.state }

func (p *Projection[S]) rebuild(events []SessionEvent) {
	if p.def == nil {
		return
	}
	if p.def.Merge != nil {
		p.state = p.def.Merge(events)
		return
	}
	p.state = p.def.Init
	for _, ev := range events {
		if p.def.Apply != nil {
			p.state = p.def.Apply(p.state, ev)
		}
	}
}

// Registry stores registered projections.
type Registry struct {
	mu        sync.RWMutex
	defs      map[string]projectionBox // ProjectionID.Raw() -> projectionBox
	seq       uint64
	changelog []ProjectionChange
}

// ProjectionChange records a projection changelog entry.
type ProjectionChange struct {
	// ProjectionID identifies the projection.
	ProjectionID brand.ProjectionID `json:"projectionId"`
	// Seq is the changelog sequence number.
	Seq uint64 `json:"seq"`
	// EventSeq identifies the triggering event.
	EventSeq uint64 `json:"eventSeq"`
	// Changed reports whether state changed.
	Changed bool `json:"changed"`
}

// NewProjectionRegistry creates an empty registry.
func NewProjectionRegistry() *Registry {
	return &Registry{defs: map[string]projectionBox{}}
}

// RegisterProjection registers a definition. It is a generic function because
// Go methods cannot declare their own type parameters.
func RegisterProjection[S ProjectionState](r *Registry, def *ProjectionDefinition[S]) (*Projection[S], error) {
	if def == nil || def.ID.IsZero() {
		return nil, &projectionError{"nil or zero-id projection"}
	}
	p := &Projection[S]{def: def, state: def.Init}
	r.mu.Lock()
	r.defs[def.ID.Raw()] = p
	r.mu.Unlock()
	return p, nil
}

// projectionError describes a projection failure.
type projectionError struct{ msg string }

func (e *projectionError) Error() string { return "projection: " + e.msg }

// ApplyEvents incrementally updates all projections and records changelog entries.
func (r *Registry) ApplyEvents(events []SessionEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ev := range events {
		r.seq++
		for _, box := range r.defs {
			changed := box.apply(ev)
			r.changelog = append(r.changelog, ProjectionChange{
				ProjectionID: box.projectionID(),
				Seq:          r.seq,
				EventSeq:     ev.Seq,
				Changed:      changed,
			})
		}
	}
}

// Changelog returns a snapshot in production order.
func (r *Registry) Changelog() []ProjectionChange {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ProjectionChange, len(r.changelog))
	copy(out, r.changelog)
	return out
}

// SnapshotAll returns a map of all current projection states.
func (r *Registry) SnapshotAll() map[brand.ProjectionID]any {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := map[brand.ProjectionID]any{}
	keys := make([]string, 0, len(r.defs))
	for id := range r.defs {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	for _, id := range keys {
		out[brand.NewProjectionID(id)] = r.defs[id].stateValue()
	}
	return out
}

// Rebuild replays complete history into all projections for consistency checking.
func (r *Registry) Rebuild(events []SessionEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, box := range r.defs {
		box.rebuild(events)
	}
}

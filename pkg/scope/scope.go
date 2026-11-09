// Package scope provides layered registry primitives matching core/scope.
// Tools, skills, commands, and credentials share host defaults with session, user,
// or project overrides. Resolution uses the nearest matching layer; within a layer
// higher rank wins, then later registration breaks ties. Anonymous entries never
// participate in named lookup and appear only in full traversal, in registration order.
package scope

import (
	"sort"
	"sync"
)

// Key identifies a scope layer, such as host, user:xxx, session:xxx, or project:xxx.
type Key string

// entry records a registration within a scope layer.
type entry[V any] struct {
	name  string // Named key; empty means anonymous.
	value V
	rank  int    // Higher rank wins conflicts for the same key.
	seq   uint64 // Registration sequence breaks equal-rank ties in favor of later entries.
}

// Layer contains entries for one scope key. A slice retains registration order
// for deterministic later-registration precedence.
type Layer[V any] struct {
	key     Key
	entries []entry[V]
	mu      sync.RWMutex
	seq     uint64
}

// NewLayer creates an empty layer for a scope key.
func NewLayer[V any](key Key) *Layer[V] {
	return &Layer[V]{key: key}
}

// Key returns the layer identifier.
func (l *Layer[V]) Key() Key {
	return l.key
}

// Register adds an entry and returns its sequence number. Empty names are
// anonymous; higher ranks win same-key conflicts within the layer.
func (l *Layer[V]) Register(name string, value V, rank int) uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	l.entries = append(l.entries, entry[V]{name: name, value: value, rank: rank, seq: l.seq})
	return l.seq
}

// get resolves within one layer by highest rank, then latest registration.
func (l *Layer[V]) get(name string) (V, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var (
		found  bool
		winner entry[V]
	)
	for _, e := range l.entries {
		if e.name != name {
			continue
		}
		if !found || e.rank > winner.rank || (e.rank == winner.rank && e.seq > winner.seq) {
			found = true
			winner = e
		}
	}
	return winner.value, found
}

// Get performs named lookup within this layer only.
func (l *Layer[V]) Get(name string) (V, bool) {
	return l.get(name)
}

// Has reports whether this layer has a named entry.
func (l *Layer[V]) Has(name string) bool {
	_, ok := l.get(name)
	return ok
}

// Unregister removes all entries with the name, leaving anonymous entries unchanged.
func (l *Layer[V]) Unregister(name string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := l.entries[:0]
	for _, e := range l.entries {
		if e.name == name {
			continue
		}
		out = append(out, e)
	}
	l.entries = out
}

// snapshot shallow-copies entries for ScopedLayers merging.
func (l *Layer[V]) snapshot() []entry[V] {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]entry[V], len(l.entries))
	copy(out, l.entries)
	return out
}

// ScopedLayers orders layers from host at index zero to the nearest scope last.
// Later layers have higher precedence.
type ScopedLayers[V any] struct {
	mu     sync.RWMutex
	layers []*Layer[V]
}

// NewScopedLayers creates an empty layered registry.
func NewScopedLayers[V any]() *ScopedLayers[V] {
	return &ScopedLayers[V]{}
}

// Push appends the nearest, highest-priority layer; push the host layer first.
func (sl *ScopedLayers[V]) Push(layer *Layer[V]) *ScopedLayers[V] {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	sl.layers = append(sl.layers, layer)
	return sl
}

// Get resolves a name from nearest scope to host, returning the first match
// or false when absent everywhere.
func (sl *ScopedLayers[V]) Get(name string) (V, bool) {
	if name == "" {
		var zero V
		return zero, false
	}
	sl.mu.RLock()
	defer sl.mu.RUnlock()
	// Search from the nearest layer at the end toward the host at the beginning.
	for i := len(sl.layers) - 1; i >= 0; i-- {
		if v, ok := sl.layers[i].get(name); ok {
			return v, true
		}
	}
	var zero V
	return zero, false
}

// ResolvedEntry is one merged result.
type ResolvedEntry[V any] struct {
	Name  string // Named key; empty for anonymous entries.
	Value V
	Rank  int
}

// Merge returns a deterministic view. Named entries resolve once by nearest
// scope, rank, and registration order. All anonymous entries remain in registration
// order across layers. Anonymous entries precede lexically sorted named entries.
func (sl *ScopedLayers[V]) Merge() []ResolvedEntry[V] {
	sl.mu.RLock()
	layers := make([]*Layer[V], len(sl.layers))
	copy(layers, sl.layers)
	sl.mu.RUnlock()

	// Collect all named keys, then resolve them across layers.
	nameSet := map[string]struct{}{}
	var anonymous []ResolvedEntry[V]
	for _, layer := range layers {
		for _, e := range layer.snapshot() {
			if e.name == "" {
				anonymous = append(anonymous, ResolvedEntry[V]{Name: "", Value: e.value, Rank: e.rank})
				continue
			}
			nameSet[e.name] = struct{}{}
		}
	}

	names := make([]string, 0, len(nameSet))
	for n := range nameSet {
		names = append(names, n)
	}
	// Sort lexically for deterministic output.
	sort.Strings(names)

	resolved := make([]ResolvedEntry[V], 0, len(names)+len(anonymous))
	for _, n := range names {
		if v, ok := sl.Get(n); ok {
			resolved = append(resolved, ResolvedEntry[V]{Name: n, Value: v, Rank: 0})
		}
	}
	resolved = append(resolved, anonymous...)
	return resolved
}

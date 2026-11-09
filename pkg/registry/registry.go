// Freezable shared registries (H07) optimize read-heavy post-startup access.
// Before Freeze, reads and writes use a mutex for dynamic registration. Freeze
// irreversibly publishes an immutable snapshot; Get, Contains, Len, and Keys
// then read immutable entries after obtaining the snapshot under a read lock.
// Put and Remove reject subsequent writes with ErrFrozen.
package registry

import (
	"errors"
	"sync"
)

// ErrFrozen indicates that a frozen registry rejects writes.
var ErrFrozen = errors.New("registry: frozen, mutation not allowed")

// Freezable is a generic shared registry that can become immutable.
type Freezable[K comparable, V any] struct {
	mu sync.RWMutex
	m  map[K]V

	frozen   bool
	snapshot map[K]V // Immutable frozen snapshot; nil before freezing.
}

// NewFreezable creates an empty registry.
func NewFreezable[K comparable, V any]() *Freezable[K, V] {
	return &Freezable[K, V]{m: map[K]V{}}
}

// NewFreezableFrom copies the supplied map to avoid sharing its backing table.
func NewFreezableFrom[K comparable, V any](entries map[K]V) *Freezable[K, V] {
	cp := make(map[K]V, len(entries))
	for k, v := range entries {
		cp[k] = v
	}
	return &Freezable[K, V]{m: cp}
}

// Put inserts or replaces an entry, returning ErrFrozen after freezing.
func (r *Freezable[K, V]) Put(k K, v V) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen {
		return ErrFrozen
	}
	r.m[k] = v
	return nil
}

// Remove deletes an entry, returning ErrFrozen after freezing.
func (r *Freezable[K, V]) Remove(k K) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen {
		return ErrFrozen
	}
	delete(r.m, k)
	return nil
}

// snapshotRef returns the immutable snapshot, or nil before freezing.
// The reference is acquired under RLock. The published map is never modified,
// so callers may read its entries without holding that lock.
func (r *Freezable[K, V]) snapshotRef() map[K]V {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snapshot
}

// Get obtains the snapshot under RLock, then reads its immutable entries.
// Before freezing, it reads the mutable map under a read lock.
func (r *Freezable[K, V]) Get(k K) (V, bool) {
	if snap := r.snapshotRef(); snap != nil {
		v, ok := snap[k]
		return v, ok
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	v, ok := r.m[k]
	return v, ok
}

// Contains checks key presence, acquiring the frozen snapshot under RLock.
func (r *Freezable[K, V]) Contains(k K) bool {
	if snap := r.snapshotRef(); snap != nil {
		_, ok := snap[k]
		return ok
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.m[k]
	return ok
}

// Len returns the entry count.
func (r *Freezable[K, V]) Len() int {
	if snap := r.snapshotRef(); snap != nil {
		return len(snap)
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.m)
}

// Keys returns keys in map iteration order; callers may sort for deterministic output.
func (r *Freezable[K, V]) Keys() []K {
	if snap := r.snapshotRef(); snap != nil {
		return keysOf(snap)
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return keysOf(r.m)
}

// keysOf collects map keys into a slice.
func keysOf[K comparable, V any](m map[K]V) []K {
	out := make([]K, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Freeze irreversibly publishes a read-only snapshot. Writes return ErrFrozen
// and reads acquire the snapshot under RLock before accessing its immutable
// entries. Repeated calls are safe.
func (r *Freezable[K, V]) Freeze() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen {
		return
	}
	snap := make(map[K]V, len(r.m))
	for k, v := range r.m {
		snap[k] = v
	}
	// Publish the snapshot before marking the registry frozen so readers observe
	// the fully initialized map.
	r.snapshot = snap
	r.frozen = true
	// Release the mutable table for GC; the snapshot retains its values.
	r.m = nil
}

// IsFrozen reports whether the registry is frozen.
func (r *Freezable[K, V]) IsFrozen() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.frozen
}

// Clone copies current entries into an independent, unfrozen registry.
func (r *Freezable[K, V]) Clone() *Freezable[K, V] {
	r.mu.RLock()
	src := r.m
	if r.frozen {
		src = r.snapshot
	}
	cp := make(map[K]V, len(src))
	for k, v := range src {
		cp[k] = v
	}
	r.mu.RUnlock()
	return &Freezable[K, V]{m: cp}
}

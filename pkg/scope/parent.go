// Explicit scope parents supplement layered scopes, matching bindScopeParent,
// scopeParentOf, and scopeChainOf upstream. Resolution walks the ancestor chain.
// Bindings form a DAG: reject links that would return to the starting scope so
// all consumers can safely walk to the root.
package scope

import (
	"errors"
	"sync"
)

// ErrAlreadyBound rejects a second parent binding for a key.
var ErrAlreadyBound = errors.New("scope: key already bound to a parent")

// ErrCycle indicates that a proposed parent link would create a cycle.
var ErrCycle = errors.New("scope: parent link would form a cycle")

// ParentTree is a concurrency-safe scope-parent registry.
type ParentTree struct {
	mu      sync.RWMutex
	parents map[Key]Key
}

// NewParentTree creates an empty parent registry.
func NewParentTree() *ParentTree {
	return &ParentTree{parents: map[Key]Key{}}
}

// Bind assigns a parent once. Existing bindings return ErrAlreadyBound;
// a parent chain reaching key returns ErrCycle.
func (t *ParentTree) Bind(key, parent Key) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, exists := t.parents[key]; exists {
		return ErrAlreadyBound
	}
	// Walk from parent upward; reaching key would create a cycle.
	for cursor := parent; ; {
		if cursor == key {
			return ErrCycle
		}
		next, ok := t.parents[cursor]
		if !ok {
			break
		}
		cursor = next
	}
	t.parents[key] = parent
	return nil
}

// Rebind replaces or establishes a parent link after cycle detection.
func (t *ParentTree) Rebind(key, parent Key) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	for cursor := parent; ; {
		if cursor == key {
			return ErrCycle
		}
		next, ok := t.parents[cursor]
		if !ok {
			break
		}
		cursor = next
	}
	t.parents[key] = parent
	return nil
}

// ParentOf returns a key's parent, or false for a root scope.
func (t *ParentTree) ParentOf(key Key) (Key, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	p, ok := t.parents[key]
	return p, ok
}

// ChainOf returns key and its ancestors nearest-first, ending at the root.
func (t *ParentTree) ChainOf(key Key) []Key {
	t.mu.RLock()
	defer t.mu.RUnlock()
	chain := []Key{}
	visited := map[Key]struct{}{} // Guard against infinite loops even if stored data is invalid.
	for cursor := key; ; {
		if _, seen := visited[cursor]; seen {
			break
		}
		visited[cursor] = struct{}{}
		chain = append(chain, cursor)
		next, ok := t.parents[cursor]
		if !ok {
			break
		}
		cursor = next
	}
	return chain
}

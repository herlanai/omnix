// Output retention (S16) preserves canonical tool-result bytes at commit. Post-execute consumers
// may read large results concurrently for context, display, and auditing, so Retain stores an
// immutable copy keyed by ToolCallID and Read returns an independent complete copy under an
// RWMutex. Remove affects future reads without invalidating copies already returned.
package tools

import (
	"bytes"
	"encoding/json"
	"sync"
)

// Concurrent tool-result retention.

// RetainedEntry stores immutable raw bytes and a logical-presence flag.
type RetainedEntry struct {
	raw      []byte
	retained bool
}

// ResultRetention stores canonical tool-result values for concurrent readers.
type ResultRetention struct {
	mu   sync.RWMutex
	data map[string]RetainedEntry
}

// NewResultRetention creates an empty retention store.
func NewResultRetention() *ResultRetention {
	return &ResultRetention{data: map[string]RetainedEntry{}}
}

// Retain copies canonical bytes at commit. Repeated writes for the same call ID use the latest
// value.
func (r *ResultRetention) Retain(callID string, value []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Copy the input so later caller mutations cannot change retained data.
	r.data[callID] = RetainedEntry{
		raw:      append([]byte(nil), value...),
		retained: true,
	}
}

// Read returns an independent complete copy, or nil, false when the entry is absent or no longer
// retained.
func (r *ResultRetention) Read(callID string) ([]byte, bool) {
	r.mu.RLock()
	e, ok := r.data[callID]
	r.mu.RUnlock()
	if !ok || !e.retained {
		return nil, false
	}
	// Give each reader a separate copy so readers cannot affect each other's bytes.
	return append([]byte(nil), e.raw...), true
}

// Remove deletes a retained entry without affecting copies already returned.
func (r *ResultRetention) Remove(callID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.data[callID]; ok {
		e.retained = false
		r.data[callID] = e
	}
}

// Len returns the number of entries currently retained for diagnostics.
func (r *ResultRetention) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.data)
}

// Canonical value extraction.

// Canonicalize converts a result value to bytes: byte slices and strings are copied, other
// values are JSON-encoded, and nil produces empty output.
func Canonicalize(value any) []byte {
	switch v := value.(type) {
	case nil:
		return nil
	case []byte:
		return v
	case string:
		return []byte(v)
	default:
		b, err := json.Marshal(value)
		if err != nil {
			return []byte{}
		}
		return b
	}
}

// RetainResult stores the canonical value from a ToolCallResult.
func (r *ResultRetention) RetainResult(res *ToolCallResult) {
	if res == nil {
		return
	}
	r.Retain(res.CallID.Raw(), Canonicalize(res.Value))
}

// Equal compares bytes for atomicity assertions.
func Equal(a, b []byte) bool {
	return bytes.Equal(a, b)
}

// Package eventbus provides a typed, in-process event bus modeled on Cordis.
// Emit broadcasts sequentially, Bail stops at the first matching result, and
// Serial collects results in registration order. Waterfall dispatch lives in
// pkg/waterfall. Retain and call the disposer returned by On during cleanup.
package eventbus

import (
	"sync"
)

// Listener receives a payload and returns a result. Emit ignores the result;
// Bail and Serial use it for short-circuiting and aggregation, respectively.
type Listener[T any, R any] func(payload T) R

// Bus is a concurrency-safe event channel with ordered listeners.
// The zero value is ready to use. Listener updates copy the registration list
// so dispatch can read a stable snapshot without allocating. This favors
// workloads that dispatch more often than they register or remove listeners.
type Bus[T any, R any] struct {
	mu        sync.RWMutex
	listeners []listenerSlot[T, R]
	nextID    uint64
}

// listenerSlot associates each listener with a stable registration ID.
type listenerSlot[T any, R any] struct {
	id uint64
	fn Listener[T, R]
}

// New creates an empty event bus.
func New[T any, R any]() *Bus[T, R] {
	return &Bus[T, R]{}
}

// On registers a listener and returns an idempotent disposer.
// The listener remains registered until the disposer is called.
func (b *Bus[T, R]) On(fn Listener[T, R]) func() {
	b.mu.Lock()
	b.nextID++
	id := b.nextID
	// Publish a new backing array so in-flight dispatches keep a stable snapshot.
	listeners := make([]listenerSlot[T, R], len(b.listeners)+1)
	copy(listeners, b.listeners)
	listeners[len(b.listeners)] = listenerSlot[T, R]{id: id, fn: fn}
	b.listeners = listeners
	b.mu.Unlock()

	var removed bool
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if removed {
			return
		}
		removed = true
		for i := range b.listeners {
			if b.listeners[i].id == id {
				listeners := make([]listenerSlot[T, R], len(b.listeners)-1)
				copy(listeners, b.listeners[:i])
				copy(listeners[i:], b.listeners[i+1:])
				b.listeners = listeners
				return
			}
		}
	}
}

// snapshot returns the immutable listener list under a short read lock.
// Dispatch runs outside the lock so callbacks may register or remove listeners.
func (b *Bus[T, R]) snapshot() []listenerSlot[T, R] {
	b.mu.RLock()
	out := b.listeners
	b.mu.RUnlock()
	return out
}

// Len returns the number of registered listeners.
func (b *Bus[T, R]) Len() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.listeners)
}

// Emit invokes all listeners in registration order and ignores their results.
func (b *Bus[T, R]) Emit(payload T) {
	for _, slot := range b.snapshot() {
		slot.fn(payload)
	}
}

// Bail returns the first result for which shouldShort returns true.
// If no result matches, it returns the zero value and false.
func (b *Bus[T, R]) Bail(payload T, shouldShort func(R) bool) (R, bool) {
	var zero R
	for _, slot := range b.snapshot() {
		r := slot.fn(payload)
		if shouldShort != nil && shouldShort(r) {
			return r, true
		}
	}
	return zero, false
}

// Serial invokes listeners sequentially and returns results in registration order.
func (b *Bus[T, R]) Serial(payload T) []R {
	fns := b.snapshot()
	out := make([]R, 0, len(fns))
	for _, slot := range fns {
		out = append(out, slot.fn(payload))
	}
	return out
}

// Notification-only specialization.

// Signal is a notification bus whose listeners do not return results.
type Signal[T any] struct {
	inner *Bus[T, struct{}]
}

// NewSignal creates a notification bus.
func NewSignal[T any]() *Signal[T] {
	return &Signal[T]{inner: New[T, struct{}]()}
}

// On registers a notification listener and returns its disposer.
func (s *Signal[T]) On(fn func(T)) func() {
	return s.inner.On(func(payload T) struct{} {
		fn(payload)
		return struct{}{}
	})
}

// Emit broadcasts a notification.
func (s *Signal[T]) Emit(payload T) {
	s.inner.Emit(payload)
}

// Len returns the number of notification listeners.
func (s *Signal[T]) Len() int { return s.inner.Len() }

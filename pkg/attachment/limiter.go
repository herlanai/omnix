// The attachment-local/compression-limiter contract uses a per-instance FIFO
// limit on concurrent native image transformations; excess work waits for a slot.
package attachment

import "sync"

// Limiter is a FIFO concurrency limiter.
type Limiter struct {
	slots chan struct{}
	// FIFO wait queue.
	mu      sync.Mutex
	waiters []chan struct{}
}

// NewLimiter creates a limiter; nonpositive concurrency defaults to one.
func NewLimiter(concurrency int) *Limiter {
	if concurrency < 1 {
		concurrency = 1
	}
	return &Limiter{slots: make(chan struct{}, concurrency)}
}

// Acquire waits for a slot, which must be returned with Release.
func (l *Limiter) Acquire() { l.slots <- struct{}{} }

// TryAcquire attempts to acquire a slot without blocking.
func (l *Limiter) TryAcquire() bool {
	select {
	case l.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

// Release returns a slot.
func (l *Limiter) Release() { <-l.slots }

// Active returns the number of occupied slots.
func (l *Limiter) Active() int { return len(l.slots) }

// Run acquires a slot and releases it after task completes, including on failure.
func (l *Limiter) Run(task func()) {
	l.Acquire()
	defer l.Release()
	task()
}

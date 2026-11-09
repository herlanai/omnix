// Package schedule implements in-process reminders: after for relative one-shot
// delays, at for absolute one-shot times, and every for fixed intervals of at least
// five minutes. Registration, delivery, and cancellation stay within the owning session.
package schedule

import (
	"errors"
	"sync"
	"time"
)

// MinEveryInterval is the minimum periodic interval: five minutes.
const MinEveryInterval = 5 * time.Minute

// Kind identifies a scheduling rule.
type Kind string

const (
	KindAfter Kind = "after" // Relative one-shot delay.
	KindAt    Kind = "at"    // Absolute one-shot time.
	KindEvery Kind = "every" // Fixed interval.
)

// ErrEmptyPrompt indicates empty reminder content.
var ErrEmptyPrompt = errors.New("schedule: empty prompt")

// ErrNotFuture indicates a target time that is not in the future.
var ErrNotFuture = errors.New("schedule: target is not in the future")

// ErrIntervalTooShort indicates an interval below the minimum.
var ErrIntervalTooShort = errors.New("schedule: every interval below minimum")

// Entry represents an active reminder.
type Entry struct {
	ID       string
	Kind     Kind
	Prompt   string
	NextAt   time.Time
	Interval time.Duration // Used only for every.
}

// Dispatch describes a due reminder notification.
type Dispatch struct {
	ID     string
	Prompt string
}

// Scheduler is a concurrency-safe in-process scheduler.
type Scheduler struct {
	mu      sync.Mutex
	entries map[string]*Entry
	timers  map[string]*time.Timer
	out     chan Dispatch
	now     func() time.Time
}

// New creates a scheduler that sends dispatches to its output channel.
func New() *Scheduler {
	return &Scheduler{
		entries: map[string]*Entry{},
		timers:  map[string]*time.Timer{},
		out:     make(chan Dispatch, 16),
		now:     time.Now,
	}
}

// SetNow injects a clock for testing.
func (s *Scheduler) SetNow(f func() time.Time) {
	s.mu.Lock()
	s.now = f
	s.mu.Unlock()
}

// Out returns the due-notification channel.
func (s *Scheduler) Out() <-chan Dispatch { return s.out }

func (s *Scheduler) arm(id string, at time.Time) {
	d := time.Until(at)
	t := time.AfterFunc(d, func() { s.fire(id) })
	s.timers[id] = t
}

// After registers a relative one-shot reminder.
func (s *Scheduler) After(id, prompt string, delay time.Duration) error {
	if prompt == "" {
		return ErrEmptyPrompt
	}
	if delay <= 0 {
		return ErrNotFuture
	}
	return s.add(&Entry{ID: id, Kind: KindAfter, Prompt: prompt, NextAt: s.now().Add(delay)})
}

// At registers an absolute one-shot reminder.
func (s *Scheduler) At(id, prompt string, at time.Time) error {
	if prompt == "" {
		return ErrEmptyPrompt
	}
	if !at.After(s.now()) {
		return ErrNotFuture
	}
	return s.add(&Entry{ID: id, Kind: KindAt, Prompt: prompt, NextAt: at})
}

// Every registers a fixed-interval reminder.
func (s *Scheduler) Every(id, prompt string, interval time.Duration) error {
	if prompt == "" {
		return ErrEmptyPrompt
	}
	if interval < MinEveryInterval {
		return ErrIntervalTooShort
	}
	e := &Entry{ID: id, Kind: KindEvery, Prompt: prompt, NextAt: s.now().Add(interval), Interval: interval}
	return s.add(e)
}

func (s *Scheduler) add(e *Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[e.ID] = e
	s.arm(e.ID, e.NextAt)
	return nil
}

// fire emits a notification, removes one-shot entries, and reschedules periodic entries.
func (s *Scheduler) fire(id string) {
	s.mu.Lock()
	e, ok := s.entries[id]
	if !ok {
		s.mu.Unlock()
		return
	}
	s.out <- Dispatch{ID: id, Prompt: e.Prompt}
	if e.Kind == KindEvery {
		e.NextAt = s.now().Add(e.Interval)
		s.arm(id, e.NextAt)
	} else {
		delete(s.entries, id)
		delete(s.timers, id)
	}
	s.mu.Unlock()
}

// Cancel cancels a reminder, returning false if absent.
func (s *Scheduler) Cancel(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.timers[id]
	if ok {
		t.Stop()
	}
	_, exists := s.entries[id]
	delete(s.entries, id)
	delete(s.timers, id)
	return exists
}

// List returns a snapshot of active reminders.
func (s *Scheduler) List() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Entry, 0, len(s.entries))
	for _, e := range s.entries {
		out = append(out, *e)
	}
	return out
}

// Shutdown stops all timers.
func (s *Scheduler) Shutdown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.timers {
		t.Stop()
	}
	s.entries = map[string]*Entry{}
	s.timers = map[string]*time.Timer{}
}

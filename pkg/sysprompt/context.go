// PromptContext registration and snapshots (M10), matching context-prompt.
// Static PromptSections form the system prefix; dynamic Context fragments append
// to user messages and may be added or removed. Change-only persistence retains
// the latest snapshot after compaction so context can be reconstructed.
package sysprompt

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"sync"
)

// RuntimeContext is a dynamic fragment whose registration or removal takes effect immediately.
type RuntimeContext struct {
	// Name uniquely identifies the fragment.
	Name string
	// Order determines concatenation order at the end of the user message.
	Order int
	// Content is the latest dynamic snapshot supplied by the caller.
	Content string
}

// ContextRegistry registers PromptContext fragments and hashes snapshots.
type ContextRegistry struct {
	mu       sync.RWMutex
	ctxs     map[string]*RuntimeContext
	lastHash string
}

// NewContextRegistry creates a context registry.
func NewContextRegistry() *ContextRegistry {
	return &ContextRegistry{ctxs: map[string]*RuntimeContext{}}
}

// Register adds or updates a fragment and reports whether its hash changed.
func (r *ContextRegistry) Register(name string, order int, content string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.ctxs[name]; exists && r.ctxs[name].Content == content && r.ctxs[name].Order == order {
		return false // No change.
	}
	r.ctxs[name] = &RuntimeContext{Name: name, Order: order, Content: content}
	return true
}

// Unregister removes a context fragment.
func (r *ContextRegistry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.ctxs, name)
}

// Compute concatenates fragments deterministically in ascending order and hashes them.
func (r *ContextRegistry) Compute() (text string, hash string) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := make([]*RuntimeContext, 0, len(r.ctxs))
	for _, c := range r.ctxs {
		items = append(items, c)
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Order < items[j].Order })
	parts := make([]string, 0, len(items))
	for _, c := range items {
		if c.Content != "" {
			parts = append(parts, c.Content)
		}
	}
	text = strings.Join(parts, "\n")
	if text == "" {
		hash = "" // Empty snapshot.
	} else {
		sum := sha256.Sum256([]byte(text))
		hash = hex.EncodeToString(sum[:])
	}
	return text, hash
}

// Snapshot returns a stable fragment snapshot for change-only persistence.
func (r *ContextRegistry) Snapshot() map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := map[string]string{}
	for name, c := range r.ctxs {
		out[name] = c.Content
	}
	return out
}

// LastHash returns the last Compute hash for change-only injection checks.
func (r *ContextRegistry) LastHash() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lastHash
}

// SetLastHash records the injected hash to prevent duplicate injection.
func (r *ContextRegistry) SetLastHash(h string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastHash = h
}

// GoalRoundContext supplies active-goal continuation context and stops after completion.
type GoalRoundContext struct {
	// Active reports whether the goal is active.
	Active bool
	// Round is the current round.
	Round int
	// GoalDesc describes the goal.
	GoalDesc string
}

// Render returns continuation guidance only for active goals.
func (g *GoalRoundContext) Render() string {
	if !g.Active {
		return "" // Stop injecting after goal completion.
	}
	return "[目标续轮 #" + itoa(g.Round) + "] " + g.GoalDesc
}

// itoa converts an integer to a string.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

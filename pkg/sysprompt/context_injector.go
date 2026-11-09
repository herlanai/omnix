// Change-only PromptContext injection and compaction snapshots (N05, D4).
// ContextRegistry.Compute supplies a stable hash until state changes.
// ChangeOnlyInjector persists the last injected hash and appends new user messages
// only on change; it never mutates the system prompt. Plan, goal, and approval
// changes use this path. CompactPreserve retains the latest snapshot for rebuilding
// derived messages after compaction.
package sysprompt

// Change-only injection.

// ChangeOnlyInjector records the last injected context hash and injects again
// only when Compute returns a different hash.
type ChangeOnlyInjector struct {
	reg      *ContextRegistry
	lastHash string
	injects  int
}

// NewChangeOnlyInjector restores an optional persisted lastHash across turns.
func NewChangeOnlyInjector(reg *ContextRegistry, persistedHash string) *ChangeOnlyInjector {
	return &ChangeOnlyInjector{reg: reg, lastHash: persistedHash}
}

// MightInject returns text only when the computed hash changes. When content
// becomes empty, ClearedContextText explicitly tells the model that prior runtime
// context no longer applies instead of sending an uninformative empty message.
const ClearedContextText = "Current runtime context: none. Earlier runtime-context snapshots no longer apply."

func (in *ChangeOnlyInjector) MightInject() (text string, injected bool) {
	text, hash := in.reg.Compute()
	if hash == in.lastHash {
		return "", false
	}
	hadSnapshot := in.lastHash != ""
	in.lastHash = hash
	in.injects++
	// Replace a formerly nonempty snapshot with an explicit clearing marker.
	if text == "" && hadSnapshot {
		return ClearedContextText, true
	}
	// Return text for one user-message injection without modifying the system prompt.
	return text, true
}

// InjectCount returns the cumulative injection count.
func (in *ChangeOnlyInjector) InjectCount() int { return in.injects }

// LastHash returns the last injected hash for persistence and restoration.
func (in *ChangeOnlyInjector) LastHash() string { return in.lastHash }

// Preserve context snapshots through compaction.

// contextSnapshot captures context at one point in time.
type contextSnapshot struct {
	// Text is concatenated context text.
	Text string
	// Hash is a stable digest.
	Hash string
}

// CompactPreserve retains the latest context snapshot so user-message context
// can be restored after compaction removes older nodes.
type CompactPreserve struct {
	last contextSnapshot
}

// Record replaces the retained snapshot with the latest computed value.
func (c *CompactPreserve) Record(text, hash string) {
	c.last = contextSnapshot{Text: text, Hash: hash}
}

// Latest returns the last snapshot, including after compaction.
func (c *CompactPreserve) Latest() (text, hash string, ok bool) {
	if c.last.Hash == "" {
		return "", "", false
	}
	return c.last.Text, c.last.Hash, true
}

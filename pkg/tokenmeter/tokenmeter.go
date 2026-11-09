// Package tokenmeter measures token usage and enforces budgets (M31), matching
// util/token-meter. It records prompt and completion tokens per request and
// accumulates them per session. Once the cap is reached, the next request is denied
// before calling the LLM. Surface-node weights estimate costs for derived message
// and tool-result snapshots exposed to UI and SDK consumers.
package tokenmeter

import (
	"fmt"
	"sync"
)

// TokenUsage records one model request's token usage.
type TokenUsage struct {
	// PromptTokens counts input tokens.
	PromptTokens int `json:"promptTokens"`
	// CompletionTokens counts output tokens.
	CompletionTokens int `json:"completionTokens"`
}

// Total returns total request tokens.
func (u TokenUsage) Total() int {
	return u.PromptTokens + u.CompletionTokens
}

// RequestRecord records one measurement.
type RequestRecord struct {
	Seq   int        `json:"seq"`
	Usage TokenUsage `json:"usage"`
}

// Meter measures session-level tokens.
type Meter struct {
	mu        sync.Mutex
	budget    int // Session budget; nonpositive means unlimited.
	seq       int // Request sequence number.
	records   []RequestRecord
	totalProm int
	totalComp int
	denied    bool // Whether the budget has entered the deny state.
}

// New creates a meter; nonpositive budgets are unlimited.
func New(budget int) *Meter {
	return &Meter{budget: budget}
}

// Budget returns the budget cap.
func (m *Meter) Budget() int {
	return m.budget
}

// Record records usage and updates budget denial.
func (m *Meter) Record(u TokenUsage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	m.totalProm += u.PromptTokens
	m.totalComp += u.CompletionTokens
	m.records = append(m.records, RequestRecord{Seq: m.seq, Usage: u})
	if m.budget > 0 && m.totalProm+m.totalComp >= m.budget {
		m.denied = true
	}
}

// HasBudget permits another request only while below the cap. Callers must
// check before invoking the LLM to avoid an actual over-budget request.
func (m *Meter) HasBudget() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return !m.denied
}

// BudgetDenied reports whether the budget has denied further requests.
func (m *Meter) BudgetDenied() bool {
	return !m.HasBudget()
}

// Totals returns cumulative prompt, completion, and total tokens.
func (m *Meter) Totals() (prompt, completion, total int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.totalProm, m.totalComp, m.totalProm + m.totalComp
}

// Records returns a snapshot of all measurements.
func (m *Meter) Records() []RequestRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]RequestRecord, len(m.records))
	copy(out, m.records)
	return out
}

// BudgetDeniedError uses a stable message for caller auditing.
const BudgetDeniedError = "token-meter: session budget exceeded; request rejected before LLM call"

// Check validates budget before a request; callers must not invoke the LLM on failure.
func (m *Meter) Check() error {
	if m.BudgetDenied() {
		return fmt.Errorf("%s (prompt+completion >= %d)", BudgetDeniedError, m.budget)
	}
	return nil
}

// SurfacePricing maps derived-node types to token weights; cost is weight times count.
type SurfacePricing map[string]int

var defaultSurfacePricing = SurfacePricing{
	"assistant/message": 4,
	"tool/result":       2,
	"user/message":      1,
}

// CostOf estimates surface-node cost; unknown types have zero cost.
func (p SurfacePricing) CostOf(entries []SurfaceNode) int {
	total := 0
	for _, e := range entries {
		if w, ok := p[e.Kind]; ok {
			total += w * e.Count
		}
	}
	return total
}

// SurfaceNode counts occurrences of a derived-node type.
type SurfaceNode struct {
	// Kind identifies the node type, such as assistant/message.
	Kind string
	// Count is the number of occurrences.
	Count int
}

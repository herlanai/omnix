// M31 token-meter tests cover per-request and per-session usage, a 10000-token budget rejecting
// subsequent requests before real LLM calls, and surface-node cost estimates.
package tests

import (
	"testing"

	"github.com/herlanai/omnix/pkg/tokenmeter"
)

// TestTokenMeterAccumulate verifies per-request and cumulative usage.
func TestTokenMeterAccumulate(t *testing.T) {
	m := tokenmeter.New(100000)
	m.Record(tokenmeter.TokenUsage{PromptTokens: 1000, CompletionTokens: 500})
	m.Record(tokenmeter.TokenUsage{PromptTokens: 2000, CompletionTokens: 300})

	prompt, comp, total := m.Totals()
	if prompt != 3000 || comp != 800 || total != 3800 {
		t.Fatalf("累计异常: prompt=%d comp=%d total=%d", prompt, comp, total)
	}
	if len(m.Records()) != 2 {
		t.Fatalf("应有 2 条计量记录, 实际 %d", len(m.Records()))
	}
	if !m.HasBudget() {
		t.Fatal("未超预算应 HasBudget()=true")
	}
}

// TestTokenMeterBudgetDeny prevents LLM calls after reaching the 10000-token limit.
func TestTokenMeterBudgetDeny(t *testing.T) {
	const budget = 10000
	m := tokenmeter.New(budget)

	// Accumulate usage toward the budget.
	llmCalled := 0
	for i := 0; i < 20; i++ {
		// Check the budget before invoking the LLM.
		if err := m.Check(); err != nil {
			break // Reject without making a real call.
		}
		llmCalled++
		m.Record(tokenmeter.TokenUsage{PromptTokens: 600, CompletionTokens: 400}) // 1000 tokens per turn.
	}

	if !m.BudgetDenied() {
		t.Fatal("累计达预算后应 budget-deny")
	}
	// Each allowed turn records 1000 tokens. Turn 10 starts at 9000 and reaches 10000; turn 11 is
	// denied, so exactly ten LLM calls occur despite attempting twenty turns.
	if llmCalled != 10 {
		t.Fatalf("应恰好产生 10 次真实 LLM 调用后拒绝, 实际 %d", llmCalled)
	}
	if err := m.Check(); err == nil {
		t.Fatal("超预算后 Check 应返回 budget deny 错误")
	}
}

// TestTokenMeterNoBudget treats nonpositive budgets as unlimited.
func TestTokenMeterNoBudget(t *testing.T) {
	m := tokenmeter.New(0) // Unlimited budget.
	for i := 0; i < 30; i++ {
		m.Record(tokenmeter.TokenUsage{PromptTokens: 1000, CompletionTokens: 1000})
	}
	if m.BudgetDenied() {
		t.Fatal("无预算上限不应 deny")
	}
	if _, _, total := m.Totals(); total != 30*2000 {
		t.Fatalf("无限制应累计全部, 实际 %d", total)
	}
}

// TestTokenMeterSurfacePricing verifies surface-node estimates.
func TestTokenMeterSurfacePricing(t *testing.T) {
	p := tokenmeter.SurfacePricing{"assistant/message": 4, "tool/result": 2, "user/message": 1}
	cost := p.CostOf([]tokenmeter.SurfaceNode{
		{Kind: "assistant/message", Count: 3},
		{Kind: "tool/result", Count: 5},
		{Kind: "user/message", Count: 2},
	})
	// 3*4 + 5*2 + 2*1 = 12 + 10 + 2 = 24
	if cost != 24 {
		t.Fatalf("表面定价估算错误: 期望 24, 实际 %d", cost)
	}
}

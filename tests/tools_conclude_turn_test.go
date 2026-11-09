// M25 ToolRunContext deferred callbacks and turn conclusion tests.
package tests

import (
	"testing"

	"github.com/herlanai/omnix/pkg/tools"
)

// TestToolsConcludeTurn marks the turn ended when report_blocker concludes it.
func TestToolsConcludeTurn(t *testing.T) {
	concluded := ""
	tc := tools.NewToolRunContext(func(reason string) { concluded = reason })

	// Simulate goal_report_blocker.
	if !tc.ConcludeTurn("goal-blocker") {
		t.Fatal("首次 conclude 应返回 true")
	}
	if concluded != "goal-blocker" {
		t.Fatalf("conclude reason = %q, want goal-blocker", concluded)
	}
	if !tc.IsConcluded() {
		t.Fatal("conclude 后 IsConcluded 应为 true")
	}

	// Repeated calls must not trigger conclusion again.
	if tc.ConcludeTurn("again") {
		t.Fatal("重复 conclude 应返回 false")
	}
}

// TestToolsDeferContext executes deferred callbacks in LIFO order on conclusion.
func TestToolsDeferContext(t *testing.T) {
	var order []string
	tc := tools.NewToolRunContext(nil)

	tc.DeferContext(func() { order = append(order, "d1") })
	tc.DeferContext(func() { order = append(order, "d2") })

	_ = tc.ConcludeTurn("cleanup")
	// LIFO order executes d2 first.
	if len(order) != 2 || order[0] != "d2" || order[1] != "d1" {
		t.Fatalf("deferred 应按 LIFO 执行: %v", order)
	}
}

// TestToolsRunContextMeta verifies runtime metadata access.
func TestToolsRunContextMeta(t *testing.T) {
	tc := tools.NewToolRunContext(nil)
	tc.SetMeta("started_ms", 100)
	if v, ok := tc.GetMeta("started_ms"); !ok || v != 100 {
		t.Fatalf("meta 读写异常: %v %v", v, ok)
	}
}

// TestToolsConcludeContextPropagation propagates the conclude callback through context.
func TestToolsConcludeContextPropagation(t *testing.T) {
	called := false
	tc := tools.NewToolRunContext(func(string) { called = true })

	ctx := tc.Context()
	got, ok := tools.ToolRunContextFrom(ctx)
	if !ok {
		t.Fatal("应能从 context 取 ToolRunContext")
	}
	got.ConcludeTurn("x")
	if !called {
		t.Fatal("conclude 回调应经 context 传播并触发")
	}
}

// R06 Goal compatibility tests verify active/paused/blocked/complete phases, stable GoalError
// codes and routable causes, invalid-transition and max-round errors, rejection of empty blocker
// reasons, and persistence of BlockReason{code,message} when reporting a blocker. Upstream:
// packages/goal/goal/src/{types,domain}.ts.
package tests

import (
	"context"
	"errors"
	"testing"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/goal"
	"github.com/herlanai/omnix/pkg/session"
	"github.com/herlanai/omnix/pkg/tools"
)

// newGoalLog creates a SessionLog containing an active goal.
func newGoalLog(t *testing.T) *session.SessionLog {
	t.Helper()
	sl := session.NewSessionLog(brand.NewSessionID("r06-goal"))
	if _, err := sl.Append(session.GoalChangeData{
		GoalID: "g1", Phase: session.GoalPhase("active"), Description: "d", MaxRounds: 5, Revision: 1,
	}); err != nil {
		t.Fatal(err)
	}
	return sl
}

// pickTool finds a tool by name.
func pickTool(t *testing.T, sl *session.SessionLog, name string) *tools.Tool {
	t.Helper()
	for _, to := range goal.NewGoalToolset(sl).Tools() {
		if to.Name == name {
			return to
		}
	}
	t.Fatalf("tool %q not found", name)
	return nil
}

func TestR06PhaseHasPaused(t *testing.T) {
	if string(goal.PhasePaused) != "paused" {
		t.Fatalf("PhasePaused = %q, want paused", goal.PhasePaused)
	}
	valid := map[goal.Phase]bool{
		goal.PhaseActive: true, goal.PhasePaused: true,
		goal.PhaseBlocked: true, goal.PhaseComplete: true,
		goal.Phase("bogus"): false, goal.Phase("in-progress"): false,
	}
	for p, want := range valid {
		if p.Valid() != want {
			t.Fatalf("Valid(%q) = %v, want %v", p, p.Valid(), want)
		}
	}
}

func TestR06GoalErrorCodeAndChain(t *testing.T) {
	cause := errors.New("underlying")
	ge := goal.NewGoalError(goal.ErrorStaleRevision, "revision mismatch", cause)
	if string(ge.Code) != "GOAL_STALE_REVISION" {
		t.Fatalf("Code = %q", ge.Code)
	}
	if !errors.Is(ge, cause) {
		t.Fatal("errors.Is 应沿 Cause 下钻到 underlying")
	}
	var as *goal.GoalError
	if !errors.As(ge, &as) || as.Code != goal.ErrorStaleRevision {
		t.Fatalf("errors.As 应还原 GoalError, got %+v", as)
	}
	if goal.FromError(ge) != ge {
		t.Fatal("FromError 应原样返回 GoalError")
	}
	if goal.FromError(errors.New("x")).Code != "unknown" {
		t.Fatal("FromError 普通 error 应归 unknown")
	}
}

func TestR06ToolValidationCodes(t *testing.T) {
	cases := []struct {
		tool  string
		input map[string]any
		code  goal.ErrorCode
	}{
		{"goal_set_phase", map[string]any{"phase": "totally-bogus"}, goal.ErrorInvalidTransition},
		{"goal_set_max_rounds", map[string]any{"maxRounds": float64(-3)}, goal.ErrorInvalidMaxRounds},
		{"goal_add_blocker", map[string]any{"blocker": ""}, goal.ErrorInvalidBlockReason},
		{"goal_report_blocker", map[string]any{"blocker": ""}, goal.ErrorInvalidBlockReason},
	}
	for _, c := range cases {
		sl := newGoalLog(t)
		fn := pickTool(t, sl, c.tool)
		_, err := fn.Execute(context.Background(), c.input)
		var ge *goal.GoalError
		if !errors.As(err, &ge) || ge.Code != c.code {
			t.Fatalf("%s(%v) err=%v, 应含 code=%q", c.tool, c.input, err, c.code)
		}
	}
}

func TestR06ReportBlockerWritesBlockReason(t *testing.T) {
	sl := newGoalLog(t)
	fn := pickTool(t, sl, "goal_report_blocker")
	_, err := fn.Execute(context.Background(), map[string]any{"blocker": "missing api key"})
	if err != nil {
		t.Fatal(err)
	}
	// The persisted goal must be blocked with a readable BlockReason (R06).
	g, ok := goal.FromLog(sl)
	if !ok {
		t.Fatal("after report_blocker, goal should still exist")
	}
	if g.Phase != goal.PhaseBlocked {
		t.Fatalf("持久化 phase = %q, want blocked", g.Phase)
	}
	if g.BlockReason == nil || g.BlockReason.Message != "missing api key" || g.BlockReason.Code == "" {
		t.Fatalf("持久化后 BlockReason 应从日志读回, got %+v", g.BlockReason)
	}
}

func TestR06BlockReasonValidation(t *testing.T) {
	reason, ok := goal.NewBlockReason("missing-credential", "api key absent")
	if !ok || reason.Code != "missing-credential" || reason.Message == "" {
		t.Fatalf("NewBlockReason = %+v ok=%v", reason, ok)
	}
	if _, emptyOk := goal.NewBlockReason("", "x"); emptyOk {
		t.Fatal("code 为空应判定非法")
	}
	if _, emptyOk2 := goal.NewBlockReason("code", ""); emptyOk2 {
		t.Fatal("message 为空应判定非法")
	}
}

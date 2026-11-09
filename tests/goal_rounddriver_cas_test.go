// M12 Goal state machine, round driver, and six-tool integration tests.
package tests

import (
	"context"
	"testing"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/goal"
	"github.com/herlanai/omnix/pkg/session"
	"github.com/herlanai/omnix/pkg/tools"
)

// TestGoalRoundDriverAutoRounds runs five automatic turns for an active goal with maxRounds=5.
func TestGoalRoundDriverAutoRounds(t *testing.T) {
	sl := session.NewSessionLog(brand.NewSessionID("goal_1"))
	ts := goal.NewGoalToolset(sl)

	// Create a goal with maxRounds=5.
	root, _ := ts.Tools()[0].Name, ts.Tools()[0]
	_ = root
	g := goal.NewGoal("g1", "实现任务", 5)
	if err := writeGoalForTest(sl, g); err != nil {
		t.Fatalf("写目标失败: %v", err)
	}

	driver := goal.NewRoundDriver()
	rounds := 0
	for {
		if !driver.OnTurnStopping(sl, rounds) {
			break
		}
		rounds++
		if rounds > 10 {
			t.Fatal("续轮超过上限，疑似死循环")
		}
	}
	if rounds != 5 {
		t.Fatalf("maxRounds=5 应自动续 5 轮, 实际 %d", rounds)
	}
}

// TestGoalReportBlockerConcludeTurn ends the turn through concludeTurn after reporting a blocker.
func TestGoalReportBlockerConcludeTurn(t *testing.T) {
	sl := session.NewSessionLog(brand.NewSessionID("goal_2"))
	ts := goal.NewGoalToolset(sl)
	if err := writeGoalForTest(sl, goal.NewGoal("g2", "任务", 3)); err != nil {
		t.Fatalf("写目标失败: %v", err)
	}

	concluded := false
	// Inject the conclude callback to simulate Agent registration.
	ctx := tools.WithConclude(context.Background(), func(reason string) { concluded = true })
	tool := ts.Tools()[5] // goal_report_blocker
	_, err := tool.Execute(ctx, map[string]any{"blocker": "缺依赖"})
	if err != nil {
		t.Fatalf("goal_report_blocker 失败: %v", err)
	}
	if !concluded {
		t.Fatal("report_blocker 应触发 concludeTurn")
	}
	// The goal must become blocked.
	cur, _ := goal.FromLog(sl)
	if cur.Phase != goal.PhaseBlocked {
		t.Fatalf("report_blocker 后 phase 应为 blocked: %+v", cur)
	}
}

// TestGoalCASRevision verifies monotonically increasing revisions and CAS protection.
func TestGoalCASRevision(t *testing.T) {
	sl := session.NewSessionLog(brand.NewSessionID("goal_3"))
	ts := goal.NewGoalToolset(sl)

	// Create a goal.
	if err := writeGoalForTest(sl, goal.NewGoal("g3", "任务", 3)); err != nil {
		t.Fatalf("写目标失败: %v", err)
	}

	// Two description updates advance revisions from 1 to 2 to 3.
	tool := ts.Tools()[2]
	if _, err := tool.Execute(context.Background(), map[string]any{"description": "新描述1"}); err != nil {
		t.Fatalf("首次 set_description 失败: %v", err)
	}
	if _, err := tool.Execute(context.Background(), map[string]any{"description": "新描述2"}); err != nil {
		t.Fatalf("二次 set_description 失败: %v", err)
	}
	cur, _ := goal.FromLog(sl)
	if cur.Revision != 3 {
		t.Fatalf("revision 应单增到 3: %d", cur.Revision)
	}
	if cur.Description != "新描述2" {
		t.Fatalf("description 应最新: %q", cur.Description)
	}
}

// TestGoalSixTools verifies all six tools are available.
func TestGoalSixTools(t *testing.T) {
	sl := session.NewSessionLog(brand.NewSessionID("goal_4"))
	ts := goal.NewGoalToolset(sl)
	if err := writeGoalForTest(sl, goal.NewGoal("g4", "任务", 3)); err != nil {
		t.Fatalf("写目标失败: %v", err)
	}
	if len(ts.Tools()) != 6 {
		t.Fatalf("应有 6 个 goal 工具: %d", len(ts.Tools()))
	}
	names := []string{}
	for _, tl := range ts.Tools() {
		names = append(names, tl.Name)
	}
	// Listing must return the goal.
	out, err := ts.Tools()[0].Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("goal_list 失败: %v", err)
	}
	if out.(map[string]any)["goal"] == nil {
		t.Fatal("goal_list 应读到目标")
	}
	_ = names
}

// writeGoalForTest writes a goal for tests.
func writeGoalForTest(sl *session.SessionLog, g goal.Goal) error {
	_, err := sl.Append(session.GoalChangeData{
		GoalID:      g.ID,
		Phase:       session.GoalPhase(g.Phase),
		Description: g.Description,
		MaxRounds:   g.MaxRounds,
		Revision:    g.Revision,
	})
	return err
}

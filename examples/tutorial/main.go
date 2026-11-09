// Tutorial: understand the omnix Agent kernel through small, independent Go implementations of
// DeepSeek Harness concepts. Three standalone experiments cover event sourcing, pure fold
// projections derived from the event log, and the Goal state machine that drives continued turns.
// Run: go run ./examples/tutorial. Read alongside pkg/session/session.go, pkg/session/fold.go, and
// pkg/goal/goal.go.
package main

import (
	"context"
	"fmt"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/goal"
	"github.com/herlanai/omnix/pkg/session"
)

func main() {
	// Each step is an independent, reproducible experiment.
	step1EventSourcing()
	fmt.Println()
	step2FoldProjection()
	fmt.Println()
	step3GoalStateMachine()
}

// Step 1: event sourcing. Instead of overwriting current state, append immutable events and derive
// state when needed. This preserves replayable audit history, avoids destructive state
// replacement, and supports forks, compaction, and incremental projections.
func step1EventSourcing() {
	fmt.Println("=== 第 1 步：事件溯源（append-only event log）===")

	// Create a session log with a brand-generated SessionID, optionally using a readable prefix.
	sl := session.NewSessionLog(brand.NewSessionID("tutorial-1"))

	// Append is the only write path and validates ordering invariants automatically.
	_, _ = sl.Append(session.UserMessageData{Content: "帮我评估 omnix"})
	_, _ = sl.Append(session.AssistantMessageData{Content: "好的，我们开始。"})

	// Events exposes every step recorded in the log.
	evs := sl.Events()
	fmt.Printf("— 已追加事件数: %d —\n", len(evs))
	for i, ev := range evs {
		fmt.Printf("  #%d seq=%d type=%s\n", i+1, ev.Seq, ev.Type)
	}

	// Events are immutable facts and are never overwritten by later writes.
	fmt.Println("— 结论：状态 = 历史事件的累积结果，而非一份被覆盖的快照 —")
}

// Step 2: fold projections. Derive messages, titles, goals, and other views from the event log.
// These values can always be recomputed; storing them separately would require synchronization.
// The log is the source of truth and every projection is a function of it.
func step2FoldProjection() {
	fmt.Println("=== 第 2 步：fold 派生投影（状态从事件算出来）===")

	sl := session.NewSessionLog(brand.NewSessionID("tutorial-2"))
	_, _ = sl.Append(session.UserMessageData{Content: "你好"})
	_, _ = sl.Append(session.AssistantMessageData{Content: "你好，我是 omnix。"})

	// FoldAll derives a complete state snapshot; focus here on its Messages projection.
	proj := session.FoldAll(sl.Events())
	fmt.Printf("— 派生消息数: %d —\n", len(proj.Messages))
	for _, m := range proj.Messages {
		fmt.Printf("  [%s] %s\n", m.Role, m.Content)
	}

	// The same log yields other views, such as plan mode and session title. Pure folds produce
	// identical output for identical input, making them testable and cacheable.
	if proj.PlanMode.Present {
		fmt.Printf("— 派生 plan mode: %v —\n", proj.PlanMode.Mode)
	}
	fmt.Println("— 结论：投影 = f(事件日志)；日志不变，投影永远可复现 —")
}

// Step 3: Goal state machine. Goals move through active (RoundDriver continues turns), paused
// (awaiting resumption), blocked (awaiting resolution), and complete (no further turns). This
// example creates a goal and reads its current state through the Goal toolset.
func step3GoalStateMachine() {
	fmt.Println("=== 第 3 步：Goal 状态机（planning state machine）===")

	// Bind GoalToolset to the same session log; goal state is also derived through event sourcing.
	sl := session.NewSessionLog(brand.NewSessionID("tutorial-3"))
	ts := goal.NewGoalToolset(sl)

	// 1. List the six goal_* tools exposed by pkg/goal.
	var names []string
	for _, t := range ts.Tools() {
		names = append(names, t.Name)
	}
	fmt.Printf("— goal 工具集: %v —\n", names)

	// 2. Set a goal for the session, including its description and maximum rounds, and observe
	// validation by the state machine.
	if _, err := call(ts, "goal_set_description", map[string]any{"description": "完成一份评估报告"}); err != nil {
		fmt.Printf("  set_description err: %v\n", err)
	}
	if _, err := call(ts, "goal_set_max_rounds", map[string]any{"maxRounds": float64(5)}); err != nil {
		fmt.Printf("  set_max_rounds err: %v\n", err)
	}

	// 3. Read the current goal state folded from the event log.
	st := sl.Projection().Goal
	fmt.Printf("— 派生目标状态: present=%v, phase=%q, maxRounds=%d —\n",
		st.Present, st.Phase, st.MaxRounds)

	// 4. Submit invalid input to demonstrate stable, routable error codes instead of exposing
	// arbitrary low-level errors.
	if _, err := call(ts, "goal_set_max_rounds", map[string]any{"maxRounds": float64(-1)}); err != nil {
		if ge, ok := err.(*goal.GoalError); ok {
			fmt.Printf("— 稳定错误码示例: %s —\n", ge.Code)
		} else {
			fmt.Printf("— 其它错误: %v —\n", err)
		}
	}
	fmt.Println("— 结论：规划 = 状态机；阶段 + 稳定错误码让 Agent 行为可预期、可驱动 —")
}

// call finds and invokes a tool by name for this tutorial, without the full pipeline.
func call(ts *goal.GoalToolset, name string, input map[string]any) (any, error) {
	for _, t := range ts.Tools() {
		if t.Name == name {
			return t.Execute(context.Background(), input)
		}
	}
	return nil, fmt.Errorf("demo: tool %q not found", name)
}

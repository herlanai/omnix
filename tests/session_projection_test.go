// M16 session projection registry tests.
package tests

import (
	"testing"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/session"
)

// intState is a minimal comparable integer projection state.
type intState int

// Equal implements ProjectionState.
func (s intState) Equal(other session.ProjectionState) bool {
	o, ok := other.(intState)
	return ok && s == o
}

// TestProjectionRegisterAndSubscribe verifies registration and changelog subscription.
func TestProjectionRegisterAndSubscribe(t *testing.T) {
	reg := session.NewProjectionRegistry()

	// Register a projection that counts todo writes.
	todoDef := &session.ProjectionDefinition[intState]{
		ID:   brand.NewProjectionID("todo"),
		Init: 0,
		Apply: func(state intState, ev session.SessionEvent) intState {
			if ev.Type == session.EventTodoWrite {
				return state + 1
			}
			return state
		},
	}
	todoProj, err := session.RegisterProjection(reg, todoDef)
	if err != nil {
		t.Fatalf("Register 失败: %v", err)
	}

	// Construct and apply events.
	reg.ApplyEvents([]session.SessionEvent{
		{Seq: 1, Type: session.EventTodoWrite, Data: session.TodoWriteData{Items: []string{"a"}}},
		{Seq: 2, Type: session.EventTodoWrite, Data: session.TodoWriteData{Items: []string{"a", "b"}}},
		{Seq: 3, Type: session.EventUserMessage, Data: session.UserMessageData{Content: "hi"}},
	})

	// Two todo/write events must produce state 2.
	if todoProj.State() != 2 {
		t.Fatalf("todo 投影状态 = %d, want 2", todoProj.State())
	}

	// Expect three changelog entries, with changed=true for the two todo events.
	ch := reg.Changelog()
	if len(ch) != 3 {
		t.Fatalf("changelog 长度 = %d, want 3", len(ch))
	}
	changedCount := 0
	for _, c := range ch {
		if c.Changed {
			changedCount++
		}
	}
	if changedCount != 2 {
		t.Fatalf("changed 条数 = %d, want 2", changedCount)
	}
}

// TestProjectionSnapshotMatchesFold compares snapshots with fold results.
func TestProjectionSnapshotMatchesFold(t *testing.T) {
	// Construct events.
	events := []session.SessionEvent{
		{Seq: 1, Type: session.EventTodoWrite, Data: session.TodoWriteData{Items: []string{"a"}}},
		{Seq: 2, Type: session.EventPlanMode, Data: session.PlanModeData{Mode: "on"}},
	}
	// Populate event type and time fields consistently.
	events[0].Time = fixedTestTime()
	events[1].Time = fixedTestTime()

	reg := session.NewProjectionRegistry()
	// Register a todo projection using the existing fold semantics.
	todoDef := &session.ProjectionDefinition[intState]{
		ID:   brand.NewProjectionID("todo"),
		Init: 0,
		Merge: func(evs []session.SessionEvent) intState {
			if session.FoldTodoWrite(evs).Present {
				return intState(len(session.FoldTodoWrite(evs).Items))
			}
			return 0
		},
	}
	_, err := session.RegisterProjection(reg, todoDef)
	if err != nil {
		t.Fatalf("Register 失败: %v", err)
	}
	reg.ApplyEvents(events)
	// With Merge but no Apply, Rebuild derives state through full replay.
	reg.Rebuild(events)

	// Compare the complete snapshot against FoldTodoWrite.
	items := session.FoldTodoWrite(events).Items
	expectedState := intState(len(items))
	if got := reg.SnapshotAll()[brand.NewProjectionID("todo")]; got != expectedState {
		t.Fatalf("snapshot = %v, fold = %v（应一致）", got, expectedState)
	}
}

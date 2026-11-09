// Compaction tool-call/result balance tests.
package tests

import (
	"testing"

	"github.com/herlanai/omnix/pkg/compaction"
	"github.com/herlanai/omnix/pkg/session"
)

func msgWithCalls(ids ...string) session.SessionEvent {
	return session.SessionEvent{
		Type: session.EventAssistantMessage,
		Data: session.AssistantMessageData{ToolCallIDs: ids},
	}
}

func toolResultEvent() session.SessionEvent {
	return session.SessionEvent{Type: session.EventToolResult, Data: session.ToolResultData{}}
}

func TestBalancedCutsSimple(t *testing.T) {
	cuts, err := compaction.BalancedCuts(nil)
	if err != nil || len(cuts) != 1 || !cuts[0] {
		t.Fatalf("empty history has one balanced leading cut, got %v err=%v", cuts, err)
	}
}

func TestBalancedCutsCallThenResult(t *testing.T) {
	events := []session.SessionEvent{msgWithCalls("c1"), toolResultEvent()}
	cuts, err := compaction.BalancedCuts(events)
	if err != nil {
		t.Fatal(err)
	}
	// Cut 0 is balanced; cut 1 between call and result is not; cut 2 is balanced again.
	if !cuts[0] || cuts[1] || !cuts[2] {
		t.Fatalf("expected balanced pattern [T F T], got %v", cuts)
	}
}

func TestIsBalancedAt(t *testing.T) {
	events := []session.SessionEvent{msgWithCalls("c1"), toolResultEvent()}
	ok, _ := compaction.IsBalancedAt(events, 1)
	if ok {
		t.Fatal("cut between call and result must be unbalanced")
	}
}

func TestNearestBalancedMovesPastPair(t *testing.T) {
	events := []session.SessionEvent{msgWithCalls("c1"), toolResultEvent()}
	got := compaction.NearestBalancedFrom(events, 1)
	if got != 2 {
		t.Fatalf("cut 1 should move to balanced cut 2, got %d", got)
	}
}

func TestRejectResultWithoutCall(t *testing.T) {
	events := []session.SessionEvent{toolResultEvent()}
	if _, err := compaction.BalancedCuts(events); err != compaction.ErrUnbalanced {
		t.Fatalf("result without call is unbalanced, got %v", err)
	}
}

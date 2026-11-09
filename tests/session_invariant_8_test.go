// N02/D1 tests verify append-only access with copied Events snapshots, eight invariants on valid
// and deliberately corrupted logs, and contiguous sequences with monotonic timestamps across 50
// turns.
package tests

import (
	"strings"
	"testing"
	"time"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/session"
)

// buildHealthyLog creates a valid log covering turn, step, approval, goal, and tool invariants.
func buildHealthyLog(t *testing.T) []session.SessionEvent {
	sl := session.NewSessionLog(brand.NewSessionID("n02"))
	_, _ = sl.Append(session.TurnStartData{Turn: 0})
	_, _ = sl.Append(session.StepStartData{Turn: 0, Step: 1})
	aid := brand.NewApprovalRequestID("ap1")
	_, _ = sl.Append(session.ApprovalRequestIDData{RequestID: aid, Tool: "bash"})
	_, _ = sl.Append(session.ApprovalDecidedData{RequestID: aid, Allowed: true})
	callID := brand.NewToolCallID("tc1")
	_, _ = sl.Append(session.ToolCallData{CallID: callID, Tool: "echo"})
	_, _ = sl.Append(session.ToolResultData{CallID: callID, Output: "hi"})
	_, _ = sl.Append(session.GoalChangeData{GoalID: "g1", Phase: session.GoalPhase("active"), Revision: 1})
	_, _ = sl.Append(session.GoalChangeData{GoalID: "g1", Revision: 2})
	_, _ = sl.Append(session.StepEndData{Turn: 0, Step: 1})
	_, _ = sl.Append(session.TurnEndData{Turn: 0, Reason: session.ReasonFinished})
	return sl.Events()
}

// TestN02AllInvariantsPass accepts all eight invariants for a healthy log.
func TestN02AllInvariantsPass(t *testing.T) {
	events := buildHealthyLog(t)
	if fails := session.VerifyInvariants(events); len(fails) != 0 {
		t.Fatalf("健康日志不应有不变量失败: %v", fails)
	}
}

// TestN02InvSeqGapCaptured detects an injected sequence gap.
func TestN02InvSeqGapCaptured(t *testing.T) {
	events := buildHealthyLog(t)
	// Change the event at index 2 to sequence 999 without reordering.
	corrupt := append([]session.SessionEvent{}, events...)
	corrupt[2].Seq = 999
	corrupt[2].Type = session.EventUserMessage // Use a non-surface event to isolate sequence validation.
	fails := session.VerifyInvariants(corrupt)
	if !hasInvariant(fails, session.InvSeqContinuous) {
		t.Fatalf("seq 跳号应被 seq_continuous 捕获: %v", fails)
	}
}

// TestN02InvTimeRewindCaptured detects timestamps moving backward.
func TestN02InvTimeRewindCaptured(t *testing.T) {
	events := buildHealthyLog(t)
	corrupt := append([]session.SessionEvent{}, events...)
	corrupt[3].Time = corrupt[2].Time.Add(-time.Hour) // Move the timestamp backward.
	fails := session.VerifyInvariants(corrupt)
	if !hasInvariant(fails, session.InvTimeMonotonic) {
		t.Fatalf("时间回退应被 time_monotonic 捕获: %v", fails)
	}
}

// TestN02InvUnpairedTurnCaptured detects an unmatched turn.
func TestN02InvUnpairedTurnCaptured(t *testing.T) {
	// Include turn/start without turn/end.
	var events []session.SessionEvent
	ev := session.SessionEvent{Seq: 1, Time: time.Now(), Type: session.EventTurnStart, Data: session.TurnStartData{Turn: 0}}
	events = append(events, ev)
	fails := session.VerifyInvariants(events)
	if !hasInvariant(fails, session.InvTurnPaired) {
		t.Fatalf("未配对 turn 应被捕获: %v", fails)
	}
}

// TestN02InvGoalCASCaptured detects a skipped goal revision.
func TestN02InvGoalCASCaptured(t *testing.T) {
	events := buildHealthyLog(t)
	// Append revision 4 where revision 3 is expected.
	extra := session.SessionEvent{Seq: 100, Time: time.Now().Add(time.Minute), Type: session.EventGoalChange,
		Data: session.GoalChangeData{GoalID: "g1", Revision: 4}}
	events = append(events, extra)
	fails := session.VerifyInvariants(events)
	if !hasInvariant(fails, session.InvGoalCAS) {
		t.Fatalf("goal revision 跳号应被 goal_revision_cas 捕获: %v", fails)
	}
}

// TestN02InvUnknownTypeCaptured detects an unknown event type.
func TestN02InvUnknownTypeCaptured(t *testing.T) {
	events := buildHealthyLog(t)
	corrupt := append([]session.SessionEvent{}, events...)
	corrupt[0].Type = session.EventType("bogus/type")
	fails := session.VerifyInvariants(corrupt)
	if !hasInvariant(fails, session.InvFormatConsistent) {
		t.Fatalf("未知类型应被 persistence_format_consistent 捕获: %v", fails)
	}
}

// TestN02FiftyRoundsSeqTime verifies contiguous sequences and monotonic time over 50 turns.
func TestN02FiftyRoundsSeqTime(t *testing.T) {
	sl := session.NewSessionLog(brand.NewSessionID("n02-50"))
	for i := uint64(0); i < 50; i++ {
		_, _ = sl.Append(session.TurnStartData{Turn: i})
		_, _ = sl.Append(session.StepStartData{Turn: i, Step: 1})
		_, _ = sl.Append(session.UserMessageData{Content: "hi"})
		_, _ = sl.Append(session.AssistantMessageData{Content: "ok"})
		_, _ = sl.Append(session.StepEndData{Turn: i, Step: 1})
		_, _ = sl.Append(session.TurnEndData{Turn: i, Reason: session.ReasonFinished})
	}
	events := sl.Events()
	if fails := session.VerifyInvariants(events); len(fails) != 0 {
		t.Fatalf("50 轮后不变量应通过: %v", fails)
	}
	// Sequences 1 through 300 must be contiguous.
	if len(events) != 300 || events[299].Seq != 300 {
		t.Fatalf("应 300 条事件且末 seq=300, 实际 %d/%d", len(events), events[len(events)-1].Seq)
	}
	// Timestamps must be monotonic.
	for i := 1; i < len(events); i++ {
		if events[i].Time.Before(events[i-1].Time) {
			t.Fatalf("time 不单调 at %d", i)
		}
	}
}

// TestN02AppendOnlyContract verifies Events returns a copy rather than writable internal storage.
func TestN02AppendOnlyContract(t *testing.T) {
	sl := session.NewSessionLog(brand.NewSessionID("ao"))
	_, _ = sl.Append(session.UserMessageData{Content: "a"})
	_, _ = sl.Append(session.UserMessageData{Content: "b"})
	// Mutating the returned snapshot must not affect the internal log.
	events := sl.Events()
	events[0].Data = session.UserMessageData{Content: "HACK"}
	if sl.Len() != 2 {
		t.Fatal("操作只读拷贝不应改变 Len")
	}
	inner := sl.Events()
	if um, ok := inner[0].Data.(session.UserMessageData); !ok || um.Content != "a" {
		t.Fatal("就地改拷贝不应影响内部事件（严格 append-only)")
	}
}

func hasInvariant(fails []error, code session.InvariantCode) bool {
	for _, f := range fails {
		if strings.Contains(f.Error(), "["+string(code)+"]") {
			return true
		}
	}
	return false
}

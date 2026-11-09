// M04 session append invariant tests.
package tests

import (
	"testing"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/session"
)

// appendTurn constructs a valid start/step/step-end/turn-end sequence; turnIdx is zero-based.
func appendTurn(sl *session.SessionLog, turnIdx uint64) error {
	if _, err := sl.Append(session.TurnStartData{Turn: turnIdx}); err != nil {
		return err
	}
	if _, err := sl.Append(session.StepStartData{Turn: turnIdx, Step: 1}); err != nil {
		return err
	}
	if _, err := sl.Append(session.UserMessageData{Content: "hi"}); err != nil {
		return err
	}
	if _, err := sl.Append(session.StepEndData{Turn: turnIdx, Step: 1}); err != nil {
		return err
	}
	_, err := sl.Append(session.TurnEndData{Turn: turnIdx, Reason: session.ReasonFinished})
	return err
}

// TestSessionAppendSeqContinuous verifies contiguous sequence numbers from 1 through N.
func TestSessionAppendSeqContinuous(t *testing.T) {
	sl := session.NewSessionLog(brand.NewSessionID("s1"))

	seqs := []uint64{}
	for i := 0; i < 5; i++ {
		seq, err := sl.Append(session.UserMessageData{Content: "msg"})
		if err != nil {
			t.Fatalf("Append 失败: %v", err)
		}
		seqs = append(seqs, seq)
	}
	for i, seq := range seqs {
		if seq != uint64(i+1) {
			t.Fatalf("seq 应连续 1..N, 第 %d 条 = %d", i+1, seq)
		}
	}
	if sl.LastSeq() != 5 || sl.Len() != 5 {
		t.Fatalf("LastSeq=%d Len=%d, want 5/5", sl.LastSeq(), sl.Len())
	}
}

// TestSessionTurnPairing rejects turn/end without turn/start.
func TestSessionTurnPairing(t *testing.T) {
	sl := session.NewSessionLog(brand.NewSessionID("s1"))

	// Reject turn/end without a preceding turn/start.
	if _, err := sl.Append(session.TurnEndData{Turn: 0, Reason: session.ReasonFinished}); err == nil {
		t.Fatal("无 turn/start 的 turn/end 应被拒绝")
	}

	// Valid turn.
	if err := appendTurn(sl, 0); err != nil {
		t.Fatalf("合法 turn 应通过: %v", err)
	}
}

// TestSessionStepPairing requires an open turn for step/start.
func TestSessionStepPairing(t *testing.T) {
	sl := session.NewSessionLog(brand.NewSessionID("s1"))

	// Reject step/start outside an open turn.
	if _, err := sl.Append(session.StepStartData{Turn: 0, Step: 1}); err == nil {
		t.Fatal("无开放 turn 的 step/start 应被拒绝")
	}

	// Valid step within a turn.
	_, _ = sl.Append(session.TurnStartData{Turn: 0})
	if _, err := sl.Append(session.StepStartData{Turn: 0, Step: 1}); err != nil {
		t.Fatalf("turn 内 step/start 应通过: %v", err)
	}
	// Reject starting another step before the previous step ends.
	if _, err := sl.Append(session.StepStartData{Turn: 0, Step: 2}); err == nil {
		t.Fatal("step 未关闭时再次 step/start 应被拒绝")
	}
}

// TestSessionToolCallResultPairing rejects unmatched tool calls and results.
func TestSessionToolCallResultPairing(t *testing.T) {
	sl := session.NewSessionLog(brand.NewSessionID("s1"))
	_, _ = sl.Append(session.TurnStartData{Turn: 0})
	_, _ = sl.Append(session.StepStartData{Turn: 0, Step: 1})

	// Reject tool/result without a matching tool/call.
	if _, err := sl.Append(session.ToolResultData{CallID: brand.NewToolCallID("ghost")}); err == nil {
		t.Fatal("无匹配 tool/call 的 tool/result 应被拒绝")
	}

	// Start a tool call.
	callID := brand.NewToolCallID("call_x")
	if _, err := sl.Append(session.ToolCallData{CallID: callID, Tool: "bash"}); err != nil {
		t.Fatalf("tool/call 应通过: %v", err)
	}
	// Reject duplicate call IDs.
	if _, err := sl.Append(session.ToolCallData{CallID: callID, Tool: "bash"}); err == nil {
		t.Fatal("重复 tool/call 应被拒绝")
	}
	// Accept the matching result.
	if _, err := sl.Append(session.ToolResultData{CallID: callID, Output: "ok"}); err != nil {
		t.Fatalf("tool/result 配对应通过: %v", err)
	}
	// Reject a duplicate result.
	if _, err := sl.Append(session.ToolResultData{CallID: callID}); err == nil {
		t.Fatal("重复 tool/result 应被拒绝")
	}
}

// TestSessionValidTurnWithTools accepts a complete valid turn containing tool calls.
func TestSessionValidTurnWithTools(t *testing.T) {
	sl := session.NewSessionLog(brand.NewSessionID("s1"))

	_, _ = sl.Append(session.TurnStartData{Turn: 0})
	_, _ = sl.Append(session.StepStartData{Turn: 0, Step: 1})
	_, _ = sl.Append(session.UserMessageData{Content: "list files"})
	callID := brand.NewToolCallID("call_ls")
	_, _ = sl.Append(session.ToolCallData{CallID: callID, Tool: "bash"})
	_, _ = sl.Append(session.ToolResultData{CallID: callID, Output: "a.txt"})
	_, _ = sl.Append(session.StepEndData{Turn: 0, Step: 1})
	_, err := sl.Append(session.TurnEndData{Turn: 0, Reason: session.ReasonFinished})
	if err != nil {
		t.Fatalf("完整合法 turn 应通过: %v", err)
	}

	// No unmatched tool calls may remain at the end of the log.
	events := sl.Events()
	if len(events) != 7 {
		t.Fatalf("事件数 = %d, want 7", len(events))
	}
}

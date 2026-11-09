// M18: classification of Agent cancellation causes.
package tests

import (
	"testing"

	"github.com/herlanai/omnix/pkg/agent"
	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/session"
)

// TestAgentCancelCauseClassify distinguishes the five cancellation paths.
func TestAgentCancelCauseClassify(t *testing.T) {
	causes := agent.AllCancelCauses()
	if len(causes) != 5 {
		t.Fatalf("取消原因应恰好 5 类: %v", causes)
	}

	for _, cause := range causes {
		sl := session.NewSessionLog(brand.NewSessionID("cancel_" + string(cause)))
		// Open a turn because turn-stopping requires an active turn.
		_, _ = sl.Append(session.TurnStartData{Turn: 0})
		// Record the cancellation.
		if err := agent.RecordCancel(sl, cause); err != nil {
			t.Fatalf("RecordCancel(%s) 失败: %v", cause, err)
		}
		// Extract the cancellation cause.
		got, ok := agent.ExtractCancelCause(sl.Events())
		if !ok {
			t.Fatalf("原因 %s 应被提取到", cause)
		}
		if got != cause {
			t.Fatalf("原因提取 = %s, want %s", got, cause)
		}
	}
}

// TestAgentCancelViaAgentAPI extracts cancellation recorded through Agent.Cancel.
func TestAgentCancelViaAgentAPI(t *testing.T) {
	sl := session.NewSessionLog(brand.NewSessionID("cancel_api"))
	a := agent.NewAgent(brand.NewSessionID("cancel_api"), sl, nil, nil, nil)
	_, _ = sl.Append(session.TurnStartData{Turn: 0})

	a.Cancel(agent.CancelHook)
	got, ok := agent.ExtractCancelCause(sl.Events())
	if !ok || got != agent.CancelHook {
		t.Fatalf("Agent.Cancel 后提取 = %s ok=%v, want hook", got, ok)
	}
}

// TestAgentCancelAbsent returns false when no cancellation was recorded.
func TestAgentCancelAbsent(t *testing.T) {
	sl := session.NewSessionLog(brand.NewSessionID("no_cancel"))
	_, _ = sl.Append(session.UserMessageData{Content: "hi"})

	if _, ok := agent.ExtractCancelCause(sl.Events()); ok {
		t.Fatal("无取消记录时应返回 false")
	}
}

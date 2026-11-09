// S01 compaction tests cover threshold-triggered assistant summaries with SurfaceReplace, reduced
// effective message counts, continued turns, and FoldAll reconstruction from effective events
// without replaced request/header history.
package tests

import (
	"context"
	"testing"

	"github.com/herlanai/omnix/pkg/compaction"
	"github.com/herlanai/omnix/pkg/session"
)

// buildChatEvents constructs n alternating user/assistant turns.
func buildChatEvents(n int) []session.SessionEvent {
	evs := make([]session.SessionEvent, 0, n*2)
	seq := uint64(1)
	for i := 0; i < n; i++ {
		evs = append(evs, session.SessionEvent{Seq: seq, Time: fixedTestTime(), Type: session.EventUserMessage,
			Data: session.UserMessageData{Content: "与用户对话的第 N 轮问题需要足够长以便计入 token 预算 QQQQ"}})
		seq++
		evs = append(evs, session.SessionEvent{Seq: seq, Time: fixedTestTime(), Type: session.EventAssistantMessage,
			Data: session.AssistantMessageData{Content: "助手回答：这是为支撑 token 预算触发的较长回复文本 AAAA"}})
		seq++
	}
	return evs
}

// TestCompactionTriggersAndReplaces verifies threshold-triggered replacement reduces message
// count.
func TestCompactionTriggersAndReplaces(t *testing.T) {
	ctx := context.Background()
	events := buildChatEvents(60) // 120 events.
	cp := compaction.New(compaction.Config{MaxTokens: 40, KeepTail: 2, SummaryMaxRunes: 200}, nil)

	if !cp.ShouldCompact(events) {
		t.Fatal("事件应超过 token 阈值触发压缩")
	}
	res, err := cp.Compact(ctx, events)
	if err != nil {
		t.Fatal(err)
	}
	if res.Start > res.End {
		t.Fatalf("替换范围非法 start=%d end=%d", res.Start, res.End)
	}
	if res.Summary == "" {
		t.Fatal("摘要不应为空")
	}
	newSeq := events[len(events)-1].Seq + 1
	if res.Replacement.Seq != newSeq {
		t.Fatalf("替换事件 seq 应为 %d，实际 %d", newSeq, res.Replacement.Seq)
	}
	if res.Replacement.SurfaceOp == nil {
		t.Fatal("替换事件必须携带 SurfaceOp")
	}

	// Compaction keeps the latest two turns (four messages) and one summary, reducing the original
	// history.
	before := len(session.DeriveMessages(events))
	after := len(session.DeriveMessages(res.Effective))
	if after >= before {
		t.Fatalf("压缩后有效消息数应下降：before=%d after=%d", before, after)
	}
}

// TestCompactionTurnContinues appends messages after compaction.
func TestCompactionTurnContinues(t *testing.T) {
	ctx := context.Background()
	events := buildChatEvents(40)
	cp := compaction.New(compaction.Config{MaxTokens: 30, KeepTail: 2, SummaryMaxRunes: 200}, nil)
	res, err := cp.Compact(ctx, events)
	if err != nil {
		t.Fatal(err)
	}
	effective := res.Effective

	// Append a user message to simulate continuing the conversation after compaction.
	nextSeq := effective[len(effective)-1].Seq + 1
	effective = append(effective, session.SessionEvent{Seq: nextSeq, Time: fixedTestTime(),
		Type: session.EventUserMessage, Data: session.UserMessageData{Content: "压缩之后的新一轮问题 PPPP"}})

	// The derived messages must include the new message, demonstrating continued execution.
	msgs := session.DeriveMessages(effective)
	last := msgs[len(msgs)-1]
	if last.Role != "user" || last.Content != "压缩之后的新一轮问题 PPPP" {
		t.Fatalf("压缩后新消息应可派生，实际最后一条=%+v", last)
	}
}

// TestCompactionHeaderRebuildNoRawEvents rebuilds request headers without replaced source events.
func TestCompactionHeaderRebuildNoRawEvents(t *testing.T) {
	ctx := context.Background()
	events := buildChatEvents(40)
	cp := compaction.New(compaction.Config{MaxTokens: 30, KeepTail: 2, SummaryMaxRunes: 200}, nil)
	res, err := cp.Compact(ctx, events)
	if err != nil {
		t.Fatal(err)
	}
	effective := res.Effective

	// FoldAll reconstructs derived state from effective events after read-time replacement.
	proj := session.FoldAll(effective)
	if len(proj.Messages) == 0 {
		t.Fatal("压缩后应至少仍有派生消息(摘要+保留)")
	}
	// Replaced source sequences must not appear as messages from the oldest turn.
	for _, m := range proj.Messages {
		if m.Seq <= res.Start {
			t.Fatalf("被替换范围 seq<=%d 的原始消息不应再出现在派生消息中，实际 seq=%d", res.Start, m.Seq)
		}
	}
}

// TestCompactionBelowThresholdNoOp skips compaction below the threshold.
func TestCompactionBelowThresholdNoOp(t *testing.T) {
	ctx := context.Background()
	events := buildChatEvents(3) // Six lightweight events.
	cp := compaction.New(compaction.Config{MaxTokens: 10000, KeepTail: 2, SummaryMaxRunes: 200}, nil)
	if cp.ShouldCompact(events) {
		t.Fatal("低负载不应触发压缩")
	}
	if _, err := cp.Compact(ctx, events); err == nil {
		t.Fatal("低于阈值调用 Compact 应返回错误（无需压缩）")
	}
}

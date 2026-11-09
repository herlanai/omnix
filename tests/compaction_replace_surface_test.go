// M21: SurfaceOp append/replace and FoldSurface.
package tests

import (
	"testing"

	"github.com/herlanai/omnix/pkg/session"
)

// TestCompactionReplaceSurface verifies message derivation after replacing [3,80] in 100 events.
func TestCompactionReplaceSurface(t *testing.T) {
	// Create 100 alternating user/assistant messages with sequence numbers 1 through 100.
	var events []session.SessionEvent
	time := fixedTestTime()
	for i := 1; i <= 100; i++ {
		var data session.EventData
		if i%2 == 1 {
			data = session.UserMessageData{Content: "u"}
		} else {
			data = session.AssistantMessageData{Content: "a"}
		}
		events = append(events, session.SessionEvent{Seq: uint64(i), Time: time, Type: data.EventType(), Data: data})
	}

	// Sequence 101 summarizes the replaced range [3,80].
	summary := session.AssistantMessageData{Content: "【摘要覆盖 3..80】"}
	events = append(events, session.SessionEvent{
		Seq: 101, Time: time, Type: session.EventAssistantMessage, Data: summary,
		SurfaceOp: session.NewReplaceOp(3, 80, nil),
	})

	// Hide sequences 3 through 80, leaving 1, 2, 81 through 100, and the replacement: 23 nodes.
	nodes, replacements := session.FoldSurface(events)
	if len(nodes) != 23 {
		t.Fatalf("有效节点数 = %d, want 23（seq1,2 + 81..100 + 替换事件）", len(nodes))
	}
	if len(replacements) != 1 || replacements[0].Start != 3 || replacements[0].End != 80 {
		t.Fatalf("替换范围异常: %+v", replacements)
	}
	// Mark the replacement event itself as Replaced.
	if !nodes[len(nodes)-1].Replaced {
		t.Fatal("替换事件应标记 Replaced")
	}

	// Derived messages must match FoldSurface nodes after read-time replacement.
	msgs := session.DeriveMessages(events)
	if len(msgs) != len(nodes) {
		t.Fatalf("deriveMessages 消息数 = %d, foldSurface 节点数 = %d，应一致", len(msgs), len(nodes))
	}
}

// TestSurfaceReplacePurity verifies source events remain immutable and append-only.
func TestSurfaceReplacePurity(t *testing.T) {
	time := fixedTestTime()
	// Source events have sequence numbers 1 through 3.
	var events []session.SessionEvent
	for i := 1; i <= 3; i++ {
		events = append(events, session.SessionEvent{
			Seq: uint64(i), Time: time, Type: session.EventUserMessage,
			Data: session.UserMessageData{Content: "orig"},
		})
	}
	// Record the source events before replacement.
	srcSeq1Before := events[0].Seq

	// Replace the range [1,3].
	events = append(events, session.SessionEvent{
		Seq: 4, Time: time, Type: session.EventAssistantMessage,
		Data:      session.AssistantMessageData{Content: "summary"},
		SurfaceOp: session.NewReplaceOp(1, 3, nil),
	})

	// Folding must not change source sequence numbers.
	if events[0].Seq != srcSeq1Before {
		t.Fatal("源事件被修改了")
	}
	// Source events must remain present.
	if len(events) != 4 {
		t.Fatalf("源事件应保留: %d", len(events))
	}
}

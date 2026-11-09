// M05 projection-fold consistency and compaction replacement tests.
package tests

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/session"
)

// TestFoldHotVsReplay compares incremental maintenance and cold replay field by field over more
// than 1000 events.
func TestFoldHotVsReplay(t *testing.T) {
	sl := session.NewSessionLog(brand.NewSessionID("fold_1"))

	// Append more than 1000 mixed message and state-change events.
	const turns = 520
	for i := 0; i < turns; i++ {
		_, _ = sl.Append(session.UserMessageData{Content: "user msg"})
		_, _ = sl.Append(session.AssistantMessageData{Content: "assistant msg"})
	}

	// Cover every fold branch with state-change events.
	_, _ = sl.Append(session.PresetChangeData{Preset: "safe"})
	_, _ = sl.Append(session.GoalChangeData{GoalID: "g1", Phase: "active", Description: "do x", MaxRounds: 5, Revision: 1})
	_, _ = sl.Append(session.TodoWriteData{Items: []string{"a", "b", "c"}})
	_, _ = sl.Append(session.PlanModeData{Mode: "on"})
	_, _ = sl.Append(session.SessionTitleData{Title: "My Session"})
	_, _ = sl.Append(session.RequestHeaderData{ConfigEpoch: 2, SystemHash: "h", ToolCount: 3})
	_, _ = sl.Append(session.PresetChangeData{Preset: "danger"}) // Override the preset.

	if sl.Len() < 1000 {
		t.Fatalf("事件数不足 1000: %d", sl.Len())
	}

	// Maintain the hot projection after each append and retain its final snapshot.
	var hot session.SessionProjection
	events := sl.Events()
	for i := 1; i <= len(events); i++ {
		hot = session.FoldAll(events[:i])
	}

	// Cold-replay the complete event list in one fold.
	replay := session.FoldAll(events)

	// Compare every field.
	if !reflect.DeepEqual(hot, replay) {
		t.Fatalf("热/冷 fold 不一致\n热: %+v\n冷: %+v", hot, replay)
	}

	// Assert key semantic values.
	if len(replay.Messages) != turns*2 {
		t.Fatalf("消息数 = %d, want %d", len(replay.Messages), turns*2)
	}
	if replay.Preset.Preset != "danger" {
		t.Fatalf("preset 应最新为 danger: %+v", replay.Preset)
	}
	if replay.SandboxMode.Mode != session.SandboxDangerFullAccess {
		t.Fatalf("danger preset 应映射 full-access: %+v", replay.SandboxMode)
	}
	if replay.Approval.Policy != session.ApprovalAllowAll {
		t.Fatalf("danger preset 应映射 allow-all: %+v", replay.Approval)
	}
	if replay.Goal.GoalID != "g1" || replay.Goal.Revision != 1 {
		t.Fatalf("goal fold 异常: %+v", replay.Goal)
	}
	if !replay.Todo.Present || len(replay.Todo.Items) != 3 {
		t.Fatalf("todo fold 异常: %+v", replay.Todo)
	}
	if replay.PlanMode.Mode != "on" {
		t.Fatalf("plan mode fold 异常: %+v", replay.PlanMode)
	}
	if replay.Title.Title != "My Session" {
		t.Fatalf("title fold 异常: %+v", replay.Title)
	}
	if !replay.RequestHeader.Present || replay.RequestHeader.ConfigEpoch != 2 {
		t.Fatalf("request header fold 异常: %+v", replay.RequestHeader)
	}
}

// TestDeriveMessagesAfterCompactionReplace verifies shortened history and correct replacement
// messages.
func TestDeriveMessagesAfterCompactionReplace(t *testing.T) {
	// Construct ten messages, a replacement at sequence 11, and a new message at sequence 12.
	var events []session.SessionEvent
	time := fixedTestTime()
	for i := 1; i <= 10; i++ {
		role := "user"
		content := "old message"
		if i%2 == 0 {
			role = "assistant"
		}
		var data session.EventData
		if role == "user" {
			data = session.UserMessageData{Content: content}
		} else {
			data = session.AssistantMessageData{Content: content}
		}
		events = append(events, session.SessionEvent{Seq: uint64(i), Time: time, Type: data.EventType(), Data: data})
	}

	// The assistant summary replaces the entire old range [1,10].
	summary := session.AssistantMessageData{Content: "【压缩摘要】前 10 条消息"}
	replaceEv := session.SessionEvent{
		Seq:       11,
		Time:      time,
		Type:      session.EventAssistantMessage,
		Data:      summary,
		SurfaceOp: session.NewReplaceOp(1, 10, nil),
	}
	events = append(events, replaceEv)

	// New message.
	events = append(events, session.SessionEvent{
		Seq: 12, Time: time, Type: session.EventUserMessage,
		Data: session.UserMessageData{Content: "新问题"},
	})

	msgs := session.DeriveMessages(events)

	// Expect only the summary and new message; sequences 1 through 10 are hidden.
	if len(msgs) != 2 {
		t.Fatalf("压缩后消息数 = %d, want 2（摘要+新消息）", len(msgs))
	}
	if msgs[0].Content != "【压缩摘要】前 10 条消息" {
		t.Fatalf("首条应为压缩摘要: %q", msgs[0].Content)
	}
	if msgs[1].Content != "新问题" {
		t.Fatalf("末条应为新消息: %q", msgs[1].Content)
	}
	if msgs[0].Seq != 11 || msgs[1].Seq != 12 {
		t.Fatalf("seq 标记异常: %+v", msgs)
	}
}

// TestFoldDeterministicAcrossReplay verifies pure, repeatable folding of identical events.
func TestFoldDeterministicAcrossReplay(t *testing.T) {
	sl := session.NewSessionLog(brand.NewSessionID("fold_2"))
	_, _ = sl.Append(session.PresetChangeData{Preset: "review"})
	_, _ = sl.Append(session.TodoWriteData{Items: []string{"x"}})
	_, _ = sl.Append(session.SessionTitleData{Title: "T"})
	events := sl.Events()

	p1 := session.FoldAll(events)
	p2 := session.FoldAll(events)

	b1, _ := json.Marshal(p1)
	b2, _ := json.Marshal(p2)
	if string(b1) != string(b2) {
		t.Fatalf("fold 输出不确定\n%s\n%s", b1, b2)
	}
}

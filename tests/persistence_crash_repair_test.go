// M43 persistence, flush checkpoints, and crash repair tests.
package tests

import (
	"context"
	"testing"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/persistence"
	"github.com/herlanai/omnix/pkg/session"
)

// TestPersistenceCrashRepair repairs an orphaned turn after a simulated crash without turn/end.
func TestPersistenceCrashRepair(t *testing.T) {
	dir := t.TempDir()
	backend, err := persistence.NewJSONL(dir, 1) // Batch size one triggers asynchronous persistence.
	if err != nil {
		t.Fatalf("NewJSONL 失败: %v", err)
	}
	ctx := context.Background()

	id := brand.NewSessionID("crash_s")
	header := session.NewSessionHeader(id, "/ws")
	if err := backend.SaveHeader(ctx, header); err != nil {
		t.Fatalf("SaveHeader 失败: %v", err)
	}

	// Persist an incomplete turn and tool events without turn/end to simulate a crash.
	events := []session.SessionEvent{
		{Seq: 1, Type: session.EventTurnStart, Data: session.TurnStartData{Turn: 0}},
		{Seq: 2, Type: session.EventStepStart, Data: session.StepStartData{Turn: 0, Step: 1}},
		{Seq: 3, Type: session.EventUserMessage, Data: session.UserMessageData{Content: "hi"}},
		{Seq: 4, Type: session.EventToolCall, Data: session.ToolCallData{CallID: brand.NewToolCallID("call_1"), Tool: "bash"}},
	}
	for i := range events {
		events[i].Time = fixedTestTime()
		if err := backend.Append(ctx, id, events[i]); err != nil {
			t.Fatalf("Append 失败: %v", err)
		}
	}
	// Explicitly flush accepted events while leaving the turn open.
	if err := backend.Flush(ctx, id); err != nil {
		t.Fatal(err)
	}

	// Reload and repair the persisted session.
	backend2, err := persistence.NewJSONL(dir, 1)
	if err != nil {
		t.Fatalf("重新打开失败: %v", err)
	}
	_, loaded, err := backend2.Load(ctx, id)
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}

	// Expect four original events plus repaired tool/result, step/end, and turn/end events.
	if len(loaded) != 7 {
		t.Fatalf("load 后事件数 = %d, want 7（4 条 + 三条配对修复事件）", len(loaded))
	}
	// The final repair event must be turn/end with reason interrupted.
	last := loaded[len(loaded)-1]
	if last.Type != session.EventTurnEnd {
		t.Fatalf("末尾应为 repair 的 turn/end: %+v", last)
	}
	if td, ok := last.Data.(session.TurnEndData); !ok || td.Reason != session.ReasonInterrupted {
		t.Fatalf("repair 的 turn/end 应为 interrupted: %+v", last.Data)
	}

	// Retain all previously written chunk and tool/call events.
	foundCall := false
	for _, ev := range loaded {
		if ev.Type == session.EventToolCall {
			foundCall = true
		}
	}
	if !foundCall {
		t.Fatal("已写入的 tool/call 事件应被保留")
	}
}

// TestPersistenceForkLineage verifies isolated parent/child persistence directories.
func TestPersistenceForkLineage(t *testing.T) {
	dir := t.TempDir()
	backend, err := persistence.NewJSONL(dir, 1)
	if err != nil {
		t.Fatalf("NewJSONL 失败: %v", err)
	}
	ctx := context.Background()

	parent := session.NewSessionHeader(brand.NewSessionID("parent"), "/ws")
	if err := backend.SaveHeader(ctx, parent); err != nil {
		t.Fatalf("SaveHeader 失败: %v", err)
	}
	child := parent.Fork(brand.NewSessionID("child"))
	if err := backend.SaveHeader(ctx, child); err != nil {
		t.Fatalf("child SaveHeader 失败: %v", err)
	}

	// Both sessions must load with the correct parent/child relationship.
	_, parentEvents, err := backend.Load(ctx, parent.ID)
	if err != nil {
		t.Fatalf("parent Load 失败: %v", err)
	}
	childHeader, childEvents, err := backend.Load(ctx, child.ID)
	if err != nil {
		t.Fatalf("child Load 失败: %v", err)
	}

	if len(parentEvents) != 0 || len(childEvents) != 0 {
		t.Fatalf("空会话事件数应为 0: parent=%d child=%d", len(parentEvents), len(childEvents))
	}
	if childHeader.ParentSession.Raw() != "parent" {
		t.Fatalf("child.ParentSession = %q, want parent", childHeader.ParentSession.Raw())
	}

	// List must include both sessions.
	ids, err := backend.List(ctx)
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("List 应含 2 个会话: %v", ids)
	}
}

// TestPersistenceFlushCheckpoint verifies flushed data can be reloaded from disk.
func TestPersistenceFlushCheckpoint(t *testing.T) {
	dir := t.TempDir()
	backend, err := persistence.NewJSONL(dir, 100)
	if err != nil {
		t.Fatalf("NewJSONL 失败: %v", err)
	}
	ctx := context.Background()

	id := brand.NewSessionID("flush_s")
	header := session.NewSessionHeader(id, "/ws")
	_ = backend.SaveHeader(ctx, header)

	// Append a few events below the 100-entry batch threshold.
	_ = backend.Append(ctx, id, session.SessionEvent{Seq: 1, Type: session.EventTurnStart, Data: session.TurnStartData{Turn: 0}})
	_ = backend.Append(ctx, id, session.SessionEvent{Seq: 2, Type: session.EventTurnEnd, Data: session.TurnEndData{Turn: 0, Reason: session.ReasonFinished}})

	// A new backend must not see unflushed events.
	backendProbe := newJSONLNoCheck(t, dir)
	_, before, _ := backendProbe.Load(ctx, id)
	if len(before) != 0 {
		t.Fatalf("flush 前文件不应有事件, got %d", len(before))
	}

	// flush checkpoint
	if err := backend.Flush(ctx, id); err != nil {
		t.Fatalf("Flush 失败: %v", err)
	}

	// After flushing, reloading must return two events.
	_, after, err := backend.Load(ctx, id)
	if err != nil {
		t.Fatalf("reload 失败: %v", err)
	}
	if len(after) != 2 {
		t.Fatalf("flush 后应有 2 条事件, got %d", len(after))
	}
}

// newJSONLNoCheck constructs a backend while discarding the error.
func newJSONLNoCheck(t *testing.T, dir string) *persistence.JSONLBackend {
	t.Helper()
	b, err := persistence.NewJSONL(dir, 1)
	if err != nil {
		t.Fatalf("NewJSONL 失败: %v", err)
	}
	return b
}

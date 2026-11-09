// M08: Agent registry and nested turn/step loop.
package tests

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/herlanai/omnix/pkg/agent"
	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/llm"
	"github.com/herlanai/omnix/pkg/session"
	"github.com/herlanai/omnix/pkg/sysprompt"
	"github.com/herlanai/omnix/pkg/tools"
)

// scriptedAdapter is a fake LLM adapter that emits scripted chunks in order.
type scriptedAdapter struct {
	mu       sync.Mutex
	scripts  [][]llm.StreamChunk // Consume one script per Chat call.
	order    []string            // Record Chat inputs to verify serial execution.
	callIdx  int                 // Chat call number.
	blockOn  chan struct{}       // When non-nil, block the first Chat call until release.
	released bool
	failOn   int // Return an error on the Nth call (one-based).
}

func newScriptedAdapter() *scriptedAdapter {
	return &scriptedAdapter{}
}

// blockFirst blocks the first Chat call for serial queue tests.
func (s *scriptedAdapter) blockFirst() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.blockOn == nil {
		s.blockOn = make(chan struct{})
	}
}

// script appends a scripted response.
func (s *scriptedAdapter) script(chunks ...llm.StreamChunk) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scripts = append(s.scripts, chunks)
}

func (s *scriptedAdapter) Name() string { return "scripted" }

func (s *scriptedAdapter) release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.released {
		s.released = true
		close(s.blockOn)
	}
}

func (s *scriptedAdapter) Chat(ctx context.Context, req llm.ChatRequest, cb func(llm.StreamChunk)) (llm.Usage, error) {
	s.mu.Lock()
	// Record the final user message as the input.
	lastUser := ""
	for _, m := range req.Messages {
		if m.Role == llm.RoleUser {
			for _, b := range m.Content {
				if b.Kind == llm.BlockText {
					lastUser = b.Text
				}
			}
		}
	}
	s.order = append(s.order, lastUser)
	s.callIdx++
	idx := s.callIdx
	block := s.blockOn
	failOn := s.failOn
	chunks := []llm.StreamChunk{}
	if idx <= len(s.scripts) {
		chunks = s.scripts[idx-1]
	}
	s.mu.Unlock()

	// Block the first call to verify serial execution.
	if block != nil && idx == 1 {
		select {
		case <-block:
		case <-ctx.Done():
			return llm.Usage{}, ctx.Err()
		}
	}

	// Inject a failure.
	if failOn != 0 && idx == failOn {
		return llm.Usage{}, errors.New("injected llm failure")
	}

	for _, c := range chunks {
		if cb != nil {
			cb(c)
		}
	}
	if cb != nil {
		cb(llm.StreamChunk{Kind: llm.ChunkDone})
	}
	return llm.Usage{PromptTokens: 1, CompletionTokens: 1}, nil
}

func (s *scriptedAdapter) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.callIdx
}

// buildAgent constructs an Agent with an echo tool.
func buildAgent(t *testing.T, adapter *scriptedAdapter) *agent.Agent {
	t.Helper()
	sl := session.NewSessionLog(brand.NewSessionID("agent_1"))
	sys := sysprompt.New()

	// Echo tool.
	echo := &tools.Tool{
		Name:        "echo",
		Description: "echo input",
		Execute: func(ctx context.Context, input map[string]any) (any, error) {
			return map[string]any{"echo": input["msg"]}, nil
		},
	}
	pipeline := tools.NewPipeline().WithTool(echo)

	a := agent.NewAgent(brand.NewSessionID("agent_1"), sl, sys, pipeline, adapter)
	a.SetToolProvider(func(name string) *tools.Tool {
		if name == "echo" {
			return echo
		}
		return nil
	})
	return a
}

// waitFor polls until the condition holds or the timeout expires.
func waitFor(cond func() bool) bool {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// countTurnEnds counts turn/end events in the log.
func countTurnEnds(sl *session.SessionLog) int {
	n := 0
	for _, ev := range sl.Events() {
		if ev.Type == session.EventTurnEnd {
			n++
		}
	}
	return n
}

// lastEventOfType returns the last event of the specified type.
func lastEventOfType(sl *session.SessionLog, typ session.EventType) (session.SessionEvent, bool) {
	events := sl.Events()
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == typ {
			return events[i], true
		}
	}
	return session.SessionEvent{}, false
}

// TestAgentTurnStepTextOnly verifies a text-only turn: turn/start, step, and turn/end with reason
// finished.
func TestAgentTurnStepTextOnly(t *testing.T) {
	adapter := newScriptedAdapter()
	adapter.script(llm.StreamChunk{Kind: llm.ChunkText, Text: "你好"})
	a := buildAgent(t, adapter)

	if err := a.Run("hello"); err != nil {
		t.Fatalf("Run 失败: %v", err)
	}
	if !waitFor(func() bool { return countTurnEnds(a.Log()) == 1 }) {
		t.Fatal("turn 未在超时内完成")
	}

	// The turn/end reason must be finished.
	ev, ok := lastEventOfType(a.Log(), session.EventTurnEnd)
	if !ok {
		t.Fatal("缺少 turn/end")
	}
	if td, ok := ev.Data.(session.TurnEndData); !ok || td.Reason != session.ReasonFinished {
		t.Fatalf("turn/end 应为 finished: %+v", ev.Data)
	}
}

// TestAgentTurnWithTool verifies tool execution in step 1 followed by step 2 and turn/end.
func TestAgentTurnWithTool(t *testing.T) {
	adapter := newScriptedAdapter()
	// Step 1 requests the echo tool.
	adapter.script(llm.StreamChunk{Kind: llm.ChunkToolCall, ToolCall: &llm.ToolCall{ID: "call_1", Name: "echo", Input: map[string]any{"msg": "hi"}}})
	// Step 2 returns the final text.
	adapter.script(llm.StreamChunk{Kind: llm.ChunkText, Text: "done"})
	a := buildAgent(t, adapter)

	if err := a.Run("use echo"); err != nil {
		t.Fatalf("Run 失败: %v", err)
	}
	if !waitFor(func() bool { return countTurnEnds(a.Log()) == 1 }) {
		t.Fatal("turn 未在超时内完成")
	}

	// Both tool/call and tool/result must be present.
	hasCall, hasResult := false, false
	for _, ev := range a.Log().Events() {
		switch ev.Type {
		case session.EventToolCall:
			hasCall = true
		case session.EventToolResult:
			hasResult = true
		}
	}
	if !hasCall || !hasResult {
		t.Fatalf("应存在 tool/call 与 tool/result: call=%v result=%v", hasCall, hasResult)
	}
	// Two Chat calls: step 1 and step 2.
	if adapter.callCount() != 2 {
		t.Fatalf("Chat 调用次数 = %d, want 2", adapter.callCount())
	}
}

// TestAgentFollowupSerialQueue queues two follow-ups while running and rejects a third.
func TestAgentFollowupSerialQueue(t *testing.T) {
	adapter := newScriptedAdapter()
	adapter.blockFirst() // Block the first call to observe the running state.
	// Prepare text scripts for three turns.
	adapter.script(llm.StreamChunk{Kind: llm.ChunkText, Text: "r1"})
	adapter.script(llm.StreamChunk{Kind: llm.ChunkText, Text: "r2"})
	adapter.script(llm.StreamChunk{Kind: llm.ChunkText, Text: "r3"})
	a := buildAgent(t, adapter)

	// The first turn blocks so its running state can be inspected.
	if err := a.Run("first"); err != nil {
		t.Fatalf("Run 失败: %v", err)
	}
	// Wait for the worker to start and block inside Chat.
	if !waitFor(func() bool { return adapter.callCount() >= 1 }) {
		t.Fatal("首个 Chat 未开始")
	}

	// Queue two follow-ups while running; queue capacity is two.
	if err := a.Followup("f1"); err != nil {
		t.Fatalf("Followup f1 应排队成功: %v", err)
	}
	if err := a.Followup("f2"); err != nil {
		t.Fatalf("Followup f2 应排队成功: %v", err)
	}
	// Reject the third follow-up because the queue is full.
	if err := a.Followup("f3"); err != nil {
		if err != agent.ErrTurnQueueFull {
			t.Fatalf("第 3 条应 reject 为 ErrTurnQueueFull, 实际 %v", err)
		}
	} else {
		t.Fatal("第 3 条 Followup 应被拒绝")
	}

	// Release the first turn and execute all three serially.
	adapter.release()
	if !waitFor(func() bool { return countTurnEnds(a.Log()) == 3 }) {
		t.Fatal("3 个 turn 未在超时内完成")
	}

	// Verify serial ordering.
	adapter.mu.Lock()
	order := append([]string(nil), adapter.order...)
	adapter.mu.Unlock()
	if len(order) != 3 || order[0] != "first" || order[1] != "f1" || order[2] != "f2" {
		t.Fatalf("串行顺序异常: %v", order)
	}
}

// TestAgentStepErrorInterruptsTurn verifies agent/error and an interrupted turn after a step
// failure.
func TestAgentStepErrorInterruptsTurn(t *testing.T) {
	adapter := newScriptedAdapter()
	adapter.failOn = 1 // Fail the first Chat call.
	a := buildAgent(t, adapter)

	if err := a.Run("trigger error"); err != nil {
		t.Fatalf("Run 入队失败: %v", err)
	}
	if !waitFor(func() bool { return countTurnEnds(a.Log()) == 1 }) {
		t.Fatal("turn 未在超时内完成")
	}

	// An agent/error event must exist.
	if _, ok := lastEventOfType(a.Log(), session.EventAgentError); !ok {
		t.Fatal("缺少 agent/error 事件")
	}
	// The turn/end reason must be interrupted.
	ev, _ := lastEventOfType(a.Log(), session.EventTurnEnd)
	if td, ok := ev.Data.(session.TurnEndData); !ok || td.Reason != session.ReasonInterrupted {
		t.Fatalf("turn/end 应为 interrupted: %+v", ev.Data)
	}
}

// TestAgentDispose verifies that Dispose stops the worker.
func TestAgentDispose(t *testing.T) {
	adapter := newScriptedAdapter()
	adapter.script(llm.StreamChunk{Kind: llm.ChunkText, Text: "x"})
	a := buildAgent(t, adapter)
	_ = a.Run("x")
	if !waitFor(func() bool { return countTurnEnds(a.Log()) == 1 }) {
		t.Fatal("turn 未完成")
	}
	a.Dispose()
}

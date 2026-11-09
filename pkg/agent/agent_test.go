package agent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/llm"
	"github.com/herlanai/omnix/pkg/session"
	"github.com/herlanai/omnix/pkg/tools"
)

type adapterFunc func(context.Context, llm.ChatRequest, func(llm.StreamChunk)) (llm.Usage, error)

func (adapterFunc) Name() string { return "test" }
func (f adapterFunc) Chat(ctx context.Context, req llm.ChatRequest, cb func(llm.StreamChunk)) (llm.Usage, error) {
	return f(ctx, req, cb)
}
func testAgent(adapter llm.LLMAdapter, pipeline *tools.Pipeline, opts ...AgentOptions) *Agent {
	id := brand.NewSessionID("test")
	return NewAgent(id, session.NewSessionLog(id), nil, pipeline, adapter, opts...)
}
func awaitIdle(t *testing.T, a *Agent) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := a.WhenIdle(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestToolConversationAndReplay(t *testing.T) {
	var requests []llm.ChatRequest
	calls := 0
	pipeline := tools.NewPipeline().WithTool(&tools.Tool{Name: "lookup", Execute: func(context.Context, map[string]any) (any, error) { calls++; return "answer", nil }})
	adapter := adapterFunc(func(ctx context.Context, req llm.ChatRequest, cb func(llm.StreamChunk)) (llm.Usage, error) {
		requests = append(requests, req)
		if len(requests) == 1 {
			cb(llm.StreamChunk{Kind: llm.ChunkText, Text: "checking"})
			cb(llm.StreamChunk{Kind: llm.ChunkToolCall, ToolCall: &llm.ToolCall{ID: "c1", Name: "lookup", Input: map[string]any{}}})
		} else {
			cb(llm.StreamChunk{Kind: llm.ChunkText, Text: "done"})
		}
		return llm.Usage{}, nil
	})
	a := testAgent(adapter, pipeline, AgentOptions{Model: "selected-model", MaxTokens: 123, ReasoningEffort: "high"})
	defer a.Dispose()
	if err := a.Run("question"); err != nil {
		t.Fatal(err)
	}
	awaitIdle(t, a)
	if len(requests) != 2 || calls != 1 {
		t.Fatalf("requests=%d calls=%d", len(requests), calls)
	}
	r := requests[1]
	if r.Model != "selected-model" || r.MaxTokens != 123 || r.ReasoningEffort != "high" || len(r.Tools) != 1 || r.Tools[0].Name != "lookup" {
		t.Fatalf("request: %+v", r)
	}
	if len(r.Messages) != 3 || r.Messages[1].Content[1].ToolCall.ID != "c1" || r.Messages[2].Content[0].ToolResult.Content != "answer" {
		t.Fatalf("history: %+v", r.Messages)
	}
	if got := session.DeriveMessages(a.Log().Events()); len(got) != 3 || got[2].Content != "done" {
		t.Fatalf("durable messages: %+v", got)
	}
	if failures := session.VerifyInvariants(a.Log().Events()); len(failures) > 0 {
		t.Fatal(failures)
	}
	// A new driver over the same log sees previous text, calls, and results.
	resumed := NewAgent(a.ID, a.Log(), nil, pipeline, adapter)
	defer resumed.Dispose()
	if err := resumed.Run("followup"); err != nil {
		t.Fatal(err)
	}
	awaitIdle(t, resumed)
	if len(requests[2].Messages) != 5 {
		t.Fatalf("resume lost history: %+v", requests[2].Messages)
	}
}

func TestCancelAndDispose(t *testing.T) {
	started := make(chan struct{})
	a := testAgent(adapterFunc(func(ctx context.Context, _ llm.ChatRequest, cb func(llm.StreamChunk)) (llm.Usage, error) {
		close(started)
		cb(llm.StreamChunk{Kind: llm.ChunkText, Text: "partial"})
		<-ctx.Done()
		return llm.Usage{}, ctx.Err()
	}), nil)
	if err := a.Run("block"); err != nil {
		t.Fatal(err)
	}
	<-started
	if err := a.Followup("discard"); err != nil {
		t.Fatal(err)
	}
	a.Cancel(CancelUser)
	awaitIdle(t, a)
	msgs := session.DeriveMessages(a.Log().Events())
	if len(msgs) != 2 || msgs[1].Content != "partial" {
		t.Fatalf("messages: %+v", msgs)
	}
	var reason session.TurnEndReason
	for _, ev := range a.Log().Events() {
		if d, ok := ev.Data.(session.TurnEndData); ok {
			reason = d.Reason
		}
	}
	if reason != session.ReasonAborted {
		t.Fatalf("reason=%s", reason)
	}
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); a.Dispose() }()
	}
	wg.Wait()
	if err := a.Run("after close"); !errors.Is(err, ErrDisposed) {
		t.Fatalf("Run=%v", err)
	}
}

func TestDefaultTimeoutAndNilAdapter(t *testing.T) {
	for _, adapter := range []llm.LLMAdapter{nil, adapterFunc(func(ctx context.Context, _ llm.ChatRequest, _ func(llm.StreamChunk)) (llm.Usage, error) {
		<-ctx.Done()
		return llm.Usage{}, ctx.Err()
	})} {
		a := testAgent(adapter, nil, AgentOptions{DefaultTimeout: 10 * time.Millisecond})
		if err := a.Run("x"); err != nil {
			t.Fatal(err)
		}
		awaitIdle(t, a)
		a.Dispose()
		if failures := session.VerifyInvariants(a.Log().Events()); len(failures) > 0 {
			t.Fatal(failures)
		}
	}
}

func TestWhenIdleIncludesQueuedWork(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	a := testAgent(adapterFunc(func(ctx context.Context, _ llm.ChatRequest, _ func(llm.StreamChunk)) (llm.Usage, error) {
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return llm.Usage{}, ctx.Err()
	}), nil)
	defer a.Dispose()
	_ = a.Run("one")
	<-started
	_ = a.Followup("two")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := a.WhenIdle(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WhenIdle=%v", err)
	}
	close(release)
	awaitIdle(t, a)
	if len(started) != 1 {
		t.Fatal("queued turn was not completed")
	}
}

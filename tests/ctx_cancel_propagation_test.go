// H01 context propagation tests verify parent cancellation reaches pipeline tools and that
// Agent.SetRunContext supplies the bound context to tool and LLM calls instead of
// context.Background.
package tests

import (
	"context"
	"testing"
	"time"

	"github.com/herlanai/omnix/pkg/agent"
	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/llm"
	"github.com/herlanai/omnix/pkg/session"
	"github.com/herlanai/omnix/pkg/tools"
)

// TestH01PipelinePropagatesCancel propagates parent cancellation into tool execution.
func TestH01PipelinePropagatesCancel(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()

	var observed bool
	tool := &tools.Tool{
		Name: "probe", Description: "probe",
		Execute: func(ctx context.Context, input map[string]any) (any, error) {
			select {
			case <-ctx.Done():
				observed = true
			case <-time.After(2 * time.Second):
			}
			return "ok", nil
		},
	}
	pipeline := tools.NewPipeline().WithTool(tool)

	// Before parent cancellation, the tool context must remain active.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel() // Cancel the parent.
	}()
	res := pipeline.Run(parent, &tools.ToolCallRequest{
		CallID: brand.NewToolCallID("h1-1"), Tool: "probe", Input: map[string]any{},
	}, tool)
	if !observed {
		t.Fatal("父取消后工具 ctx 应观测到 Done（取消未传播进工具实现）")
	}
	if res.IsError {
		t.Fatalf("工具不应报错，仅应透传取消信号：%s", res.Error)
	}
}

// TestH01AgentPathCancellation verifies tools invoked through Agent.executeTool receive the
// SetRunContext context and observe parent cancellation.
func TestH01AgentPathCancellation(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()

	sid := brand.NewSessionID("h1-agent")
	pipeline := tools.NewPipeline()
	started := make(chan struct{}, 1)
	canceled := make(chan struct{}, 1)
	tool := &tools.Tool{
		Name: "probe", Description: "probe",
		Execute: func(ctx context.Context, _ map[string]any) (any, error) {
			select {
			case started <- struct{}{}:
			default:
			}
			select {
			case <-ctx.Done():
				select {
				case canceled <- struct{}{}:
				default:
				}
			case <-time.After(2 * time.Second):
			}
			return "ok", nil
		},
	}
	pipeline.WithTool(tool)

	adapter := &toolCallOnceLLM{toolName: "probe"}
	a := agent.NewAgent(sid, session.NewSessionLog(sid), nil, pipeline, adapter)
	defer a.Dispose()
	a.SetToolProvider(func(name string) *tools.Tool { return tool })
	a.SetRunContext(parent, 0) // Bind the cancellable upstream context.
	if err := a.Run("do it"); err != nil {
		t.Fatal(err)
	}

	// Wait for execution before canceling so the test exercises the tool path.
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("tool execution did not start")
	}
	cancel()
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("tool did not observe parent cancellation")
	}

}

// toolCallOnceLLM emits one tool call and then completes.
type toolCallOnceLLM struct {
	toolName string
}

func (m *toolCallOnceLLM) Name() string { return "toolonce" }

func (m *toolCallOnceLLM) Chat(ctx context.Context, req llm.ChatRequest, cb func(llm.StreamChunk)) (llm.Usage, error) {
	if cb != nil {
		cb(llm.StreamChunk{Kind: llm.ChunkToolCall, ToolCall: &llm.ToolCall{ID: "call_1", Name: m.toolName, Input: map[string]any{}}})
		cb(llm.StreamChunk{Kind: llm.ChunkDone})
	}
	return llm.Usage{}, nil
}

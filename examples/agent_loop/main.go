// This example demonstrates the in-process omnix Agent turn/step loop. NewAgent assembles the
// session log, system prompt, tool pipeline, and LLM adapter. Run(input) starts a turn; each step
// builds a prompt, streams LLM chunks, and executes any tool calls before continuing or ending the
// turn. Run: go run ./examples/agent_loop. The scripted adapter makes no network requests; replace
// it with provider_deepseek.NewDeepSeek(store) for a real model (see examples/usage).
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/herlanai/omnix/pkg/agent"
	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/llm"
	"github.com/herlanai/omnix/pkg/session"
	"github.com/herlanai/omnix/pkg/sysprompt"
	"github.com/herlanai/omnix/pkg/tools"
)

// scriptedLLM emits scripted responses in call order, including tool calls followed by text. It
// makes no network requests, keeping the turn/step demonstration reproducible.
type scriptedLLM struct {
	replies []func(cb func(llm.StreamChunk)) // Consume one script per Chat call.
	callN   int
}

func (d *scriptedLLM) Name() string { return "scripted-demo" }

func (d *scriptedLLM) Chat(ctx context.Context, req llm.ChatRequest, cb func(llm.StreamChunk)) (llm.Usage, error) {
	d.callN++
	if d.callN <= len(d.replies) {
		d.replies[d.callN-1](cb)
	}
	// Emit a final done chunk so the agent can finish the message and turn.
	cb(llm.StreamChunk{Kind: llm.ChunkDone})
	return llm.Usage{PromptTokens: int(len(req.Messages)), CompletionTokens: 2}, nil
}

func main() {
	ctx := context.Background()

	// 1. Create the event-sourced session log for turn, step, assistant, and tool events.
	sl := session.NewSessionLog(brand.NewSessionID("agent-loop-demo"))

	// 2. Create the system prompt assembler with a persona section.
	sys := sysprompt.New()
	sys.Register("persona", 100, "你是一个使用 omnix 的演示 Agent。")

	// 3. Create a tool pipeline with a demonstration tool: now returns the current time and triggers
	// another step.
	p := tools.NewPipeline().WithTool(&tools.Tool{
		Name:        "now",
		Description: "返回当前时间",
		Execute: func(ctx context.Context, input map[string]any) (any, error) {
			return map[string]any{"time": time.Now().Format(time.RFC3339)}, nil
		},
	})

	// 4. The scripted LLM first calls now, then returns final text, demonstrating tool execution and
	// continuation within one turn.
	adapter := &scriptedLLM{
		replies: []func(cb func(llm.StreamChunk)){
			func(cb func(llm.StreamChunk)) {
				cb(llm.StreamChunk{Kind: llm.ChunkToolCall, ToolCall: &llm.ToolCall{
					ID: "call-1", Name: "now", Input: map[string]any{},
				}})
			},
			func(cb func(llm.StreamChunk)) {
				cb(llm.StreamChunk{Kind: llm.ChunkText, Text: "当前时间已获取。"})
			},
		},
	}

	// 5. Pipeline registrations provide both model schemas and execution lookup.
	a := agent.NewAgent(brand.NewSessionID("agent-loop-demo"), sl, sys, p, adapter)
	defer a.Dispose()

	// 6. Submit a turn asynchronously; a cancellable parent context may be supplied (H01).
	a.SetRunContext(ctx, 30*time.Second)
	if err := a.Run("现在几点？"); err != nil {
		fmt.Printf("Run 失败: %v\n", err)
		return
	}

	// 7. Wait for all accepted turns, including tool continuation steps.
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := a.WhenIdle(waitCtx); err != nil {
		fmt.Printf("等待失败: %v\n", err)
		return
	}

	// 9. Print the event stream to show the nested turn/step structure.
	printEvents(sl)
	fmt.Println("\nAgent Turn/Step 循环示例完成。")
}

// printEvents prints the session event sequence to expose the nested loops.
func printEvents(sl *session.SessionLog) {
	seq := 0
	for _, ev := range sl.Events() {
		seq++
		_ = seq
		fmt.Printf("  #%d %s\n", ev.Seq, ev.Type)
	}
}

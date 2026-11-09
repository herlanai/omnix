# Running an agent

[Documentation](README.md) · [简体中文](../zh-CN/agent.md) · [Tools](tools.md)

An `Agent` runs model requests and tool calls inside your Go process. Its constructor takes a session log, an adapter, and optional prompt/tool components. The log is in memory; attaching storage is a separate host responsibility.

## Start with an offline adapter

Use the Go version in [`go.mod`](../../go.mod). Save this complete program as `/tmp/omnix-agent-guide.go`, then run `go run /tmp/omnix-agent-guide.go` from the repository root. It needs no credentials or network connection.

```go
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/herlanai/omnix/pkg/agent"
	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/llm"
	"github.com/herlanai/omnix/pkg/session"
)

type offlineAdapter struct{}

func (offlineAdapter) Name() string { return "offline" }

func (offlineAdapter) Chat(ctx context.Context, req llm.ChatRequest, emit func(llm.StreamChunk)) (llm.Usage, error) {
	if err := ctx.Err(); err != nil {
		return llm.Usage{}, err
	}
	emit(llm.StreamChunk{Kind: llm.ChunkText, Text: "Hello from the offline adapter."})
	emit(llm.StreamChunk{Kind: llm.ChunkDone})
	return llm.Usage{}, nil
}

func main() {
	id := brand.NewSessionID("offline-guide")
	log := session.NewSessionLog(id)
	a := agent.NewAgent(id, log, nil, nil, offlineAdapter{}, agent.AgentOptions{
		DefaultTimeout: 5 * time.Second,
	})
	defer a.Dispose()

	if err := a.Run("Hello"); err != nil {
		panic(err)
	}
	waitCtx, stopWaiting := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopWaiting()
	if err := a.WhenIdle(waitCtx); err != nil {
		a.Cancel(agent.CancelUser)
		panic(err)
	}

	// Idle is a lifecycle state, not a successful execution result.
	for _, event := range log.Events() {
		switch data := event.Data.(type) {
		case session.AssistantMessageData:
			fmt.Println(data.Content)
		case session.AgentErrorData:
			fmt.Printf("execution error: %s\n", data.Message)
		case session.TurnEndData:
			fmt.Printf("turn %d: %s\n", data.Turn, data.Reason)
		}
	}
}
```

The output is `Hello from the offline adapter.` followed by `turn 0: finished`. The current loop uses `ReasonFinished`, whose value is `finished`; the separately defined `ReasonCompleted` has a different value.

## Build the application boundary

[`NewAgent`](../../pkg/agent/agent.go) receives `(id, log, prompt, pipeline, adapter, options...)`. Pass a valid log and the same session ID used to create it. The prompt assembler and pipeline may be nil. A nil adapter is accepted at construction but fails when a step executes.

The [`LLMAdapter`](../../pkg/llm/llm.go) interface has two methods: `Name` and `Chat`. `Chat` receives a request, emits ordered chunks through a callback, and returns usage or an error. Deliver callbacks sequentially and stop calling them before returning. Implement cancellation using the supplied context.

The built-in network adapter is [`provider_deepseek`](../../pkg/llm/provider_deepseek/deepseek.go). [`examples/chat`](../../examples/chat/main.go) demonstrates credentials and direct provider requests. For another service, implement `LLMAdapter` and inject it yourself. Setting `AgentOptions.Provider` does not switch adapters.

`Model`, `MaxTokens`, and `ReasoningEffort` populate requests; an empty model currently defaults to `deepseek-chat`. A [tool pipeline](tools.md) supplies model-visible tool schemas and execution lookup. Finish configuration before submitting work; do not mutate the pipeline or tool definitions during execution.

## Submit and wait

`Run` starts the worker lazily and queues one turn. `Followup` queues another turn; it does not inject input into an active model stream. The worker processes turns serially, and each turn may contain multiple model/tool steps.

A successful submission means accepted, not completed. Handle `agent.ErrTurnQueueFull` and `agent.ErrDisposed` with `errors.Is`. The queue is bounded, but concurrent draining means callers should not rely on a fixed number of accepted submissions.

Call `WhenIdle(ctx)` after successful submission. It waits for accepted work in the current busy period; later submissions can start a new busy period. Its return value only describes waiting, not whether the model or tools succeeded.

## Control execution lifetime

| Method | Behavior |
|---|---|
| `SetRunContext(parent, timeout)` | Supplies the parent and timeout used when a turn starts. Nonpositive timeout falls back to `DefaultTimeout`; with both zero, only parent cancellation applies. |
| `Cancel(cause)` | Cancels the active execution and discards queued requests. |
| `CancelWithOptions(cause, agent.CancelOptions{KeepInbox: true})` | Cancels active execution while preserving queued requests. |
| `Dispose()` | Closes the agent permanently, cancels work, and waits for the worker. Repeated and concurrent calls are supported. |

A timeout of the context passed to **WhenIdle** only stops waiting. To stop execution, call `Cancel` or cancel the parent supplied through `SetRunContext`. A canceled parent also affects later turns using that same parent.

Cancellation is cooperative. A tool or adapter that ignores its context can delay `Dispose` indefinitely. Do not call `Dispose` synchronously from the agent's own tool callback: it waits for the worker that is currently executing that callback.

## Read results and failures

Read `log.Events()` after waiting, and inspect concrete event payloads:

- `AssistantMessageData` contains accumulated assistant text and reasoning; partial text received before cancellation is retained.
- `ToolResultData` contains `IsError` and output text. Tool failure normally becomes input for another model step rather than immediately failing the whole turn.
- `AgentErrorData` records execution failures. `TurnEndData.Reason` is currently `finished` for normal completion, `aborted` for cancellation/deadline errors, and `interrupted` for other execution errors.

When calling an adapter directly, `Chat` returns its error. Use `errors.As` to inspect `*llm.LlmFailure` and `errors.Is` for wrapped context errors. The agent's persisted error event is text, not the original Go error object.

Reusing the same log lets a new driver see prior conversation events, including tool calls and results. Do not run two drivers concurrently against one log. A loaded event slice is not automatically a runnable `SessionLog`; storage, restoration, checkpoints, and restart orchestration belong to the host. Treat returned event payloads as read-only.

The constructor does not automatically install retries, provider routing, compaction, goal scheduling, or a full Inbox steering driver. Compose those components explicitly if your application needs them.

## Verify the contract

See the [agent implementation](../../pkg/agent/agent.go), [lifecycle and replay tests](../../pkg/agent/agent_test.go), and [offline tool-loop example](../../examples/agent_loop/main.go).

```sh
go test ./pkg/agent
go run ./examples/agent_loop
```

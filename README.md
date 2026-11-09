# omnix

**An embeddable Go runtime for stateful, tool-using AI agents.**

[English](README.md) · [简体中文](README.zh-CN.md)

omnix brings the agent loop, tool execution, and replayable session state into your Go process. It combines an append-only event log with derived projections, a four-stage tool pipeline, and a streaming DeepSeek adapter. Planning, persistence, subagents, and workflows are available as separate packages.

The project is under active development. Start with the local examples, then assemble the components your application needs.

## What you can build

- **Agents with inspectable history.** Record turns, model responses, tool calls, and results as session events; derive current state or replay a stored conversation.
- **Tools with execution policies.** Validate arguments and apply approval decisions, restrictions, timeouts, and result processing through middleware.
- **Stateful planning.** Track goals, todos, and plan mode alongside the conversation; expose selected planning tools to the model.
- **Go application integrations.** Supply an LLM adapter, register tools, choose persistence, and compose subagents or workflows within your application.

## Quick start

Requires **Go 1.25 or later**. SQLite support uses the pure-Go `modernc.org/sqlite` driver.

```bash
git clone https://github.com/herlanai/omnix.git
cd omnix
go run ./examples/agent_loop
go run ./examples/tutorial
```

These two examples run without a model API key. `agent_loop` uses a scripted adapter to demonstrate a complete tool-call cycle. The tutorial covers sessions and projections; its goal-tool section currently prints `GOAL_NOT_FOUND` because no goal has been created.

To add omnix to an existing Go module:

```bash
go get github.com/herlanai/omnix
```

### Create a session

Session events are the source of truth. Enable incremental projections when reading state repeatedly as new events arrive.

```go
package main

import (
	"fmt"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/session"
)

func main() {
	log := session.NewSessionLog(brand.NewSessionID("hello"))
	log.EnableIncrementalProjection()

	if _, err := log.Append(session.UserMessageData{Content: "Hello, omnix!"}); err != nil {
		panic(err)
	}

	for _, message := range log.Projection().Messages {
		fmt.Printf("%s: %s\n", message.Role, message.Content)
	}
}
```

Output:

```text
user: Hello, omnix!
```

### Run an agent

The [agent-loop example](examples/agent_loop/main.go) shows the complete wiring: a `SessionLog`, system prompt, tool `Pipeline`, and `llm.LLMAdapter` passed to `agent.NewAgent`.

The execution lifecycle is asynchronous:

1. Configure the adapter, tools, and middleware before starting the agent.
2. Set the execution context with `SetRunContext`.
3. Submit input with `Run` or `Followup`; their return value reports whether the input was accepted.
4. Use `WhenIdle(ctx)` to wait for accepted work to finish. Inspect session events for model/tool errors and turn completion reasons.
5. Call `Cancel` to stop active work, and `Dispose` when the agent is no longer needed.

A timeout on `WhenIdle` stops waiting. Execution cancellation is controlled by the run context or `Cancel`. Tool implementations must honor their context.

### Try the DeepSeek adapter

The chat example makes live API requests and requires your own credentials:

```bash
export DEEPSEEK_API_KEY="your-api-key"
export DEEPSEEK_MODEL="deepseek-chat"
go run ./examples/chat
```

`DEEPSEEK_MODEL` is optional; the example defaults to `deepseek-chat`. This example demonstrates streaming model access directly. Use the agent-loop example to learn tool dispatch and session recording.

## How it fits together

```text
Application input
       |
       v
Agent turn / step loop <----> LLM adapter
       |                         |
       |                    Tool requests
       |                         v
       |        pre -> execute -> post -> result
       |                         |
       +------ session events ---+
                    |
                    v
             Append-only log
              /           \
       Projections     Persistence
```

A turn can contain several model/tool steps. Events preserve what happened, projections expose the current view, and persistence stores history for later loading and replay. Surface replacements let compaction change the visible history while retaining source events.

## Packages

| Area | Packages | Purpose |
| --- | --- | --- |
| Runtime | [`agent`](pkg/agent), [`session`](pkg/session), [`eventbus`](pkg/eventbus) | Execution lifecycle, event history, projections, notifications |
| Model access | [`llm`](pkg/llm), [`provider_deepseek`](pkg/llm/provider_deepseek) | Adapter contract, streaming, usage, failure classification |
| Tools | [`tools`](pkg/tools), [`approval`](pkg/approval), [`sandbox`](pkg/sandbox) | Schemas, middleware, access decisions, execution confinement |
| Planning | [`goal`](pkg/goal), [`todo`](pkg/todo), [`plan`](pkg/plan), [`skills`](pkg/skills) | Goal state, task lists, plan mode, skill discovery |
| Storage | [`persistence`](pkg/persistence), [`sqlite`](pkg/persistence/sqlite), [`storage`](pkg/storage) | JSONL, SQLite/FTS5, scoped storage |
| Composition | [`subagent`](pkg/subagent), [`workflow`](pkg/workflow), [`mcp`](pkg/mcp) | Child-agent interfaces, orchestration, MCP tool bridging |
| Local execution | [`fs`](pkg/fs), [`shell`](pkg/shell), [`subprocess`](pkg/subprocess), [`jobs`](pkg/jobs) | Files, commands, processes, background work |
| Observability | [`telemetry`](pkg/telemetry), [`tokenmeter`](pkg/tokenmeter) | Session metrics, token estimates, budgets |

## Examples

Run each command from the repository root.

| Command | Demonstrates |
| --- | --- |
| `go run ./examples/tutorial` | Events, projections, and goal-tool validation |
| `go run ./examples/agent_loop` | Model requests, tool execution, and turn completion with a scripted adapter |
| `go run ./examples/usage` | SDK components and JSONL persistence |
| `go run ./examples/todo` | Todo writes and derived state |
| `go run ./examples/workflow` | Sequential and parallel orchestration |
| `go run ./examples/subagent` | Child-agent backends and lifecycle |
| `go run ./examples/sandbox_approval` | Sandbox-mode and approval-policy resolution |
| `go run ./examples/mcp` | MCP tool bridging with an in-memory transport |
| `go run ./examples/chat` | Live DeepSeek streaming; requires an API key |

## Performance

Recent work reduces allocation in event dispatch, uses merged intervals for visible-history filtering, and removes reflection from incremental preset comparison. Local measurements below are medians of three runs on an Apple M5, macOS arm64, with Go 1.26.2.

| Microbenchmark | Before | After |
| --- | ---: | ---: |
| Dispatch to 16 listeners | 34.17 ns/op; 1 alloc/op | 13.28 ns/op; 0 allocs/op |
| Filter 10,000 events with a replacement range | 215.73 μs/op | 99.27 μs/op |
| Incrementally process 10,000 events | 1.010 ms/op; 21,683 allocs/op | 0.493 ms/op; 1,683 allocs/op |

Results vary with hardware and workload. Event-bus dispatch avoids allocation by copying the listener list during registration and removal, favoring workloads with frequent dispatch and infrequent listener changes.

Reproduce the current benchmarks:

```bash
go test ./pkg/eventbus ./pkg/session -run '^$' \
  -bench 'BenchmarkEmit|BenchmarkEffectiveEvents' -benchmem -count=3
go test ./tests -run '^$' \
  -bench '^BenchmarkIncrementalH04IncrementalPath$' -benchmem -count=3
go test ./tests -run '^$' -bench '^BenchmarkSQLiteSearch$' -benchmem
```

## Development

```bash
gofmt -w cmd examples internal pkg tests
go vet ./...
go test ./...
go test -race ./...
```

Tests include local HTTP/SSE mock servers; the test environment must allow loopback listeners. Some planned cases remain explicitly skipped placeholders. Keep code comments in English and add regression tests for behavior changes. Measure performance changes with benchmarks separately from functional assertions.

## Implementation scope

The built-in model adapter targets DeepSeek. Other providers implement `llm.LLMAdapter`; the adapter passed to the constructor handles requests, and `AgentOptions.Provider` does not provide runtime provider routing.

MCP network transports, complete inbox steering, and automatic orchestration of compaction and retry policies still require further integration. Sandbox enforcement varies by backend, and the Windows implementation contains incomplete enforcement paths. Review the relevant package before relying on it in a deployment.

See [SDK execution behavior and integration boundaries](docs/SDK_CORE.md) for details.

## Documentation

- [SDK execution guide](docs/SDK_CORE.md)
- [Design and task records](docs/TASKS.md)
- [Test-case specifications](docs/TEST_CASES.md)
- [Prefix-cache design notes](docs/CACHE_HIT_RATE_PLAN.md)

Design and task records include historical plans; consult implementation and tests for current behavior.

## Origins and license

omnix started from `dsh-go` and draws on the DeepSeek Harness design. This repository develops its own Go implementation and does not claim complete upstream feature parity.

Released under the [MIT License](LICENSE). The original copyright notice is preserved.

# Runtime architecture

[Guide index](README.md) · [简体中文](../zh-CN/architecture.md)

The runtime is assembled inside the host Go process. The host supplies a session log, an LLM adapter, a system-prompt assembler, and a tool pipeline to [`agent.NewAgent`](../../pkg/agent/agent.go). There is no daemon, HTTP server, database connection, or model-provider discovery started by that constructor.

## Request flow

```text
Run / Followup
      |
      v
Bounded turn queue -> single worker -> turn/start + user/message
                                           |
                                           v
                                  Assemble model request
                                           |
                                           v
                                      Adapter.Chat
                                           |
                               Record assistant output
                                           |
                                  Tool calls returned?
                                   /              \
                                 yes              no
                                  |                |
                            Tool pipeline      turn/end
                                  |
                       Record results; next step
```

[`runTurn` and `runStep`](../../pkg/agent/agent.go) implement this flow. The worker processes accepted turns sequentially. A step builds model messages from the session history, calls the adapter, collects streamed text/reasoning/tool calls, and then executes tool calls in their returned order. A tool-bearing step leads to another model request. Successful completion currently records the `finished` turn-end reason.

`Run` reports queue acceptance; `WhenIdle` waits for the current busy period. Neither returns a model answer. Read the event log for output and execution errors. The [agent guide](agent.md) covers lifecycle and cancellation.

## Events and projections

[`SessionEvent`](../../pkg/session/session.go) contains a sequence number, timestamp, event type, typed payload, and optional surface-operation metadata. `SessionLog.Append` checks the temporal invariants before accepting an event, including turn/step pairing and tool-call/result pairing.

The log is the state source. Projection functions derive messages, goals, todos, request headers, presets, and other views from it. The following program demonstrates the projection API without an agent or model:

```go
package main

import (
	"fmt"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/session"
)

func main() {
	log := session.NewSessionLog(brand.NewSessionID("projection-demo"))
	log.EnableIncrementalProjection()
	if _, err := log.Append(session.UserMessageData{Content: "Hello"}); err != nil {
		panic(err)
	}
	for _, message := range log.Projection().Messages {
		fmt.Printf("%s: %s\n", message.Role, message.Content)
	}
}
```

Output: `user: Hello`.

- `FoldAll(events)` reconstructs aggregate state from a supplied event slice.
- `EnableIncrementalProjection()` builds a baseline and updates it on later appends.
- `Projection()` returns the current aggregate view, copying the outer message slice when using incremental state.
- `ProjectionMeta()` omits messages, avoiding their copy on the incremental path.

Nested slices and pointers can still be shared. Treat returned event payloads and projection data as read-only; create your own copies before mutation. The standalone `IncrementalFolder` requires synchronization by its caller. `SessionLog` protects its own append and projection operations with a mutex.

Incremental projections optimize those projection APIs. The current agent model-request path still calls `log.Events()` and rebuilds model messages; enabling projections does not remove all history processing from the agent loop.

## Visible history and compaction

[`SurfaceOp`](../../pkg/session/surface.go) represents a read-time replacement of a sequence interval. [`EffectiveEvents`](../../pkg/session/fold.go) filters hidden events while preserving input order. The implementation merges replacement intervals and searches them instead of allocating one entry for every sequence value, including values absent from a sparse log.

This changes the visible conversation, not the original event slice. The [compaction package](../../pkg/compaction) provides separate compaction helpers; the agent constructor does not schedule or install automatic compaction. Applications decide when to compact, which context must survive, and how to persist the resulting events.

## Package ownership

| Layer | Main packages | Responsibility |
| --- | --- | --- |
| Execution | [`agent`](../../pkg/agent), [`llm`](../../pkg/llm), [`tools`](../../pkg/tools) | Model/tool cycle and execution contracts |
| State | [`session`](../../pkg/session), [`goal`](../../pkg/goal), [`todo`](../../pkg/todo), [`plan`](../../pkg/plan) | Event records and derived planning state |
| Prompt construction | [`sysprompt`](../../pkg/sysprompt), [`skills`](../../pkg/skills) | Sections, dynamic context, catalogs |
| Storage | [`persistence`](../../pkg/persistence), [`storage`](../../pkg/storage) | Explicit event persistence and scoped key-value storage |
| Host policy | [`approval`](../../pkg/approval), [`sandbox`](../../pkg/sandbox), [`credentials`](../../pkg/credentials) | Approval, execution confinement, credential access |
| Composition | [`workflow`](../../pkg/workflow), [`subagent`](../../pkg/subagent), [`mcp`](../../pkg/mcp) | Orchestration and pluggable integration interfaces |

A package being present does not activate it in the agent loop. For example, `mcp.Transport` is supplied by the host, persistence is explicitly wired, and approval middleware must be attached to the execution path. The adapter supplied to `NewAgent` handles model requests; `AgentOptions.Provider` does not implement dynamic provider routing.

## State and lifetime boundaries

Use a separate agent/log pair for independent conversations. Configure tool definitions before starting work and keep them stable while execution runs. The event bus uses immutable listener snapshots so callbacks run outside its lock; listener changes allocate new lists, favoring frequent dispatch over frequent registration.

The host owns shutdown: stop new submissions, cancel execution when necessary, dispose of the agent, and finish the persistence/exporter lifecycle. Context cancellation is cooperative and cannot terminate arbitrary Go callbacks. Storage details belong in the [persistence guide](persistence.md); policy wiring belongs in the [tool guide](tools.md).

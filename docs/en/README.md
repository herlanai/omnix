# omnix guides

[简体中文](../zh-CN/README.md) · [Project overview](../../README.md)

omnix is an in-process Go SDK for running agents around an event log. Your application supplies the model adapter, tools, persistence, and execution policy. Start with an offline example to understand the lifecycle before connecting a provider.

## Get started

Use Go 1.25 or newer, as declared in [go.mod](../../go.mod). To add the SDK to an existing Go module:

```sh
go get github.com/herlanai/omnix
```

For local development, run commands from the repository root:

```sh
go run ./examples/agent_loop
```

The example uses a local adapter and needs no API key. The [agent guide](agent.md) includes a standalone offline program and explains how to read results from the event log. Then use the [tool guide](tools.md) to register a tool and execute it through a pipeline.

## Guides

| Guide | What you will learn |
| --- | --- |
| [Runtime architecture](architecture.md) | Turn/step execution, events, projections, and component ownership. |
| [Running an agent](agent.md) | Construction, submissions, result handling, cancellation, and shutdown. |
| [Tools and execution policy](tools.md) | Schemas, registries, pipelines, context, approval, and sandbox boundaries. |
| [Persisting sessions](persistence.md) | JSONL checkpoints and recovery, SQLite writes/search, and loading projections. |
| [Usage and performance](observability.md) | Adapter usage capture, cache counters, telemetry wiring, and benchmarks. |
| [Development](development.md) | Repository layout, validation commands, regression tests, and contribution practices. |

## Read alongside the code

[Examples](../../examples) demonstrate integrations; [package source](../../pkg) defines public behavior; [tests](../../tests) exercise cross-package scenarios. Each guide links to the implementations it describes. Components such as compaction, approval, persistence, and telemetry need explicit host wiring; their presence in the repository does not automatically enable them in every agent.

Both language directories use matching filenames. Each page links directly to its counterpart. Update both versions when a public API or documented behavior changes, and validate runnable examples against the same revision.

# Usage, cache observations, and local performance

[Documentation](README.md) · [中文](../zh-CN/observability.md)

Observe real request outcomes before setting performance targets. Omnix supplies usage types, cache calculations, callback hooks, and local tests. An application must connect those components to its own logger, exporter, or notification system.

## Capture model usage

[`llm.LLMAdapter.Chat`](../../pkg/llm/llm.go) returns `Usage` alongside its error. The [DeepSeek adapter](../../pkg/llm/provider_deepseek/deepseek.go) requests streamed usage and reads these SSE fields:

| Provider field | Go field | Interpretation |
| --- | --- | --- |
| `prompt_tokens` | `PromptTokens` | Reported total input tokens. |
| `completion_tokens` | `CompletionTokens` | Reported output tokens. |
| `prompt_cache_hit_tokens` | `PromptCacheHitTokens` | Input tokens reported as cache hits. |
| `prompt_cache_miss_tokens` | `PromptCacheMissTokens` | Input tokens reported as cache misses. |

The [agent loop](../../pkg/agent/agent.go) currently discards the returned `Usage`. Wrap the adapter before passing it to the agent, or observe direct `Chat` calls. This complete wrapper preserves request and streaming behavior and calls an observer after completion, including failures:

```go
package cacheobs

import (
	"context"

	"github.com/herlanai/omnix/pkg/llm"
)

type Adapter struct {
	Inner   llm.LLMAdapter
	Observe func(llm.Usage, error)
}

func (a Adapter) Name() string { return a.Inner.Name() }

func (a Adapter) Chat(
	ctx context.Context,
	req llm.ChatRequest,
	cb func(llm.StreamChunk),
) (llm.Usage, error) {
	usage, err := a.Inner.Chat(ctx, req, cb)
	if a.Observe != nil {
		a.Observe(usage, err)
	}
	return usage, err
}

var _ llm.LLMAdapter = Adapter{}
```

Supply a nonnil `Inner`. Observers run synchronously; keep them short and concurrency-safe if shared. Associate session identity through the wrapper or request context because `Usage` has no session ID. Record bounded metadata such as model, outcome, prompt hash, and usage counts rather than credentials or complete prompts.

## Interpret cache counters

[`llm.HitRatioOf`](../../pkg/llm/cache.go) calculates `hit / (hit + miss)` and returns zero for a nonpositive denominator. Completion tokens are excluded. Hit tokens are not extra input tokens, so do not add them to `PromptTokens`.

The adapter preserves all four fields independently; it does not reconcile totals or infer absent miss counts. Integer fields have no presence flags: an all-zero hit/miss pair can mean absent cache detail, not a measured all-miss request. Each later SSE usage object replaces the previous one rather than being summed. Classify incomplete responses and absent usage separately where possible.

For a token-weighted total, divide summed hits by summed hits plus misses. Averaging per-request ratios instead weights short and long requests equally. `CacheStats` accumulates counters without synchronization, so protect shared instances.

`CacheAlert` compares a sequence of ratios. Defaults warn on an absolute drop greater than `0.3` and flag five consecutive ratios below `0.5`. Use one instance per comparison stream: the session ID argument labels messages but does not partition state. The sink runs synchronously under the detector mutex and must not reenter `Observe` or `Alerts`. `Alert.Webhook` is a flag for your sink, not an HTTP delivery implementation.

## Keep request construction repeatable

The [system assembler](../../pkg/sysprompt/sysprompt.go) orders sections by order and name. `AssembleHash()` hashes only assembled system text, not the complete request. Keep tool descriptions, schema arrays, message order, and unchanged section text consistent; record intentional configuration changes as boundaries.

[`Pipeline.Tools()`](../../pkg/tools/pipeline.go) returns tools in lexical order. Direct adapter callers supply their own tool ordering. [`JsonSchemaNode.Marshal`](../../pkg/tools/schema.go) sorts `required`, but the agent uses a separate `json.Marshal(tool.Schema)` path; do not assume all paths normalize schema arrays.

[`ChangeOnlyInjector`](../../pkg/sysprompt/context_injector.go) returns changed context text without appending events or persisting itself. The host must wire it into messages and restore its hash after restart. Give [`ContextRegistry`](../../pkg/sysprompt/context.go) fragments distinct order values because equal orders lack a deterministic name tie-breaker. [`skills.Injector`](../../pkg/skills/skills.go) compares catalog hashes; the separate discovery `Hash()` can miss same-length content changes.

Stable text does not guarantee provider cache hits. Local helpers cannot control cache retention, placement, tokenization, or billing. Report observed results with workload, sample count, model, and configuration boundaries rather than a universal percentage target.

## Metrics and export are separate

[`internal/telemetry.CacheMetrics`](../../internal/telemetry/cache_metrics.go) is an in-memory support aggregator, not a ready-made OTel instrument set or Prometheus endpoint. Its advertised names are `omnix.cache.hit_ratio`, `omnix.cache.hit_tokens`, `omnix.cache.miss_tokens`, and `omnix.cache.broken_count`.

`Record` adds samples and totals; `MarkBroken` increments a counter manually. Session/preset/turn fields are not automatically exported as labels. Samples have no retention window. `HistogramP50()` sorts all samples and chooses the upper middle value for even counts; it is not a time-windowed or token-weighted measure.

The public [`SessionTelemetry`](../../pkg/telemetry/session_telemetry.go) exposes event, chunk, and tool hooks. Its callbacks run synchronously with panic isolation, so slow callbacks still delay the caller. [`OTelBridge`](../../pkg/telemetry/otel.go) sends spans through an application-supplied `Exporter`; it does not automatically publish cache metrics or install request-loop instrumentation.

The [Grafana JSON](../../deploy/grafana/omnix-cache-dashboard.json) is a starting template. Its first query uses `_count` for a histogram quantile despite a title describing a 24-hour average; it needs actual bucket series and an explicitly chosen time/aggregation meaning. The other queries also depend on exporter-defined series. [Tests](../../tests/cache_metrics_otel_integration_test.go) check JSON parsing and panel count, not working PromQL, data sources, or deployed alerts.

## What local tests measure

The [SSE probe](../../tests/cache_probe_deepseek_client_test.go) uses a local HTTP fixture to verify counter parsing. The [prefix scenarios](../../tests/cache_hit_rate_e2e_test.go) call a [simulator](../../tests/testutil/deepseek_mock.go) that compares the previous prompt's byte prefix and approximates tokens from bytes divided by four. Their thresholds are synthetic test expectations, not live provider results. The shortened-history scenario does not invoke the production compactor.

The [text checker](../../pkg/sysprompt/static_check.go) and [source scanner](../../internal/lint/cache_safety.go) detect limited patterns, not full data flow or request equivalence. `ScanDir` skips parse failures. Validate actual serialized output in addition to these heuristics.

## Run focused checks

From the repository root, these checks need no provider API key. HTTP fixture tests require permission to bind a local port.

```sh
go test ./pkg/sysprompt ./pkg/llm/... ./pkg/telemetry
go test ./tests -run 'Test(CacheStats|DeepSeekParseUsageFromMockSSE|N03|N06|N07|N08|N09)' -count=1
go test ./... -race
```

For bounded CPU/allocation measurements on local event processing:

```sh
go test ./pkg/session -run '^$' -bench '^BenchmarkEffectiveEvents$' -benchmem -count=3
go test ./tests -run '^$' -bench '^BenchmarkIncrementalH04IncrementalPath$' -benchmem -count=3
```

These benchmark event filtering and incremental projection computation, not remote caching or token cost. Compare the same machine, Go version, and load, retaining raw results. The full-replay baseline is intentionally excluded from routine runs because repeated growing-history folds can be expensive.

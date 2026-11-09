# Development

[Documentation](./README.md) · [中文](../zh-CN/development.md)

Run commands from the repository root. The module is `github.com/herlanai/omnix`; [go.mod](../../go.mod) requires Go 1.25 or newer. Install a Go toolchain before starting. Fetching missing module dependencies requires network access.

## Find the relevant code

| Location | Purpose |
|---|---|
| [pkg/](../../pkg/) | Public Go packages: Agent, session, tools, providers, persistence, and supporting services |
| [internal/](../../internal/) | Internal cache-safety analysis and telemetry helpers |
| [examples/](../../examples/) | Runnable integration examples |
| [tests/](../../tests/) | Cross-package behavior and integration tests |
| [tests/testutil/](../../tests/testutil/) | Shared test helpers, including a DeepSeek response simulator |

Package-local tests sit beside their implementation. Start with those when changing a single package; use the integration suite for behavior crossing package boundaries.

## Build and check changes

```bash
# Format Go source and tests.
gofmt -w examples internal pkg tests

# Build every package, including examples.
go build ./...

# Run static checks.
go vet ./...

# Run tests without cached results.
go test ./... -count=1
```

Write code comments in English. Exported API comments should start with the declared name and describe behavior, ownership, errors, or concurrency constraints. Prefer a short explanation of a non-obvious decision over a restatement of the code. Preserve compiler directives such as `//go:build`.

Keep changes focused. Preserve public API and event-format compatibility unless the change explicitly requires otherwise. For a bug fix, add an assertion that fails before the fix and exercises observable behavior. Use temporary directories and cleanup hooks for resources owned by a test.

## Exercise concurrency and platform behavior

```bash
# Run the race detector on a supported platform with a C toolchain.
go test -race ./... -count=1

# Focus on session state and event dispatch.
go test ./pkg/session ./pkg/eventbus -count=1

# Inspect detailed integration results, including skips.
go test ./tests -v -count=1
```

Use channels or other synchronization to coordinate concurrent tests instead of assuming a worker completes after a fixed sleep. Propagate cancellation through `context.Context`, stop timers and workers, and wait for owned goroutines to exit during cleanup.

Several tests open loopback HTTP servers or start local shell processes. A restricted sandbox can block these operations; a permission error is distinct from an assertion failure. The normal suite uses mock HTTP responses and does not need a real DeepSeek API key.

Platform coverage is explicit: Windows ACL tests run on Windows; macOS sandbox-profile tests skip elsewhere; terminal tests can skip when an interactive shell is unavailable. Some SQLite searches skip when the driver lacks the required full-text feature. Read skip reasons rather than treating a successful command as proof that every platform path ran.

The [chat example](../../examples/chat/main.go) is different from the mock-based tests: it calls the real DeepSeek API and reads `DEEPSEEK_API_KEY`. Do not place credentials in source or test fixtures.

## Measure performance

Benchmarks live alongside tests and run with Go's benchmark runner. Keep the machine, Go version, inputs, and build flags constant when comparing revisions. Report `ns/op`, `B/op`, and `allocs/op`; race-enabled runs are not comparable with normal performance measurements.

```bash
# Event dispatch and filtering of visible session events.
go test ./pkg/eventbus ./pkg/session -run '^$' -bench 'Benchmark(Emit|EffectiveEvents)$' -benchmem -count=5

# Full-history versus incremental projection work.
go test ./tests -run '^$' -bench 'BenchmarkIncremental' -benchmem -count=5

# Persistence, tool pooling, registry reads, and SQLite search.
go test ./tests -run '^$' -bench 'PersistencePooledIO|Run.*Pooled|Read.*Freeze|SQLiteSearch' -benchmem -count=5
```

The prefix-cache tests use an in-memory simulator. Their cache-hit ratios validate controlled scenarios; they do not measure the external provider's production cache. Likewise, a faster isolated benchmark does not establish an end-to-end improvement without a representative workload.

Relevant focused tests include:

- [Session surface filtering](../../pkg/session/surface_performance_test.go): replacement boundaries, sparse sequence ranges, and reference equivalence.
- [Sequence range decoding](../../pkg/session/seqrange_test.go): limits and integer boundaries.
- [Todo projections](../../pkg/session/todo_projection_test.go): state changes and equality of structured entries.
- [Event bus](../../pkg/eventbus/eventbus_test.go): dispatch snapshots and concurrent registration.
- [SQLite persistence](../../tests/persistence_sqlite_fts5_test.go): round trips, full-text search, and search benchmarks.

## Review before sharing

```bash
# Inspect the complete patch and whitespace errors.
git diff --stat
git diff --check
git diff
```

Describe the behavior changed, why it changed, and which checks ran. Include skipped or unavailable platform checks and benchmark conditions when they matter. Update the corresponding English and Chinese guide when an API, example, or documented command changes.

# Persisting sessions

[Documentation](README.md) · [中文](../zh-CN/persistence.md)

Session state and storage are separate. Creating an Agent or a `SessionLog` does not automatically save anything. The application chooses a backend, forwards events, handles write failures, and coordinates shutdown.

## Choose the storage layer

| Layer | Public entry point | What it stores |
| --- | --- | --- |
| [Memory log](../../pkg/session/session.go) | `session.NewSessionLog(id)` | Events and live ordering state in this process. |
| [JSONL](../../pkg/persistence/jsonl.go) | `persistence.NewJSONL(dir, batchSize, options...)` | `header.json` and `events.jsonl` beneath each session directory. |
| [SQLite](../../pkg/persistence/sqlite/sqlite.go) | `sqlite.Open(path)` | Event JSON keyed by session/sequence, with optional full-text indexing. |
| [Storage Domain](../../pkg/storage/storage.go) | `storage.NewDomain[T](backend, prefix)` | Typed key/value records with CAS versions, for settings-like records and sidecars rather than session events. |

`SessionLog.Append(data)` validates ordering, assigns a sequence number and timestamp, and updates memory. `Events()` returns the event slice snapshot; it does not flush storage. Backend `Append` methods accept a complete `SessionEvent` and do not substitute for the log's lifecycle validation.

JSONL implements `persistence.Persistence`. SQLite is a separate API: `Load` returns events without a header, and it does not implement that interface. Choose the operations you need rather than assuming the two stores are interchangeable.

## Write a small JSONL session

The following function creates a fresh session, copies its events to JSONL, explicitly flushes, and verifies the stored event count. Call it once per new storage directory/session identity; production writers should track the last persisted sequence rather than resend the whole log after every append.

```go
package sessionstore

import (
	"context"
	"errors"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/persistence"
	"github.com/herlanai/omnix/pkg/session"
)

func WriteExample(ctx context.Context, dir string) (err error) {
	id := brand.NewSessionID("example-session")
	log := session.NewSessionLog(id)
	if _, err = log.Append(session.UserMessageData{Content: "Hello"}); err != nil {
		return err
	}
	store, err := persistence.NewJSONL(dir, 32)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, store.Close()) }()
	if err = store.SaveHeader(ctx, session.NewSessionHeader(id, ".")); err != nil {
		return err
	}
	for _, event := range log.Events() {
		if err = store.Append(ctx, id, event); err != nil {
			return err
		}
	}
	if err = store.Flush(ctx, id); err != nil {
		return err
	}
	_, events, err := store.Load(ctx, id)
	if err != nil {
		return err
	}
	if len(events) != log.Len() {
		return errors.New("stored event count differs from memory")
	}
	return nil
}
```

`SaveHeader` is a concrete JSONL method, not part of `Persistence`. Use [`NewSessionHeader`](../../pkg/session/header.go) to set the current format version. Loading rejects unsupported header versions; application-specific metadata validation remains separate.

## JSONL checkpoints and recovery

Defaults are 16 shards and a 100 ms flush interval. `WithShardCount(n)` rounds to a power of two; `WithFlushInterval(d)` requires a positive duration because the writer uses `time.NewTicker`. A nonpositive batch size becomes one. Reaching the batch threshold signals a background writer; successful `Append` means buffered acceptance, not completed disk persistence.

`Flush(ctx, id)` waits for the shard's current writer and writes that session's pending events. `FlushAll` processes all shards. `Load` reads disk only, so flush before expecting recently appended events. `Snapshot` flushes and loads, then returns an event count; it is not a separate snapshot-file format.

Stop producers before shutdown and call `Close` to stop and join background writers. Appending after `Close` is unsupported. The JSONL methods accept contexts but currently do not use them to cancel file operations or mutex waits.

Loading an unfinished final turn can append synthetic tool-error results, a missing step end, and an interrupted turn end, then rewrite storage. This is recovery with side effects, not a read-only query. Do it while no writer is active for that session. `Load` returns errors for malformed JSON lines and lines beyond its approximately 1 MiB scanner limit; it does not silently truncate arbitrary corrupt tails.

Current durability boundaries matter when choosing this backend:

- There is no `fsync` durability contract. Flush is a buffering/checkpoint operation, not a guarantee against power loss.
- Background flush errors are not delivered through an error channel, and `Close` currently returns nil even if its final background flush failed.
- The append helper discards deferred buffered-writer flush and file-close errors. Explicit `Flush` therefore does not report every possible late I/O failure.
- Retrying a batch after a partial write can duplicate data; this is not an exactly-once log writer. Header writes and recovery rewrites are not multi-file transactions.
- The backend forms paths from the raw session ID. Generate filesystem-safe IDs in trusted application code; branded IDs alone do not validate path safety.

For strict durable acknowledgments or multi-process coordination, add and test an appropriate persistence boundary instead of treating these helpers as a complete journal service.

## SQLite writes and search

`Store.Append(ctx, id, event)` writes one event; `AppendBatch(ctx, id, events)` writes a batch in one SQL transaction. Check the returned commit error. `Load` returns events ordered by sequence; `Count`, `ListSessions`, and `Search` provide read access. Close the store when application ownership ends.

FTS creation is attempted at open time. Check `FTSEnabled()` rather than assuming indexing is available. Search indexes user text, assistant text, and session titles; short queries under three runes and unavailable FTS use `LIKE`. Search behavior and cost therefore differ by query and backend capability. See [search implementation](../../pkg/persistence/sqlite/sqlite.go) and [round-trip/search tests](../../tests/persistence_sqlite_fts5_test.go).

The event table uses `INSERT OR REPLACE` for an existing session/sequence key, while the FTS table receives another insert. Replaying the same batch is not guaranteed to be idempotent for search results; avoid duplicate sequence writes. `Open` does not configure WAL, busy-retry policy, or a custom synchronous setting. SQLite durability and contention behavior follow the actual driver/database configuration. Headers and automatic orphan-turn repair are not supplied by this store.

## Read persisted state

Use `session.FoldAll(events)` or `session.FolderFromEvents(events)` to derive state from loaded events. These functions derive projections; they do not reconstruct an Agent, reconnect its tools, or resume execution automatically. The current `SessionLog` API has no general load-existing-events constructor. Re-appending `event.Data` to a new log would assign new timestamps/sequences and would not preserve all event metadata.

## Verify changes

Run from the repository root:

```sh
go test ./pkg/persistence/... ./pkg/session
go test ./tests -run '^Test(Persistence|SQLite)' -count=1
```

[Flush tests](../../pkg/persistence/flush_test.go) cover checkpoint ordering and recoverable pre-write failures. [Repair tests](../../pkg/persistence/repair_test.go) cover unfinished turns. These tests do not establish power-loss durability, multi-process locking, or recovery from every partial-write failure.

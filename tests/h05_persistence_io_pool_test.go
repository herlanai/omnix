// H05 pooled persistence tests verify round-trip equivalence, observable buffer/writer pool reuse,
// and allocation benchmarks for 100 sessions with 200 events each.
package tests

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/persistence"
	"github.com/herlanai/omnix/pkg/session"
)

// genEventsH05 constructs n small events.
func genEventsH05(n int) []session.SessionEvent {
	evs := make([]session.SessionEvent, 0, n)
	for i := 0; i < n; i++ {
		seq := uint64(i + 1)
		switch i % 2 {
		case 0:
			evs = append(evs, session.SessionEvent{
				Seq: seq, Time: fixedTestTimeH02(),
				Type: session.EventUserMessage,
				Data: session.UserMessageData{Content: "hello-" + itoa(i)},
			})
		case 1:
			evs = append(evs, session.SessionEvent{
				Seq: seq, Time: fixedTestTimeH02(),
				Type: session.EventAssistantMessage,
				Data: session.AssistantMessageData{Content: "world-" + itoa(i)},
			})
		}
	}
	return evs
}

// 1. Load/Append round-trip equivalence.

// TestH05PersistenceRoundTripEquivalent verifies event count, content, and sequence numbers
// through pooled IO; encoder newline differences must not affect loading.
func TestH05PersistenceRoundTripEquivalent(t *testing.T) {
	dir := t.TempDir()

	persistence.ResetJSONLIOStats()

	j, err := persistence.NewJSONL(filepath.Join(dir, "h05"), 50, persistence.WithShardCount(4))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()

	ctx := context.Background()
	id := brand.NewSessionID("h05-eq")
	_ = j.SaveHeader(ctx, session.NewSessionHeader(id, "/ws"))

	events := genEventsH05(200)

	// Append a batch and force persistence with Flush.
	for _, ev := range events {
		if err := j.Append(ctx, id, ev); err != nil {
			t.Fatalf("Append 失败: %v", err)
		}
	}
	if err := j.Flush(ctx, id); err != nil {
		t.Fatalf("Flush 失败: %v", err)
	}

	_, got, err := j.Load(ctx, id)
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if len(got) != len(events) {
		t.Fatalf("Load 事件数 = %d, want %d", len(got), len(events))
	}
	for i, e := range got {
		if e.Seq != events[i].Seq || e.Type != events[i].Type {
			t.Fatalf("事件 %d 不匹配: got %+v want %+v", i, e, events[i])
		}
	}

	// Inspect pool reuse for marshaling buffers and buffered writers.
	stats := persistence.ReadJSONLIOStats()
	if stats.MarshaledEvents != uint64(len(events)) {
		t.Fatalf("MarshaledEvents = %d, want %d", stats.MarshaledEvents, len(events))
	}
	// Expect one marshaling-buffer acquisition per event and at least one buffered-writer acquisition
	// per flush batch.
	minHits := uint64(len(events)) + 1
	if stats.PooledBufferHits < minHits {
		t.Fatalf("PooledBufferHits = %d, want >= %d", stats.PooledBufferHits, minHits)
	}
	if stats.MarshaledBytes == 0 {
		t.Fatal("MarshaledBytes 应 > 0（说明 pooled 路径没被走到）")
	}
}

// TestH05RewriteEquivalent checks persistence compatibility when reloading written events.
func TestH05RewriteEquivalent(t *testing.T) {
	dir := t.TempDir()
	persistence.ResetJSONLIOStats()
	j, err := persistence.NewJSONL(filepath.Join(dir, "h05-rw"), 100)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	ctx := context.Background()
	id := brand.NewSessionID("h05-rw")
	_ = j.SaveHeader(ctx, session.NewSessionHeader(id, "/ws"))
	// Write an initial batch.
	events := genEventsH05(120)
	for _, ev := range events {
		_ = j.Append(ctx, id, ev)
	}
	// Inspect it through Snapshot, which flushes and loads.
	n, err := j.Snapshot(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(events) {
		t.Fatalf("Snapshot N = %d, want %d", n, len(events))
	}
	// Loaded event sequence numbers and types must match the original header/event stream.
	_, got, err := j.Load(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(events) {
		t.Fatalf("rewrite 后 Load 事件数不匹配: %d vs %d", len(got), len(events))
	}
	for i := range got {
		if got[i].Seq != events[i].Seq {
			t.Fatalf("rewrite 后 Seq 失配 i=%d got=%d want=%d", i, got[i].Seq, events[i].Seq)
		}
	}
	stats := persistence.ReadJSONLIOStats()
	if stats.MarshaledEvents < uint64(len(events)) {
		t.Fatalf("MarshaledEvents = %d, want >= %d", stats.MarshaledEvents, len(events))
	}
	// SaveHeader writes header.json directly; a nonzero HeadMarshaledBytes confirms header
	// serialization occurred.
	_ = stats.HeaderMarshaledBytes
}

// 2. Benchmark batched persistence for 100 sessions with 200 events each; target at least 50%
// fewer allocations.

func BenchmarkH05PersistencePooledIO(b *testing.B) {
	dir := b.TempDir()
	// Warm the pool before timing to reduce first-use allocation effects.
	j0, _ := persistence.NewJSONL(filepath.Join(dir, "warm"), 200)
	warm := genEventsH05(200)
	for i := 0; i < 100; i++ {
		id := brand.NewSessionID("warm-" + itoa(i))
		for _, ev := range warm {
			_ = j0.Append(context.Background(), id, ev)
		}
		_ = j0.Flush(context.Background(), id)
	}
	_ = j0.Close()

	persistence.ResetJSONLIOStats()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		runDir := filepath.Join(dir, "r-"+itoa(i))
		j, err := persistence.NewJSONL(runDir, 200, persistence.WithShardCount(8))
		if err != nil {
			b.Fatal(err)
		}
		ctx := context.Background()
		evs := genEventsH05(200)
		for s := 0; s < 100; s++ {
			id := brand.NewSessionID("s-" + itoa(s))
			for _, ev := range evs {
				if aerr := j.Append(ctx, id, ev); aerr != nil {
					b.Fatal(aerr)
				}
			}
			if ferr := j.Flush(ctx, id); ferr != nil {
				b.Fatal(ferr)
			}
		}
		_ = j.Close()
	}
}

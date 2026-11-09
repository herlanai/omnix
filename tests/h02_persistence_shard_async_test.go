// H02 JSONL persistence tests verify shard isolation, interval-triggered asynchronous batches,
// lossless concurrent appends across sessions, graceful Close flushing and worker termination, and
// immediate explicit Flush.
package tests

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/persistence"
	"github.com/herlanai/omnix/pkg/session"
)

// fixedTestTimeH02 returns a fixed timestamp with a file-specific helper name.
func fixedTestTimeH02() time.Time { return time.Unix(1700000000, 0).UTC() }

// newJSONLNoCheckH02 constructs a read-only probe backend with a large batch and default interval.
func newJSONLNoCheckH02(t *testing.T, dir string) *persistence.JSONLBackend {
	t.Helper()
	b, err := persistence.NewJSONL(dir, 1024)
	if err != nil {
		t.Fatalf("NewJSONL 失败: %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return b
}

// TestH02ShardCountDefault verifies DefaultShardCount is 16 and a power of two.
func TestH02ShardCountDefault(t *testing.T) {
	dir := t.TempDir()
	b, err := persistence.NewJSONL(dir, 100)
	if err != nil {
		t.Fatalf("NewJSONL 失败: %v", err)
	}
	defer b.Close()

	if got := b.ShardCount(); got != persistence.DefaultShardCount {
		t.Fatalf("默认 ShardCount = %d, want %d", got, persistence.DefaultShardCount)
	}
	// A power of two is required for bitmask-based indexing.
	n := b.ShardCount()
	if n&(n-1) != 0 {
		t.Fatalf("ShardCount %d 不是 2 的幂", n)
	}
}

// TestH02WithShardCountRoundsUp rounds a non-power-of-two count up to the next power of two.
func TestH02WithShardCountRoundsUp(t *testing.T) {
	dir := t.TempDir()
	b, err := persistence.NewJSONL(dir, 100, persistence.WithShardCount(7))
	if err != nil {
		t.Fatalf("NewJSONL 失败: %v", err)
	}
	defer b.Close()
	if got := b.ShardCount(); got != 8 {
		t.Fatalf("WithShardCount(7) 应向上取整到 8, got %d", got)
	}
}

// TestH02AppendAsyncFlushByInterval flushes below-threshold batches when the interval expires.
func TestH02AppendAsyncFlushByInterval(t *testing.T) {
	dir := t.TempDir()
	// Use a 20 ms interval and batch size 10000 so only the timer triggers flushing.
	interval := 20 * time.Millisecond
	b, err := persistence.NewJSONL(dir, 10000, persistence.WithFlushInterval(interval))
	if err != nil {
		t.Fatalf("NewJSONL 失败: %v", err)
	}
	defer b.Close()

	ctx := context.Background()
	id := brand.NewSessionID("h02_async_int")
	hdr := session.NewSessionHeader(id, "/ws")
	_ = b.SaveHeader(ctx, hdr)

	// Append two entries, well below the 10000-entry threshold.
	for i := uint64(1); i <= 2; i++ {
		_ = b.Append(ctx, id, session.SessionEvent{
			Seq: i, Time: fixedTestTimeH02(),
			Type: session.EventUserMessage,
			Data: session.UserMessageData{Content: "msg"},
		})
	}

	// An immediate load should precede the timer-triggered write.
	probe := newJSONLNoCheckH02(t, dir)
	_, before, _ := probe.Load(ctx, id)
	if len(before) != 0 {
		t.Fatalf("Append 后立刻 Load 应看不到未落盘事件, got %d", len(before))
	}

	// Wait at least three intervals plus margin for a background flush.
	time.Sleep(interval*3 + 10*time.Millisecond)

	_, after, err := probe.Load(ctx, id)
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if len(after) != 2 {
		t.Fatalf("窗口 flush 后应有 2 条事件, got %d", len(after))
	}
}

// TestH02CloseFlushesEverything flushes remaining buffers and stops the writer cleanly.
func TestH02CloseFlushesEverything(t *testing.T) {
	dir := t.TempDir()
	// A large batch and long interval leave Close as the flush trigger.
	b, err := persistence.NewJSONL(dir, 100000,
		persistence.WithFlushInterval(5*time.Second),
		persistence.WithShardCount(4))
	if err != nil {
		t.Fatalf("NewJSONL 失败: %v", err)
	}

	ctx := context.Background()
	const N = 50
	ids := make([]brand.SessionID, N)
	for i := 0; i < N; i++ {
		id := brand.NewSessionID(fmt.Sprintf("h02_close_%d", i))
		ids[i] = id
		_ = b.SaveHeader(ctx, session.NewSessionHeader(id, "/ws"))
		_ = b.Append(ctx, id, session.SessionEvent{
			Seq: 1, Time: fixedTestTimeH02(),
			Type: session.EventTurnStart, Data: session.TurnStartData{Turn: 0},
		})
		_ = b.Append(ctx, id, session.SessionEvent{
			Seq: 2, Time: fixedTestTimeH02(),
			Type: session.EventTurnEnd,
			Data: session.TurnEndData{Turn: 0, Reason: session.ReasonFinished},
		})
	}

	// Close immediately and flush all 100 events across 50 sessions.
	if err := b.Close(); err != nil {
		t.Fatalf("Close 失败: %v", err)
	}

	// Reload every session using a new backend.
	probe, _ := persistence.NewJSONL(dir, 1)
	defer probe.Close()
	for i := 0; i < N; i++ {
		_, evs, err := probe.Load(ctx, ids[i])
		if err != nil {
			t.Fatalf("会话 %d Load 失败: %v", i, err)
		}
		if len(evs) != 2 {
			t.Fatalf("会话 %d 事件数 = %d, want 2", i, len(evs))
		}
	}
}

// TestH02ConcurrentAppendNoLoss verifies the loaded event count matches concurrent appends after
// flushing.
func TestH02ConcurrentAppendNoLoss(t *testing.T) {
	dir := t.TempDir()
	b, err := persistence.NewJSONL(dir, 32,
		persistence.WithFlushInterval(30*time.Millisecond),
		persistence.WithShardCount(8))
	if err != nil {
		t.Fatalf("NewJSONL 失败: %v", err)
	}
	defer b.Close()

	ctx := context.Background()
	id := brand.NewSessionID("h02_concurrent")
	_ = b.SaveHeader(ctx, session.NewSessionHeader(id, "/ws"))

	const goroutines = 16
	const perRoutine = 200
	total := goroutines * perRoutine

	var seq int64 // Increment sequence numbers atomically.
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for k := 0; k < perRoutine; k++ {
				s := atomic.AddInt64(&seq, 1)
				_ = b.Append(ctx, id, session.SessionEvent{
					Seq:  uint64(s),
					Time: fixedTestTimeH02(),
					Type: session.EventAssistantMessage,
					Data: session.AssistantMessageData{Content: "x"},
				})
			}
		}()
	}
	wg.Wait()

	// Flush synchronously rather than relying on the timer.
	if err := b.Flush(ctx, id); err != nil {
		t.Fatalf("Flush 失败: %v", err)
	}
	_, evs, err := b.Load(ctx, id)
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if len(evs) != total {
		t.Fatalf("并发写入后事件数 = %d, want %d（丢失 %d）",
			len(evs), total, total-len(evs))
	}
}

// TestH02ExplicitFlushImmediate persists data without waiting for the interval.
func TestH02ExplicitFlushImmediate(t *testing.T) {
	dir := t.TempDir()
	// A 60-second interval leaves explicit Flush as the trigger.
	b, err := persistence.NewJSONL(dir, 10000,
		persistence.WithFlushInterval(60*time.Second))
	if err != nil {
		t.Fatalf("NewJSONL 失败: %v", err)
	}
	defer b.Close()

	ctx := context.Background()
	id := brand.NewSessionID("h02_exp_flush")
	_ = b.SaveHeader(ctx, session.NewSessionHeader(id, "/ws"))

	_ = b.Append(ctx, id, session.SessionEvent{
		Seq: 1, Time: fixedTestTimeH02(),
		Type: session.EventTurnStart, Data: session.TurnStartData{Turn: 0},
	})
	_ = b.Append(ctx, id, session.SessionEvent{
		Seq: 2, Time: fixedTestTimeH02(),
		Type: session.EventTurnEnd,
		Data: session.TurnEndData{Turn: 0, Reason: session.ReasonFinished},
	})

	if err := b.Flush(ctx, id); err != nil {
		t.Fatalf("Flush 失败: %v", err)
	}
	probe := newJSONLNoCheckH02(t, dir)
	_, evs, err := probe.Load(ctx, id)
	if err != nil {
		t.Fatalf("probe Load 失败: %v", err)
	}
	if len(evs) != 2 {
		t.Fatalf("显式 Flush 后应有 2 条, got %d", len(evs))
	}
}

// TestH02BatchThresholdTrigger verifies the threshold signals an immediate flush.
func TestH02BatchThresholdTrigger(t *testing.T) {
	dir := t.TempDir()
	// A long interval isolates the batch-size signal path.
	b, err := persistence.NewJSONL(dir, 5,
		persistence.WithFlushInterval(60*time.Second))
	if err != nil {
		t.Fatalf("NewJSONL 失败: %v", err)
	}
	defer b.Close()

	ctx := context.Background()
	id := brand.NewSessionID("h02_batch_trig")
	_ = b.SaveHeader(ctx, session.NewSessionHeader(id, "/ws"))

	// Append five entries, exactly the batch size, to signal a flush.
	for i := uint64(1); i <= 5; i++ {
		_ = b.Append(ctx, id, session.SessionEvent{
			Seq: i, Time: fixedTestTimeH02(),
			Type: session.EventAssistantChunk,
			Data: session.AssistantChunkData{Text: "x"},
		})
	}
	// Allow a short interval for the signal to be processed.
	time.Sleep(80 * time.Millisecond)

	probe := newJSONLNoCheckH02(t, dir)
	_, evs, err := probe.Load(ctx, id)
	if err != nil {
		t.Fatalf("probe Load 失败: %v", err)
	}
	if len(evs) != 5 {
		t.Fatalf("达到 batchSize 后信号 flush 应有 5 条事件, got %d", len(evs))
	}
}

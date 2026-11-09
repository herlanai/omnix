// S05 telemetry-hook tests cover event, chunk, and tool hooks, reasonable latency, and panic
// isolation between hooks and callers.
package tests

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/herlanai/omnix/pkg/telemetry"
)

// TestTelemetryThreeHooksFire invokes all three hook kinds and verifies nonnegative latency.
func TestTelemetryThreeHooksFire(t *testing.T) {
	tm := telemetry.New()
	ctx := context.Background()

	var (
		mu                    sync.Mutex
		events, chunks, tools int
		latencies             []time.Duration
	)

	tm.RegisterEventHook(func(_ context.Context, d telemetry.EventHookData) {
		mu.Lock()
		defer mu.Unlock()
		events++
		latencies = append(latencies, d.LatencyMS)
	})
	tm.RegisterChunkHook(func(_ context.Context, d telemetry.ChunkHookData) {
		mu.Lock()
		defer mu.Unlock()
		chunks++
		latencies = append(latencies, d.LatencyMS)
	})
	tm.RegisterToolHook(func(_ context.Context, d telemetry.ToolHookData) {
		mu.Lock()
		defer mu.Unlock()
		tools++
		latencies = append(latencies, d.LatencyMS)
	})

	// Trigger each instrumentation kind in sequence.
	now := time.Now()
	tm.RecordEvent(ctx, "user/message", now.Add(-2*time.Millisecond))
	tm.RecordChunk(ctx, 1, 12, now.Add(-3*time.Millisecond))
	tm.RecordTool(ctx, "bash", true, now.Add(-5*time.Millisecond))

	mu.Lock()
	defer mu.Unlock()
	if events != 1 || chunks != 1 || tools != 1 {
		t.Fatalf("三类钩子应各触发 1 次，实际 event=%d chunk=%d tool=%d", events, chunks, tools)
	}
	if len(latencies) != 3 {
		t.Fatalf("应收集 3 条 latency，实际 %d", len(latencies))
	}
	// Latency should reflect the injected delays of approximately 2, 3, and 5 ms.
	for i, l := range latencies {
		if l < 0 {
			t.Fatalf("latency[%d]=%v 不应为负", i, l)
		}
		if l > 100*time.Millisecond {
			t.Fatalf("latency[%d]=%v 超出合理区间", i, l)
		}
	}
}

// TestTelemetryToolOkFlag propagates tool success and failure flags.
func TestTelemetryToolOkFlag(t *testing.T) {
	tm := telemetry.New()
	ctx := context.Background()
	var okResult bool
	tm.RegisterToolHook(func(_ context.Context, d telemetry.ToolHookData) {
		okResult = d.Ok
	})
	tm.RecordTool(ctx, "todo_write", false, time.Now())
	if okResult {
		t.Fatal("失败工具应 Ok=false")
	}
}

// TestTelemetryPanicIsolated prevents one hook panic from interrupting other hooks or callers.
func TestTelemetryPanicIsolated(t *testing.T) {
	tm := telemetry.New()
	ctx := context.Background()
	fired := 0

	tm.RegisterEventHook(func(_ context.Context, _ telemetry.EventHookData) {
		panic("boom") // A deliberately faulty hook panics immediately.
	})
	tm.RegisterEventHook(func(_ context.Context, _ telemetry.EventHookData) {
		fired++
	})

	// Must not crash.
	tm.RecordEvent(ctx, "tool/bash", time.Now())
	if fired != 1 {
		t.Fatalf("panic 钩子后的正常钩子应仍被调用，实际 fired=%d", fired)
	}
}

// TestTelemetryNoHooksNoPanic safely dispatches to an empty hook set.
func TestTelemetryNoHooksNoPanic(t *testing.T) {
	tm := telemetry.New()
	ctx := context.Background()
	now := time.Now()
	tm.RecordEvent(ctx, "user/message", now)
	tm.RecordChunk(ctx, 1, 10, now)
	tm.RecordTool(ctx, "bash", true, now)
	// No hooks must not cause a panic.
}

// TestTelemetryConcurrent verifies concurrent registration and dispatch with the race detector.
func TestTelemetryConcurrent(t *testing.T) {
	tm := telemetry.New()
	ctx := context.Background()
	// Register and dispatch concurrently.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tm.RegisterEventHook(func(_ context.Context, _ telemetry.EventHookData) {})
		}()
	}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tm.RecordEvent(ctx, "user/message", time.Now())
			tm.RecordChunk(ctx, 1, 5, time.Now())
			tm.RecordTool(ctx, "bash", true, time.Now())
		}()
	}
	wg.Wait()
}

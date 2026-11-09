// Package telemetry provides session-level observability hooks (S05), matching
// session-telemetry. Agent and session code record event appends, assistant text
// chunks, and completed tool calls. Record methods compute milliseconds from the
// supplied start time for consistent latency measurements. Hooks run synchronously
// in sequence, with individual panic recovery so failures do not interrupt other
// hooks or the caller. Latency is not clamped; tests use controlled clocks.
package telemetry

import (
	"context"
	"sync"
	"time"
)

// Hook types.

// EventHookData records event type counts and append latency.
type EventHookData struct {
	EventType string        // Session event type, such as user/message.
	LatencyMS time.Duration // Elapsed time for one event append.
}

// ChunkHookData describes a streamed chunk.
type ChunkHookData struct {
	Seq       uint64        // Chunk sequence number.
	TextLen   int           // Text length.
	LatencyMS time.Duration // Elapsed time for one chunk.
}

// ToolHookData describes a tool execution.
type ToolHookData struct {
	ToolName  string        // Tool name, such as bash or todo_write.
	Ok        bool          // Whether execution succeeded (err == nil).
	LatencyMS time.Duration // Tool execution duration.
}

// EventHook, ChunkHook, and ToolHook define the three callback signatures.
type EventHook func(ctx context.Context, d EventHookData)
type ChunkHook func(ctx context.Context, d ChunkHookData)
type ToolHook func(ctx context.Context, d ToolHookData)

// Session telemetry registry.

// SessionTelemetry is a concurrency-safe session telemetry registry.
type SessionTelemetry struct {
	mu         sync.RWMutex
	eventHooks []EventHook
	chunkHooks []ChunkHook
	toolHooks  []ToolHook
	now        func() time.Time // Injectable clock for tests.
}

// New creates an empty telemetry registry.
func New() *SessionTelemetry {
	return &SessionTelemetry{
		eventHooks: []EventHook{},
		chunkHooks: []ChunkHook{},
		toolHooks:  []ToolHook{},
		now:        time.Now,
	}
}

// nowFn returns the current clock function for implementation and tests.
func (t *SessionTelemetry) nowFn() func() time.Time {
	if t.now == nil {
		return time.Now
	}
	return t.now
}

// Hook registration.

// RegisterEventHook registers an event-append hook.
func (t *SessionTelemetry) RegisterEventHook(h EventHook) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.eventHooks = append(t.eventHooks, h)
}

// RegisterChunkHook registers a streaming-chunk hook.
func (t *SessionTelemetry) RegisterChunkHook(h ChunkHook) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.chunkHooks = append(t.chunkHooks, h)
}

// RegisterToolHook registers a tool-execution hook.
func (t *SessionTelemetry) RegisterToolHook(h ToolHook) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.toolHooks = append(t.toolHooks, h)
}

// Recording instrumentation.

// RecordEvent records an append using startedAt as its latency origin.
func (t *SessionTelemetry) RecordEvent(ctx context.Context, eventType string, startedAt time.Time) {
	d := EventHookData{EventType: eventType, LatencyMS: t.since(startedAt)}
	t.dispatchEvent(ctx, d)
}

// RecordChunk records one streaming chunk.
func (t *SessionTelemetry) RecordChunk(ctx context.Context, seq uint64, textLen int, startedAt time.Time) {
	d := ChunkHookData{Seq: seq, TextLen: textLen, LatencyMS: t.since(startedAt)}
	t.dispatchChunk(ctx, d)
}

// RecordTool records completion of a tool call.
func (t *SessionTelemetry) RecordTool(ctx context.Context, toolName string, ok bool, startedAt time.Time) {
	d := ToolHookData{ToolName: toolName, Ok: ok, LatencyMS: t.since(startedAt)}
	t.dispatchTool(ctx, d)
}

// since computes milliseconds elapsed since startedAt.
func (t *SessionTelemetry) since(startedAt time.Time) time.Duration {
	if startedAt.IsZero() {
		return 0
	}
	return t.nowFn()().Sub(startedAt)
}

// Dispatch with per-hook panic isolation.

func (t *SessionTelemetry) dispatchEvent(ctx context.Context, d EventHookData) {
	t.mu.RLock()
	hooks := append([]EventHook(nil), t.eventHooks...)
	t.mu.RUnlock()
	for _, h := range hooks {
		callSafe(func() { h(ctx, d) })
	}
}

func (t *SessionTelemetry) dispatchChunk(ctx context.Context, d ChunkHookData) {
	t.mu.RLock()
	hooks := append([]ChunkHook(nil), t.chunkHooks...)
	t.mu.RUnlock()
	for _, h := range hooks {
		callSafe(func() { h(ctx, d) })
	}
}

func (t *SessionTelemetry) dispatchTool(ctx context.Context, d ToolHookData) {
	t.mu.RLock()
	hooks := append([]ToolHook(nil), t.toolHooks...)
	t.mu.RUnlock()
	for _, h := range hooks {
		callSafe(func() { h(ctx, d) })
	}
}

// callSafe recovers callback panics so instrumentation cannot interrupt execution.
func callSafe(fn func()) {
	defer func() { _ = recover() }()
	fn()
}

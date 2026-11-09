// Telemetry export (S07) bridges S05 hooks to an OTLP-style exporter with session,
// turn, and step baggage, matching telemetry-otel. The Exporter interface avoids
// a mandatory third-party SDK: implementations can use real gateways or an
// InMemoryExporter for tests. OTelBridge emits one span per event, chunk, or tool
// hook, carrying baggage for downstream correlation.
package telemetry

import (
	"context"
	"sync"
	"time"
)

// ============================================================================
// Baggage & Span
// ============================================================================

// Baggage carries session, turn, and step IDs across telemetry dimensions.
type Baggage struct {
	SessionID string `json:"sessionId"`
	TurnID    string `json:"turnId"`
	StepID    string `json:"stepId"`
}

// Span represents an exported telemetry span.
type Span struct {
	Name    string            `json:"name"`
	Baggage Baggage           `json:"baggage"`
	Started time.Time         `json:"started"`
	Ended   time.Time         `json:"ended"`
	Kind    string            `json:"kind,omitempty"` // event/chunk/tool
	Attrs   map[string]string `json:"attrs,omitempty"`
}

// ============================================================================
// Exporter
// ============================================================================

// Exporter receives spans, corresponding to an OTLP exporter.
type Exporter interface {
	ExportSpan(ctx context.Context, span Span) error
}

// In-memory export for tests and local collection.

// InMemoryExporter collects spans for test assertions.
type InMemoryExporter struct {
	mu    sync.Mutex
	Spans []Span
}

// ExportSpan records a span.
func (m *InMemoryExporter) ExportSpan(_ context.Context, span Span) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Spans = append(m.Spans, span)
	return nil
}

// Collect returns a snapshot of collected spans.
func (m *InMemoryExporter) Collect() []Span {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Span, len(m.Spans))
	copy(out, m.Spans)
	return out
}

// ============================================================================
// BaggageProvider & OTelBridge
// ============================================================================

// BaggageProvider supplies current session, turn, and step context.
type BaggageProvider func() Baggage

// OTelBridge connects S05 telemetry hooks to an Exporter.
type OTelBridge struct {
	exporter Exporter
	baggage  BaggageProvider
}

// NewOTelBridge creates a telemetry bridge.
func NewOTelBridge(exporter Exporter, baggage BaggageProvider) *OTelBridge {
	return &OTelBridge{exporter: exporter, baggage: baggage}
}

// Attach registers event, chunk, and tool hooks that emit spans.
func (b *OTelBridge) Attach(tm *SessionTelemetry) {
	bag := b.currentBaggage()
	tm.RegisterEventHook(func(ctx context.Context, d EventHookData) {
		_ = b.exporter.ExportSpan(ctx, Span{
			Name:    "session.event." + d.EventType,
			Baggage: bag,
			Kind:    "event",
			Started: time.Now(), Ended: time.Now(),
			Attrs: map[string]string{"latency_ms": d.LatencyMS.String()},
		})
	})
	tm.RegisterChunkHook(func(ctx context.Context, d ChunkHookData) {
		_ = b.exporter.ExportSpan(ctx, Span{
			Name:    "session.chunk",
			Baggage: bag,
			Kind:    "chunk",
			Started: time.Now(), Ended: time.Now(),
			Attrs: map[string]string{"seq": u64str(d.Seq), "len": itoa(d.TextLen)},
		})
	})
	tm.RegisterToolHook(func(ctx context.Context, d ToolHookData) {
		_ = b.exporter.ExportSpan(ctx, Span{
			Name:    "session.tool." + d.ToolName,
			Baggage: bag,
			Kind:    "tool",
			Started: time.Now(), Ended: time.Now(),
			Attrs: map[string]string{"ok": boolstr(d.Ok), "latency_ms": d.LatencyMS.String()},
		})
	})
}

// currentBaggage evaluates the provider once to keep a recording's context consistent.
func (b *OTelBridge) currentBaggage() Baggage {
	if b.baggage == nil {
		return Baggage{}
	}
	return b.baggage()
}

// Helpers.

func u64str(v uint64) string {
	return itoa(int(v))
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	var buf [20]byte
	i := len(buf)
	for v != 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func boolstr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

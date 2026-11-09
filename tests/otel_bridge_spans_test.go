// S07 OTel tests bridge all three S05 hooks into InMemoryExporter spans, each with session, turn,
// and step baggage and the correct event/chunk/tool kind.
package tests

import (
	"context"
	"testing"
	"time"

	"github.com/herlanai/omnix/pkg/telemetry"
)

// TestOTelBridgeSpansCarryBaggage verifies all three instrumentation kinds export the three IDs.
func TestOTelBridgeSpansCarryBaggage(t *testing.T) {
	tm := telemetry.New()
	exporter := &telemetry.InMemoryExporter{}

	// Fixed session, turn, and step baggage.
	bag := telemetry.Baggage{SessionID: "s-1", TurnID: "t-1", StepID: "st-1"}
	bridge := telemetry.NewOTelBridge(exporter, func() telemetry.Baggage { return bag })
	bridge.Attach(tm)

	ctx := context.Background()
	now := time.Now()
	tm.RecordEvent(ctx, "user/message", now.Add(-time.Millisecond))
	tm.RecordChunk(ctx, 7, 42, now.Add(-time.Millisecond))
	tm.RecordTool(ctx, "bash", true, now.Add(-time.Millisecond))

	spans := exporter.Collect()
	if len(spans) != 3 {
		t.Fatalf("应导出 3 条 span，实际 %d", len(spans))
	}
	for i, sp := range spans {
		if sp.Baggage.SessionID != "s-1" || sp.Baggage.TurnID != "t-1" || sp.Baggage.StepID != "st-1" {
			t.Fatalf("span[%d] 应带 session/turn/step baggage，实际 %+v", i, sp.Baggage)
		}
	}
	// Check that every kind appears; ordering follows telemetry registration and is not asserted here.
	kinds := map[string]bool{}
	for _, sp := range spans {
		kinds[sp.Kind] = true
	}
	if !kinds["event"] || !kinds["chunk"] || !kinds["tool"] {
		t.Fatalf("应包含 event/chunk/tool 三类 Kind，实际 %v", kinds)
	}
}

// TestOTelSpanNameSuffix includes the tool name in session.tool.<tool>.
func TestOTelSpanNameSuffix(t *testing.T) {
	tm := telemetry.New()
	exporter := &telemetry.InMemoryExporter{}
	bridge := telemetry.NewOTelBridge(exporter, nil)
	bridge.Attach(tm)
	tm.RecordTool(context.Background(), "todo_write", false, time.Now())

	for _, sp := range exporter.Collect() {
		if sp.Kind == "tool" && sp.Name != "session.tool.todo_write" {
			t.Fatalf("工具 span 名应为 session.tool.todo_write，实际 %s", sp.Name)
		}
	}
}

// TestOTelNoBaggageProvider exports spans with empty baggage without panicking.
func TestOTelNoBaggageProvider(t *testing.T) {
	tm := telemetry.New()
	exporter := &telemetry.InMemoryExporter{}
	bridge := telemetry.NewOTelBridge(exporter, nil)
	bridge.Attach(tm)
	tm.RecordEvent(context.Background(), "user/message", time.Now())
	if n := len(exporter.Collect()); n != 1 {
		t.Fatalf("应导出 1 条 span，实际 %d", n)
	}
}

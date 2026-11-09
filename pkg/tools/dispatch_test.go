package tools

import (
	"context"
	"errors"
	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/waterfall"
	"testing"
)

func TestDispatchSelectsExactlyOneTool(t *testing.T) {
	for _, pooled := range []bool{false, true} {
		calls := map[string]int{}
		p := NewPipeline().SetPooled(pooled)
		for _, name := range []string{"a", "b"} {
			p.WithTool(&Tool{Name: name, Execute: func(context.Context, map[string]any) (any, error) { calls[name]++; return name, nil }})
		}
		r := p.Run(context.Background(), &ToolCallRequest{CallID: brand.NewToolCallID("c"), Tool: "b"}, nil)
		if r.IsError || r.Value != "b" || calls["a"] != 0 || calls["b"] != 1 {
			t.Fatalf("pooled=%v result=%+v calls=%v", pooled, r, calls)
		}
		r = p.Run(context.Background(), &ToolCallRequest{Tool: "unknown"}, nil)
		if !r.IsError || calls["a"] != 0 || calls["b"] != 1 {
			t.Fatalf("unknown dispatch: %+v %v", r, calls)
		}
	}
}

func TestPipelineGuardsInBothModes(t *testing.T) {
	for _, pooled := range []bool{false, true} {
		called := false
		tool := &Tool{Name: "guarded", Schema: &JsonSchemaNode{Type: TypeObject, Required: []string{"x"}}, Execute: func(context.Context, map[string]any) (any, error) { called = true; return nil, nil }}
		p := NewPipeline().SetPooled(pooled).WithTool(tool)
		req := &ToolCallRequest{Tool: "guarded", Input: map[string]any{}}
		if r := p.Run(context.Background(), req, nil); !r.IsError || called {
			t.Fatalf("schema bypass: %+v", r)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if r := p.Run(ctx, req, nil); !r.IsError || r.ErrorCode == "" || called {
			t.Fatalf("cancellation bypass: %+v", r)
		}
		p.UsePre(func(*ExecContext, waterfall.NextFunc) error { return errors.New("blocked") })
		if r := p.Run(context.Background(), req, nil); !r.IsError || r.Error != "blocked" || called {
			t.Fatalf("pre error bypass: %+v", r)
		}
	}
}

func TestToolTimeoutAndPanic(t *testing.T) {
	for _, pooled := range []bool{false, true} {
		p := NewPipeline().SetPooled(pooled)
		timeout := &Tool{Name: "timeout", TimeoutMs: 5, Execute: func(ctx context.Context, _ map[string]any) (any, error) { <-ctx.Done(); return nil, ctx.Err() }}
		if r := p.Run(context.Background(), &ToolCallRequest{Tool: "timeout"}, timeout); !r.IsError || r.ErrorCode != ToolTimeoutCode {
			t.Fatalf("timeout=%+v", r)
		}
		panics := &Tool{Name: "panic", Execute: func(context.Context, map[string]any) (any, error) { panic("boom") }}
		if r := p.Run(context.Background(), &ToolCallRequest{Tool: "panic"}, panics); !r.IsError {
			t.Fatalf("panic=%+v", r)
		}
	}
}

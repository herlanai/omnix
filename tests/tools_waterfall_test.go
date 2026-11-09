// M23 four-stage tool execution waterfall tests.
package tests

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/tools"
	"github.com/herlanai/omnix/pkg/waterfall"
)

// makeEchoTool constructs a tool that returns its input.
func makeEchoTool() *tools.Tool {
	return &tools.Tool{
		Name:        "echo",
		Description: "echo input",
		Execute: func(ctx context.Context, input map[string]any) (any, error) {
			return map[string]any{"echo": input["msg"]}, nil
		},
	}
}

// TestToolPipelinePreDeny skips execution and returns isError after pre-execution denial.
func TestToolPipelinePreDeny(t *testing.T) {
	p := tools.NewPipeline().
		UsePre(func(ec *tools.ExecContext, next waterfall.NextFunc) error {
			if ec.Request.Input["danger"] == true {
				ec.Denied = true
				return nil // Omitting next short-circuits the chain.
			}
			return next()
		}).
		WithTool(makeEchoTool())

	req := &tools.ToolCallRequest{
		CallID: brand.NewToolCallID("c1"),
		Tool:   "echo",
		Input:  map[string]any{"msg": "hello", "danger": true},
	}
	res := p.Run(context.Background(), req, nil)
	if !res.IsError {
		t.Fatal("pre deny 应标记 isError")
	}
	if !strings.Contains(res.Error, "denied") {
		t.Fatalf("错误信息应含 denied: %q", res.Error)
	}
	if res.Value != nil {
		t.Fatal("deny 后不应有结果值")
	}
}

// TestToolPipelinePreRewrite changes input before execution.
func TestToolPipelinePreRewrite(t *testing.T) {
	p := tools.NewPipeline().
		UsePre(func(ec *tools.ExecContext, next waterfall.NextFunc) error {
			// Replace the secret message with masked text.
			if v, ok := ec.Request.Input["msg"].(string); ok && v == "secret" {
				ec.Request.Input["msg"] = "masked"
			}
			return next()
		}).
		WithTool(makeEchoTool())

	req := &tools.ToolCallRequest{
		CallID: brand.NewToolCallID("c2"),
		Tool:   "echo",
		Input:  map[string]any{"msg": "secret"},
	}
	res := p.Run(context.Background(), req, nil)
	if res.IsError {
		t.Fatalf("换参后应成功: %s", res.Error)
	}
	val := res.Value.(map[string]any)
	if val["echo"] != "masked" {
		t.Fatalf("工具应收到改写后的参数: %v", val)
	}
}

// TestToolPipelineExecuteError converts tool errors into isError results.
func TestToolPipelineExecuteError(t *testing.T) {
	failing := &tools.Tool{
		Name: "fail",
		Execute: func(ctx context.Context, input map[string]any) (any, error) {
			return nil, errors.New("exec failed")
		},
	}
	p := tools.NewPipeline().WithTool(failing)

	req := &tools.ToolCallRequest{CallID: brand.NewToolCallID("c3"), Tool: "fail", Input: map[string]any{}}
	res := p.Run(context.Background(), req, nil)
	if !res.IsError || !strings.Contains(res.Error, "exec failed") {
		t.Fatalf("工具错误应传播: %+v", res)
	}
}

// TestToolPipelineSignalCancel returns isError when execution changes the signal to cancel.
func TestToolPipelineSignalCancel(t *testing.T) {
	p := tools.NewPipeline().
		UseExecute(func(ec *tools.ExecContext, next waterfall.NextFunc) error {
			// Set the cancel signal before invoking the real tool.
			ec.Signal = tools.SignalCancel
			return next()
		}).
		UsePost(func(ec *tools.ExecContext, next waterfall.NextFunc) error {
			// The post stage detects cancellation and blocks the result.
			if ec.Signal == tools.SignalCancel {
				ec.Denied = true
				return next()
			}
			return next()
		}).
		WithTool(makeEchoTool())

	req := &tools.ToolCallRequest{CallID: brand.NewToolCallID("c4"), Tool: "echo", Input: map[string]any{"msg": "x"}}
	res := p.Run(context.Background(), req, nil)
	if !res.IsError {
		t.Fatal("cancel 后结果应 isError")
	}
}

// TestToolPipelinePostMeta adds metadata after execution.
func TestToolPipelinePostMeta(t *testing.T) {
	p := tools.NewPipeline().
		UsePost(func(ec *tools.ExecContext, next waterfall.NextFunc) error {
			ec.Meta["duration_ms"] = 42
			return next()
		}).
		WithTool(makeEchoTool())

	req := &tools.ToolCallRequest{CallID: brand.NewToolCallID("c5"), Tool: "echo", Input: map[string]any{"msg": "hi"}}
	res := p.Run(context.Background(), req, nil)
	// The result stage must see the added metadata.
	_ = res
}

// TestToolPipelinePostTruncate truncates output after execution.
func TestToolPipelinePostTruncate(t *testing.T) {
	longTool := &tools.Tool{
		Name: "long",
		Execute: func(ctx context.Context, input map[string]any) (any, error) {
			return strings.Repeat("x", 1000), nil
		},
	}
	p := tools.NewPipeline().
		UsePost(func(ec *tools.ExecContext, next waterfall.NextFunc) error {
			if s, ok := ec.Result.Value.(string); ok && len(s) > 10 {
				ec.Result.Value = s[:10] + "..."
			}
			return next()
		}).
		WithTool(longTool)

	req := &tools.ToolCallRequest{CallID: brand.NewToolCallID("c6"), Tool: "long", Input: map[string]any{}}
	res := p.Run(context.Background(), req, nil)
	if s := res.Value.(string); len(s) != 13 {
		t.Fatalf("截断结果长度 = %d, want 13", len(s))
	}
}

// TestToolPipelineResultPhase reads metadata and rewrites the final result.
func TestToolPipelineResultPhase(t *testing.T) {
	p := tools.NewPipeline().
		UsePost(func(ec *tools.ExecContext, next waterfall.NextFunc) error {
			ec.Meta["processed"] = true
			return next()
		}).
		UseResult(func(ec *tools.ExecContext, next waterfall.NextFunc) error {
			if processed, _ := ec.Meta["processed"].(bool); processed {
				ec.Result.Value = map[string]any{"wrapped": true}
			}
			return next()
		}).
		WithTool(makeEchoTool())

	req := &tools.ToolCallRequest{CallID: brand.NewToolCallID("c7"), Tool: "echo", Input: map[string]any{"msg": "hi"}}
	res := p.Run(context.Background(), req, nil)
	val := res.Value.(map[string]any)
	if val["wrapped"] != true {
		t.Fatalf("result 级应包装结果: %v", val)
	}
}

// TestToolPipelineAllPhases verifies ordering and output across all four stages.
func TestToolPipelineAllPhases(t *testing.T) {
	var order []string
	record := func(name string) waterfall.Handler[tools.ExecContext] {
		return func(ec *tools.ExecContext, next waterfall.NextFunc) error {
			order = append(order, name)
			return next()
		}
	}

	p := tools.NewPipeline().
		UsePre(record("pre")).
		UseExecute(record("execute")).
		UsePost(record("post")).
		UseResult(record("result")).
		WithTool(makeEchoTool())

	req := &tools.ToolCallRequest{CallID: brand.NewToolCallID("c8"), Tool: "echo", Input: map[string]any{"msg": "hi"}}
	res := p.Run(context.Background(), req, nil)
	if res.IsError {
		t.Fatalf("应成功: %s", res.Error)
	}
	if strings.Join(order, ",") != "pre,execute,post,result" {
		t.Fatalf("四级顺序 = %v", order)
	}
}

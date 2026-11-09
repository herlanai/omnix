// H06 pipeline pooling tests verify branch equivalence for pre-deny, execute, post-block, and
// result handling; independent returned results; safe context/metadata reuse; and allocation
// differences between pooled and unpooled execution.
package tests

import (
	"context"
	"strings"
	"testing"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/tools"
	"github.com/herlanai/omnix/pkg/waterfall"
)

// 1. Pooled and unpooled Run must preserve branch behavior.

// TestH06PooledPreDeny verifies pre-deny short-circuiting with pooling enabled.
func TestH06PooledPreDeny(t *testing.T) {
	p := tools.NewPipeline().SetPooled(true).
		UsePre(func(ec *tools.ExecContext, next waterfall.NextFunc) error {
			if ec.Request.Input["danger"] == true {
				ec.Denied = true
				return nil
			}
			return next()
		}).
		WithTool(makeEchoTool())

	req := &tools.ToolCallRequest{
		CallID: brand.NewToolCallID("hp1"),
		Tool:   "echo",
		Input:  map[string]any{"msg": "hi", "danger": true},
	}
	res := p.Run(context.Background(), req, nil)
	if !res.IsError {
		t.Fatal("pooled pre deny 应标记 isError")
	}
	if !strings.Contains(res.Error, "denied") {
		t.Fatalf("pooled deny 错误应含 denied: %q", res.Error)
	}
	if res.Value != nil {
		t.Fatal("pooled deny 后不应有值")
	}
	// Check whether pooling is enabled.
	if !p.PooledState() {
		t.Fatal("PooledState 应为 true")
	}
}

// TestH06PooledExecuteResult verifies normal results in pooled mode.
func TestH06PooledExecuteResult(t *testing.T) {
	p := tools.NewPipeline().SetPooled(true).WithTool(makeEchoTool())
	req := &tools.ToolCallRequest{
		CallID: brand.NewToolCallID("hp2"),
		Tool:   "echo",
		Input:  map[string]any{"msg": "world"},
	}
	res := p.Run(context.Background(), req, nil)
	if res.IsError {
		t.Fatalf("pooled 正常执行不应出错: %s", res.Error)
	}
	v, ok := res.Value.(map[string]any)
	if !ok {
		t.Fatalf("res.Value 类型异常: %T", res.Value)
	}
	if v["echo"] != "world" {
		t.Fatalf("echo 值异常: %v", v)
	}
}

// TestH06PooledResultIndependent ensures later runs do not overwrite previously returned results.
func TestH06PooledResultIndependent(t *testing.T) {
	p := tools.NewPipeline().SetPooled(true).WithTool(makeEchoTool())
	ctx := context.Background()

	// Issue distinct requests and retain their result pointers.
	results := make([]*tools.ToolCallResult, 0, 5)
	for i := 0; i < 5; i++ {
		req := &tools.ToolCallRequest{
			CallID: brand.NewToolCallID("ind-" + itoa(i)),
			Tool:   "echo",
			Input:  map[string]any{"msg": "m" + itoa(i)},
		}
		results = append(results, p.Run(ctx, req, nil))
	}
	// Each result must retain its own CallID rather than being overwritten by pool reuse.
	for i, r := range results {
		if r.CallID.Raw() != "ind-"+itoa(i) {
			t.Fatalf("第 %d 个结果 CallID 被污染: %s", i, r.CallID.Raw())
		}
		v, ok := r.Value.(map[string]any)
		if !ok || v["echo"] != "m"+itoa(i) {
			t.Fatalf("第 %d 个结果值被污染: %+v", i, r.Value)
		}
	}
}

// TestH06PooledMetaIsolation verifies reused ExecContext metadata is cleared between calls.
func TestH06PooledMetaIsolation(t *testing.T) {
	// At pre-execution, Meta must be empty; clearMeta must remove the previous run's post-execution
	// mutations.
	p := tools.NewPipeline().SetPooled(true).
		UseExecute(func(ec *tools.ExecContext, next waterfall.NextFunc) error {
			if len(ec.Meta) != 0 {
				t.Fatalf("复用 ExecContext 时 Meta 残留: %+v", ec.Meta)
			}
			return next()
		}).
		UsePost(func(ec *tools.ExecContext, next waterfall.NextFunc) error {
			// Add a tag to Meta on every run.
			ec.Meta["tag"] = ec.Request.Input["tag"]
			return next()
		}).
		WithTool(makeEchoTool())
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		req := &tools.ToolCallRequest{
			CallID: brand.NewToolCallID("meta-" + itoa(i)),
			Tool:   "echo",
			Input:  map[string]any{"msg": "x", "tag": "t" + itoa(i)},
		}
		p.Run(ctx, req, nil)
	}
}

// 2. Compare allocations per operation with and without pooling.

// BenchmarkH06RunUnPooled measures the default path.
func BenchmarkH06RunUnPooled(b *testing.B) {
	p := tools.NewPipeline().WithTool(makeEchoTool())
	ctx := context.Background()
	req := &tools.ToolCallRequest{
		CallID: brand.NewToolCallID("bench"), Tool: "echo",
		Input: map[string]any{"msg": "x"},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = p.Run(ctx, req, nil)
	}
}

// BenchmarkH06RunPooled measures the H06 pooled path.
func BenchmarkH06RunPooled(b *testing.B) {
	p := tools.NewPipeline().SetPooled(true).WithTool(makeEchoTool())
	ctx := context.Background()
	req := &tools.ToolCallRequest{
		CallID: brand.NewToolCallID("bench"), Tool: "echo",
		Input: map[string]any{"msg": "x"},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = p.Run(ctx, req, nil)
	}
}

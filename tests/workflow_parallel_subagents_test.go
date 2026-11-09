// S12 workflow tests cover three parallel two-step subagents with ordered aggregation, sequential
// short-circuiting, and cascading cancellation marked by Cancelled.
package tests

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/herlanai/omnix/pkg/workflow"
)

// spawnStub integrates an S02 subagent and returns agent:<input>:done.
func spawnStub(ctx context.Context, backend, input string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "agent:" + backend + ":" + input + ":done", nil
}

// TestWorkflowParallel3Subagents aggregates three parallel subagents with two steps each.
func TestWorkflowParallel3Subagents(t *testing.T) {
	// Each subagent runs two Agent steps sequentially.
	subA := workflow.Pipeline("subA",
		workflow.Agent("a1", "in-process", "A-1", spawnStub),
		workflow.Agent("a2", "in-process", "A-2", spawnStub),
	)
	subB := workflow.Pipeline("subB",
		workflow.Agent("b1", "in-process", "B-1", spawnStub),
		workflow.Agent("b2", "in-process", "B-2", spawnStub),
	)
	subC := workflow.Pipeline("subC",
		workflow.Agent("c1", "in-process", "C-1", spawnStub),
		workflow.Agent("c2", "in-process", "C-2", spawnStub),
	)

	// The outer workflow runs three subagents in parallel.
	meta := workflow.Parallel("meta", workflow.Seq(subA), workflow.Seq(subB), workflow.Seq(subC))

	res, err := meta.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Cancelled {
		t.Fatal("正常运行不应 marked cancelled")
	}
	if len(res.Outputs) != 3 {
		t.Fatalf("应有 3 个子代理输出，实际 %d", len(res.Outputs))
	}
	// Each subagent output must contain both step results.
	full := strings.Join(res.Outputs, ";")
	if !strings.Contains(full, "A-1:done") || !strings.Contains(full, "A-2:done") ||
		!strings.Contains(full, "B-1:done") || !strings.Contains(full, "B-2:done") ||
		!strings.Contains(full, "C-1:done") || !strings.Contains(full, "C-2:done") {
		t.Fatalf("汇总应含全部 6 步结果，实际 %q", full)
	}
}

// TestWorkflowPipelineShortCircuit stops after a failing sequential step.
func TestWorkflowPipelineShortCircuit(t *testing.T) {
	runCount := 0
	bad := workflow.Pipeline("bad",
		&workflow.Step{Name: "ok", Run: func(ctx context.Context) (string, error) { return "ok", nil }},
		&workflow.Step{Name: "boom", Run: func(ctx context.Context) (string, error) { return "", errBoom{} }},
		&workflow.Step{Name: "never", Run: func(ctx context.Context) (string, error) { runCount++; return "no", nil }},
	)
	res, err := bad.Run(context.Background())
	if err == nil {
		t.Fatal("串行失败应返回错误")
	}
	if runCount != 0 {
		t.Fatalf("短路后不应执行后续步骤，实际执行 %d 次", runCount)
	}
	// Return only the first two outputs: ok and boom:error.
	if len(res.Outputs) != 2 {
		t.Fatalf("输出应为 2 条(ok + error)，实际 %d", len(res.Outputs))
	}
}

// TestWorkflowCancel cascades cancellation into child steps and sets Cancelled.
func TestWorkflowCancel(t *testing.T) {
	step := &workflow.Step{
		Name: "blocking",
		Run: func(ctx context.Context) (string, error) {
			// Block until canceled.
			<-ctx.Done()
			return "", ctx.Err()
		},
	}
	w := workflow.Parallel("cancel", step, step)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	res, _ := w.Run(ctx)
	if !res.Cancelled {
		t.Fatal("取消后 Result 应 marked Cancelled")
	}
}

// errBoom is a simple test error.
type errBoom struct{}

func (errBoom) Error() string { return "boom" }

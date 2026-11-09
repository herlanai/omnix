// Workflow tutorial. Pipeline runs steps sequentially and stops on the first failure; Parallel
// runs independent steps concurrently and preserves result order; Agent wraps a subagent call as a
// step. The example demonstrates all three and cascading context cancellation. Run: go run
// ./examples/workflow. See pkg/workflow/workflow.go.
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/herlanai/omnix/pkg/workflow"
)

func main() {
	ctx := context.Background()

	// 1. Run three steps sequentially; Result.Outputs preserves their text results in order.
	pipe := workflow.Pipeline("prepare",
		step("拉取代码", 50*time.Millisecond, "repo fetched"),
		step("构建", 60*time.Millisecond, "build ok"),
		step("跑测试", 30*time.Millisecond, "tests pass"),
	)
	res, err := pipe.Run(ctx)
	if err != nil {
		fmt.Printf("pipeline 失败: %v\n", err)
	} else {
		fmt.Println("— 串行 Pipeline 输出（按序）—")
		for _, o := range res.Outputs {
			fmt.Printf("  · %s\n", o)
		}
	}

	// 2. Run three independent steps concurrently. Duration is approximately that of the slowest step,
	// while results retain input order.
	par := workflow.Parallel("fetch-3",
		step("请求A", 120*time.Millisecond, "data A"),
		step("请求B", 90*time.Millisecond, "data B"),
		step("请求C", 150*time.Millisecond, "data C"),
	)
	start := time.Now()
	pres, _ := par.Run(ctx)
	fmt.Printf("\n— 并行 Parallel 输出（并发，总耗时≈最慢一步）—\n")
	for _, o := range pres.Outputs {
		fmt.Printf("  · %s\n", o)
	}
	fmt.Printf("  (并行耗时: %v)\n", time.Since(start).Round(time.Millisecond))

	// 3. Wrap a subagent call with workflow.Agent. A real SpawnAgentFunc runs an in-process, ACP, or
	// fork-copy agent; this closure demonstrates the input/output contract.
	agentFlow := workflow.Pipeline("with-subagent",
		step("准备输入", 20*time.Millisecond, "input-ready"),
		workflow.Agent("审阅", "in-process", "请审阅代码", func(_ context.Context, _ string, input string) (string, error) {
			// Simulate the subagent's output.
			return "subagent 审阅完成: " + input, nil
		}),
	)
	ares, _ := agentFlow.Run(ctx)
	fmt.Printf("\n— 含子代理步骤的工作流 —\n")
	for _, o := range ares.Outputs {
		fmt.Printf("  · %s\n", o)
	}

	// 4. Cancel the context while a long-running workflow is active. Parallel waits for all goroutines
	// to exit and sets Cancelled=true.
	ctx2, cancel := context.WithCancel(context.Background())
	cancelAfter := workflow.Parallel("cancel-demo",
		step("短任务", 40*time.Millisecond, "short done"),
		step("长任务", 800*time.Millisecond, "long done"), // Cancel while execution is in progress.
	)
	go func() { time.Sleep(60 * time.Millisecond); cancel() }()
	cres, cerr := cancelAfter.Run(ctx2)
	// The short task finishes and cancellation interrupts the long task; Parallel still returns a
	// result marked Cancelled.
	fmt.Printf("\n— ctx 取消级联演示 —\n  cancelled=%v, cnt=%d, err=%v\n",
		cres.Cancelled, len(cres.Outputs), cerr)
}

// step constructs a named, timed step that returns text.
func step(name string, dur time.Duration, out string) *workflow.Step {
	return &workflow.Step{
		Name: name,
		Run: func(c context.Context) (string, error) {
			select {
			case <-time.After(dur):
				return out, nil
			case <-c.Done():
				return "", c.Err()
			}
		},
	}
}

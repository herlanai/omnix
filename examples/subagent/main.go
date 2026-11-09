// Subagent tutorial: spawn child tasks and collect their results. Provider abstracts startup;
// built-in acp and fork-copy backends are stubs. ForkLineage records parent/child sessions for
// causal attribution. Handle.Drain waits for completion; Dispose releases resources and cascades
// cancellation. Runtime.Spawn selects a backend and registers the handle; DisposeOwner releases
// all children of a parent. The example covers spawning, lineage, draining, cascading cleanup, and
// stable errors for unknown backends. Run: go run ./examples/subagent. See
// pkg/subagent/subagent.go.
package main

import (
	"context"
	"fmt"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/subagent"
)

func main() {
	ctx := context.Background()

	// 1. Create a top-level parent session with no parent of its own.
	parent := brand.NewSessionID("parent-task")

	// 2. Create the runtime with built-in acp and fork-copy stub providers.
	rt := subagent.NewRuntime()

	// 3. Spawn through a named backend; Spawn returns a Handle and Drain waits for completion.
	fmt.Println("— 派生子代理（内置后端）—")
	for _, backend := range []string{"acp", "fork-copy"} {
		h, err := rt.Spawn(ctx, backend, subagent.SpawnRequest{
			Parent:    &parent,
			Input:     "分析这份需求",
			MaxRounds: 3,
		})
		if err != nil {
			fmt.Printf("  [%s] spawn err: %v\n", backend, err)
			continue
		}
		if err := h.Drain(ctx); err != nil {
			fmt.Printf("  [%s] drain err: %v\n", backend, err)
			continue
		}
		// Inspect the parent-to-child lineage used for causal attribution.
		parentStr := "(顶层)"
		if h.Lineage.Parent != nil {
			parentStr = h.Lineage.Parent.Raw()
		}
		fmt.Printf("  [%s] session=%s | lineage: parent=%s\n",
			backend, h.Lineage.Session.Raw(), parentStr)
	}

	// 4. DisposeOwner releases every handle owned by the parent. Subsequent Drain calls return
	// immediately. Disposal ends handles but does not remove entries from Runtime.handles, so
	// ActiveCount stays unchanged.
	for _, backend := range []string{"acp", "fork-copy"} {
		// Spawn again to demonstrate Drain after disposal.
		h, _ := rt.Spawn(ctx, backend, subagent.SpawnRequest{Parent: &parent, Input: "x"})
		_ = h.Drain(ctx)
	}
	before := rt.ActiveCount()
	rt.DisposeOwner(parent)
	fmt.Printf("\n— 父释放级联：DisposeOwner 后登记数 %d（Drain 均已立即返回）—\n", before)

	// 5. Unknown backends return ErrUnknownProvider rather than panicking, allowing callers to route
	// or display the error.
	if _, err := rt.Spawn(ctx, "does-not-exist", subagent.SpawnRequest{Input: "x"}); err != nil {
		if subagent.IsUnknownProvider(err) {
			fmt.Printf("— 未知后端稳定错误: %v —\n", err)
		} else {
			fmt.Printf("— 其它错误: %v —\n", err)
		}
	}
}

// S02 subagent tests cover in-process fork execution and lineage, child cleanup on parent
// disposal, unknown backends, and built-in ACP/ForkCopy stubs.
package tests

import (
	"context"
	"testing"
	"time"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/subagent"
)

// TestSubagentInProcessForkLineage verifies parent and child session identifiers.
func TestSubagentInProcessForkLineage(t *testing.T) {
	ctx := context.Background()
	parent := brand.NewSessionID("parent-1")
	captured := ""

	r := subagent.NewRuntime()
	// Register an in-process backend that records lineage and returns output.
	rp := subagent.NewInProcessProvider(func(_ context.Context, req subagent.SpawnRequest) (brand.SessionID, string, error) {
		sid := brand.NewSessionID("child-1")
		captured = "parent="
		if req.Parent != nil {
			captured += req.Parent.Raw()
		}
		captured += "/session=" + sid.Raw()
		return sid, "子任务完成", nil
	})
	_ = r.RegisterProvider(rp)

	h, err := r.Spawn(ctx, "in-process", subagent.SpawnRequest{
		Parent: &parent, Input: "fork me",
		MaxRounds: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Verify parent and child lineage.
	if h.Lineage.Parent == nil || *h.Lineage.Parent != parent {
		t.Fatalf("家谱 parent 应为 %s，实际 %v", parent, h.Lineage.Parent)
	}
	if h.Lineage.Session.Raw() != "child-1" {
		t.Fatalf("子会话应为 child-1，实际 %s", h.Lineage.Session)
	}
	if captured != "parent=parent-1/session=child-1" {
		t.Fatalf("runner 收到的家谱上下文异常: %q", captured)
	}
	// Drain returns immediately because in-process execution completed synchronously.
	if err := h.Drain(ctx); err != nil {
		t.Fatal(err)
	}
}

// TestSubagentParentDisposeDrainsChild cleans up child handles on parent disposal.
func TestSubagentParentDisposeDrainsChild(t *testing.T) {
	ctx := context.Background()
	parent := brand.NewSessionID("parent-2")

	r := subagent.NewRuntime()
	rp := subagent.NewInProcessProvider(nil) // Default placeholder runner.
	_ = r.RegisterProvider(rp)

	h, err := r.Spawn(ctx, "in-process", subagent.SpawnRequest{Parent: &parent, Input: "t"})
	if err != nil {
		t.Fatal(err)
	}
	// Release every subagent owned by the parent.
	r.DisposeOwner(parent)
	// Disposed child handles must drain immediately.
	done := make(chan error, 1)
	go func() { done <- h.Drain(ctx) }()
	select {
	case <-done:
		// Drain must not block after disposal.
		_ = h.Drain(ctx)
	case <-time.After(2 * time.Second):
		t.Fatal("父 dispose 后子句柄应能 drain，超时未返回")
	}
}

// TestSubagentUnknownProvider rejects unregistered backends.
func TestSubagentUnknownProvider(t *testing.T) {
	r := subagent.NewRuntime()
	_, err := r.Spawn(context.Background(), "no-such", subagent.SpawnRequest{Input: "x"})
	if !subagent.IsUnknownProvider(err) {
		t.Fatalf("未知后端应报 ErrUnknownProvider，实际 %v", err)
	}
}

// TestSubagentBuiltinStubs exercises built-in ACP and ForkCopy stubs.
func TestSubagentBuiltinStubs(t *testing.T) {
	r := subagent.NewRuntime()
	for _, name := range []string{"acp", "fork-copy"} {
		h, err := r.Spawn(context.Background(), name, subagent.SpawnRequest{Input: "s"})
		if err != nil {
			t.Fatalf("%s 桩 spawn 失败: %v", name, err)
		}
		if h.Provider != name {
			t.Fatalf("%s 句柄 provider 应为 %s，实际 %s", name, name, h.Provider)
		}
	}
	// Two handles are registered: acp and fork-copy.
	if r.ActiveCount() != 2 {
		t.Fatalf("应有 2 个句柄登记，实际 %d", r.ActiveCount())
	}
}

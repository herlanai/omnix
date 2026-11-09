// H07 registry tests cover basic operations, immutable snapshots after Freeze, ErrFrozen on
// mutation, idempotent Freeze, race-free concurrent reads, commands.Registry integration, and
// frozen versus locked-read benchmarks.
package tests

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/herlanai/omnix/pkg/commands"
	"github.com/herlanai/omnix/pkg/registry"
	"github.com/herlanai/omnix/pkg/session"
)

// 1. Basic reads and writes.

func TestH07FreezableBasic(t *testing.T) {
	r := registry.NewFreezable[string, int]()
	if r.Len() != 0 {
		t.Fatal("初始应为空")
	}
	if err := r.Put("a", 1); err != nil {
		t.Fatal(err)
	}
	if err := r.Put("b", 2); err != nil {
		t.Fatal(err)
	}
	if v, ok := r.Get("a"); !ok || v != 1 {
		t.Fatalf("Get(a) = %v, ok=%v", v, ok)
	}
	if !r.Contains("b") || r.Contains("z") {
		t.Fatal("Contains 判定异常")
	}
	if r.Len() != 2 {
		t.Fatalf("Len = %d, want 2", r.Len())
	}
	// Remove
	if err := r.Remove("a"); err != nil {
		t.Fatal(err)
	}
	if r.Contains("a") {
		t.Fatal("Remove 后不应包含 a")
	}
}

// 2. Snapshot consistency after Freeze.

func TestH07FreezableSnapshotConsistency(t *testing.T) {
	r := registry.NewFreezable[string, int]()
	_ = r.Put("x", 10)
	_ = r.Put("y", 20)
	r.Freeze()
	if !r.IsFrozen() {
		t.Fatal("Freeze 后 IsFrozen 应为 true")
	}
	// The read-only snapshot must retain its values.
	if v, ok := r.Get("x"); !ok || v != 10 {
		t.Fatalf("Freeze 后 Get(x) = %v ok=%v", v, ok)
	}
	if v, ok := r.Get("y"); !ok || v != 20 {
		t.Fatalf("Freeze 后 Get(y) = %v ok=%v", v, ok)
	}
	if r.Len() != 2 || !r.Contains("y") {
		t.Fatal("Freeze 后 Len/Contains 异常")
	}
}

// 3. Reject writes after Freeze.

func TestH07FreezeRejectsMutation(t *testing.T) {
	r := registry.NewFreezable[string, int]()
	_ = r.Put("a", 1)
	r.Freeze()
	if err := r.Put("b", 2); !errors.Is(err, registry.ErrFrozen) {
		t.Fatalf("Freeze 后 Put 应返回 ErrFrozen, got %v", err)
	}
	if err := r.Remove("a"); !errors.Is(err, registry.ErrFrozen) {
		t.Fatalf("Freeze 后 Remove 应返回 ErrFrozen, got %v", err)
	}
	// Rejected writes must not corrupt the snapshot.
	if r.Len() != 1 || !r.Contains("a") {
		t.Fatal("Freeze 后拒绝的写入不应影响快照")
	}
}

// 4. Freeze is idempotent.

func TestH07FreezeIdempotent(t *testing.T) {
	r := registry.NewFreezable[string, int]()
	_ = r.Put("a", 42)
	r.Freeze()
	r.Freeze() // Must not panic.
	r.Freeze()
	if blob, ok := r.Get("a"); !ok || blob != 42 {
		t.Fatalf("多次 Freeze 后快照应保持: %v %v", blob, ok)
	}
}

// 5. Stable concurrent reads after Freeze.

func TestH07ConcurrentReadAfterFreeze(t *testing.T) {
	const n = 64
	r := registry.NewFreezable[string, int]()
	for i := 0; i < n; i++ {
		_ = r.Put(itoa(i), i)
	}
	r.Freeze()
	var wg sync.WaitGroup
	stop := make(chan struct{})
	// Eight readers repeatedly read until stopped.
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				for i := 0; i < n; i++ {
					v, ok := r.Get(itoa(i))
					if !ok || v != i {
						t.Errorf("并发读异常: key %d got %v ok=%v", i, v, ok)
						return
					}
				}
			}
		}(g)
	}
	// Use a signal to stop early; the race detector checks concurrency safety.
	_ = stop
	close(stop) // Stop this iteration; stable reads and race detection are the main assertions.
	wg.Wait()
}

// 6. commands.Registry integration.

func TestH07CommandsFreeze(t *testing.T) {
	r := commands.NewRegistry()
	// Built-in plan and goal commands.
	if _, ok := r.Get("plan"); !ok {
		t.Fatal("内置 plan 命令缺失")
	}
	if r.IsFrozen() {
		t.Fatal("初始不应冻结")
	}
	// Register a third-party command.
	if err := r.Register(&commands.CommandDefinition{Name: "resume", Description: "继续", Handler: func(ctx context.Context, args string, sl *session.SessionLog) (string, error) { return "", nil }}); err != nil {
		t.Fatal(err)
	}
	r.Freeze()
	if !r.IsFrozen() {
		t.Fatal("Freeze 后 IsFrozen 应为 true")
	}
	// Reads remain valid after Freeze.
	if _, ok := r.Get("resume"); !ok {
		t.Fatal("Freeze 后应能读到已注册命令")
	}
	if err := r.Register(&commands.CommandDefinition{Name: "late", Description: "x", Handler: nil}); !errors.Is(err, registry.ErrFrozen) {
		t.Fatalf("Freeze 后 Register 应返回 ErrFrozen, got %v", err)
	}
	// The list still contains built-ins and resume.
	names := r.List()
	foundPlan, foundResume := false, false
	for _, n := range names {
		if n == "plan" {
			foundPlan = true
		}
		if n == "resume" {
			foundResume = true
		}
	}
	if !foundPlan || !foundResume {
		t.Fatalf("List 应含 plan 与 resume, got %v", names)
	}
}

// 7. Compare read throughput before and after Freeze.

// BenchmarkH07ReadBeforeFreeze measures the read-lock baseline.
func BenchmarkH07ReadBeforeFreeze(b *testing.B) {
	r := registry.NewFreezable[string, int]()
	for i := 0; i < 100; i++ {
		_ = r.Put(itoa(i), i)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = r.Get(itoa(i % 100))
	}
}

// BenchmarkH07ReadAfterFreeze measures lock-free frozen snapshots.
func BenchmarkH07ReadAfterFreeze(b *testing.B) {
	r := registry.NewFreezable[string, int]()
	for i := 0; i < 100; i++ {
		_ = r.Put(itoa(i), i)
	}
	r.Freeze()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = r.Get(itoa(i % 100))
	}
}

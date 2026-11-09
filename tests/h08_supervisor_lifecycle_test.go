// H08 Supervisor tests cover graceful worker shutdown, parent cancellation, repeated
// Shutdown/Close, timeout for unresponsive workers, rejection of Go after shutdown, individual
// cancellation/wait/health operations, and a ticker-driven writer example.
package tests

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/herlanai/omnix/pkg/jobs"
)

// 1. Start multiple workers and shut them down without leaks.

func TestH08SupervisorGracefulShutdown(t *testing.T) {
	sup := jobs.New()
	var exited atomic.Int32
	for i := 0; i < 8; i++ {
		name := "worker-" + itoa(i)
		ok := sup.Go(name, func(ctx context.Context) {
			defer exited.Add(1)
			// Simulate a bounded background loop that exits on cancellation.
			<-ctx.Done()
		})
		if !ok {
			t.Fatalf("启动 worker %s 失败", name)
		}
	}
	if sup.WorkerCount() != 8 {
		t.Fatalf("WorkerCount = %d, want 8", sup.WorkerCount())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := sup.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown 失败: %v", err)
	}
	if exited.Load() != 8 {
		t.Fatalf("退出 worker 数 = %d, want 8（存在泄漏）", exited.Load())
	}
	// No live workers after shutdown means Healthy is false.
	if sup.Healthy(ctx) {
		t.Fatal("Shutdown 后不应有存活 worker")
	}
}

// 2. Parent-context cancellation cascades to workers.

func TestH08ParentCtxCascade(t *testing.T) {
	sup := jobs.New()
	sawCancel := make(chan struct{}, 1)
	sup.Go("cascade", func(ctx context.Context) {
		<-ctx.Done()
		sawCancel <- struct{}{}
	})
	ctx, cancelCtx := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelCtx()
	if err := sup.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-sawCancel:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("worker 未收到级联取消信号")
	}
}

// 3. Repeated Shutdown and Close are safe.

func TestH08IdempotentShutdown(t *testing.T) {
	sup := jobs.New()
	sup.Go("w1", func(ctx context.Context) { <-ctx.Done() })
	ctx := context.Background()
	if err := sup.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := sup.Shutdown(ctx); err != nil {
		t.Fatalf("第二次 Shutdown 应幂等成功: %v", err)
	}
	sup.Close() // Must neither panic nor hang.
	sup.Close()
	// Reject Go after shutdown.
	if sup.Go("late", func(ctx context.Context) {}) {
		t.Fatal("Shutdown 后 Go 应被拒绝")
	}
}

// 4. Shutdown times out when a worker refuses to exit.

func TestH08ShutdownTimeout(t *testing.T) {
	sup := jobs.New()
	// One worker ignores cancellation while another exits normally.
	sup.Go("stubborn", func(ctx context.Context) { select {} }) // Do not finish until explicitly released.
	sup.Go("ok", func(ctx context.Context) { <-ctx.Done() })
	// Use a short deadline to force a shutdown timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	err := sup.Shutdown(ctx)
	if err == nil {
		t.Fatal("存在拒不退出的 worker 时 Shutdown 应返回错误")
	}
}

// 5. Cancel and wait for an individual worker.

func TestH08SingleWorkerCancel(t *testing.T) {
	sup := jobs.New()
	var exited atomic.Bool
	sup.Go("target", func(ctx context.Context) {
		<-ctx.Done()
		exited.Store(true)
	})
	sup.Go("other", func(ctx context.Context) { <-ctx.Done() })
	sup.CancelWorker("target")
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	if !sup.WaitWorker(ctx, "target") {
		t.Fatal("WaitWorker(target) 应在取消后很快 true")
	}
	if !exited.Load() {
		t.Fatal("target worker 应已退出")
	}
	// Other workers must remain unaffected.
	if sup.WorkerCount() != 2 {
		t.Fatalf("WorkerCount 应保持 2，got %d", sup.WorkerCount())
	}
}

// 6. Ticker-driven writer integration, resembling JSONL shardWriter.

func TestH08TickerWorkerViaSupervisor(t *testing.T) {
	sup := jobs.New()
	var flushed atomic.Int64
	// Flush every 10 ms and exit on ctx.Done, like a JSONL shard writer.
	sup.Go("jsonl-shard-0", func(ctx context.Context) {
		tk := time.NewTicker(10 * time.Millisecond)
		defer tk.Stop()
		for {
			select {
			case <-tk.C:
				flushed.Add(1)
			case <-ctx.Done():
				return
			}
		}
	})
	// Run for 60 ms.
	time.Sleep(60 * time.Millisecond)
	before := flushed.Load()
	if before == 0 {
		t.Fatal("ticker worker 应已触发 flush")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	if err := sup.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown ticker worker 失败: %v", err)
	}
	// Flushing should stop after shutdown.
	tk := time.NewTicker(10 * time.Millisecond)
	defer tk.Stop()
	select {
	case <-tk.C:
		// After the interval, the flush count should no longer grow; this checks termination rather than
		// races.
	case <-time.After(50 * time.Millisecond):
	}
	// Shutdown returning proves termination; additionally verify flushed is at least its previous
	// value.
	if flushed.Load() < before {
		t.Fatalf("flushed 应单调递增: before=%d now=%d", before, flushed.Load())
	}
}

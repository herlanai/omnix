// S11/M46 background-job tests cover startup, incremental output, settlement, cancellation,
// owner-bound cleanup on Agent disposal, and both manual hooks and subprocess backends.
package tests

import (
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/jobs"
)

// TestJobsIncrementalOutput verifies incremental output reads (S11).
func TestJobsIncrementalOutput(t *testing.T) {
	var argv []string
	if runtime.GOOS == "windows" {
		argv = []string{"powershell.exe", "-NoProfile", "-Command", "1..10 | ForEach-Object { Write-Output $_ ; Start-Sleep -Milliseconds 100 }"}
	} else {
		argv = []string{"sh", "-c", "for i in 1 2 3 4 5 6 7 8 9 10; do echo $i; sleep 0.1; done"}
	}
	rt := jobs.NewRuntime()
	job, err := rt.StartSubprocess(jobs.SubprocessSpec{
		Kind:           jobs.KindBash,
		Label:          "seq 1 10",
		OwnerSession:   brand.NewSessionID("owner1"),
		Argv:           argv,
		Cwd:            t.TempDir(),
		StdoutMaxBytes: 65536,
	})
	if err != nil {
		t.Fatal(err)
	}
	var got strings.Builder
	for i := 0; i < 50; i++ {
		if t.Failed() {
			break
		}
		got.WriteString(job.ReadOutput())
		select {
		case out := <-job.Done():
			got.WriteString(job.ReadOutput())
			if out.Status != jobs.StatusCompleted {
				t.Fatalf("应 completed, 实际 %+v", out)
			}
		default:
		}
		if strings.Contains(got.String(), "10") {
			break
		}
		<-time.After(40 * time.Millisecond)
	}
	if !strings.Contains(got.String(), "10") {
		t.Fatalf("应能读到最终输出 10, 实际 %q", got.String())
	}
	if job.ID == (brand.JobID{}) {
		t.Fatal("应有任务 id")
	}
}

// TestJobsListByOwner lists jobs by owner.
func TestJobsListByOwner(t *testing.T) {
	rt := jobs.NewRuntime()
	for i := 0; i < 3; i++ {
		_, _ = rt.Start(manualStart(jobs.KindBash, "ownerX", func() {}))
	}
	_, _ = rt.Start(manualStart(jobs.KindBash, "ownerY", func() {}))
	if got := len(rt.ListByOwner(brand.NewSessionID("ownerX"))); got != 3 {
		t.Fatalf("ownerX 应有 3 个任务, 实际 %d", got)
	}
}

// TestJobsOwnerDisposeCancels cancels all jobs owned by a disposed Agent (M46).
func TestJobsOwnerDisposeCancels(t *testing.T) {
	owner := brand.NewSessionID("owner-z")
	rt := jobs.NewRuntime()
	cancelled := make(chan struct{}, 2)

	// Create two persistent jobs and count cancel-hook calls.
	_, _ = rt.Start(manualStart(jobs.KindBash, owner.Raw(), func() { cancelled <- struct{}{} }))
	_, _ = rt.Start(manualStart(jobs.KindBash, owner.Raw(), func() { cancelled <- struct{}{} }))

	// Dispose the owner, cancel every job, and wait for completion.
	done := make(chan struct{})
	go func() {
		rt.DisposeOwner(owner, "owner disposed")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("DisposeOwner 未在超时内完成")
	}
	// Both jobs must receive cancellation.
	count := 0
	for i := 0; i < 2; i++ {
		select {
		case <-cancelled:
			count++
		case <-time.After(time.Second):
		}
	}
	if count != 2 {
		t.Fatalf("dispose 应 cancel 全部 2 个任务, 实际 %d", count)
	}
	// Completed jobs are removed from the registry.
	if jobs := rt.ListByOwner(owner); len(jobs) != 0 {
		t.Fatalf("dispose 后 owner 名下列表应为空, 实际 %d", len(jobs))
	}
}

// TestJobsOwnerDisposeSubprocessCleanup terminates subprocess trees during owner disposal.
func TestJobsOwnerDisposeSubprocessCleanup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("依赖 sh")
	}
	owner := brand.NewSessionID("owner-sp")
	rt := jobs.NewRuntime()
	_, err := rt.StartSubprocess(jobs.SubprocessSpec{
		Kind:         jobs.KindBash,
		Label:        "sleep long",
		OwnerSession: owner,
		Argv:         []string{"sh", "-c", "sleep 1000"},
		Cwd:          t.TempDir(),
		GraceMs:      100,
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { rt.DisposeOwner(owner, "dispose"); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("dispose 子进程任务超时")
	}
	if len(rt.ListByOwner(owner)) != 0 {
		t.Fatal("dispose 后子进程任务应从 registry 移除")
	}
}

// manualStart constructs a job starter with a manual cancellation hook.
func manualStart(kind jobs.JobKind, ownerID string, onCancel func()) jobs.JobStart {
	owner := brand.NewSessionID(ownerID)
	done := make(chan jobs.JobOutcome, 1)
	return jobs.JobStart{
		Kind:         kind,
		Label:        "manual",
		OwnerSession: owner,
		Run: func() *jobs.JobHooks {
			return &jobs.JobHooks{
				// Cancel increments the counter and settles the job as killed so DisposeOwner can finish waiting.
				Cancel: func(reason string) {
					onCancel()
					select {
					case done <- jobs.JobOutcome{Status: jobs.StatusKilled, Detail: "cancelled"}:
					default:
					}
				},
				Done:       done,
				ReadOutput: func() string { return "" },
			}
		},
	}
}

// M36 subprocess tests cover removal of OMNIX_* environment entries, truncated output with
// complete spill recovery, process-tree termination and settlement, and exit codes.
package tests

import (
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/herlanai/omnix/pkg/subprocess"
)

// envHelper builds a cross-platform command reporting whether OMNIX_* variables are absent.
func envCheckArgv(varName string) []string {
	if runtime.GOOS == "windows" {
		// if defined prints the status and exits successfully.
		return []string{"cmd", "/c", "if defined " + varName + " (echo PRESENT) else (echo EMPTY)"}
	}
	return []string{"sh", "-c", `if [ -z "$` + varName + `" ]; then echo EMPTY; else echo PRESENT; fi`}
}

// TestSubprocessScrubEnv removes OMNIX_* variables from child environments.
func TestSubprocessScrubEnv(t *testing.T) {
	t.Setenv("OMNIX_TEST_FACT", "secret")
	rt := subprocess.NewLocal()
	h := rt.Spawn(subprocess.SubprocessSpawnSpec{
		Argv: envCheckArgv("OMNIX_TEST_FACT"),
		Cwd:  t.TempDir(),
	})
	out := h.Collected().Stdout()
	res := <-h.Done()
	if res.ExitCode != 0 {
		t.Fatalf("子进程退出码应 0, 实际 %d", res.ExitCode)
	}
	co := out.ReadOutput(0)
	if !strings.Contains(co.Text, "EMPTY") {
		t.Fatalf("OMNIX_* 被 scrub 后子进程应读到空: %q", co.Text)
	}
}

// TestSubprocessCollectedSpill truncates oversized collected output while retaining the complete
// spill stream.
func TestSubprocessCollectedSpill(t *testing.T) {
	var argv []string
	if runtime.GOOS == "windows" {
		argv = []string{"cmd", "/c", "for /L %i in (1,1,3000) do @echo line of output no %i"}
	} else {
		argv = []string{"sh", "-c", "seq 1 3000"}
	}
	rt := subprocess.NewLocal()
	h := rt.Spawn(subprocess.SubprocessSpawnSpec{
		Argv:           argv,
		Cwd:            t.TempDir(),
		StdoutMaxBytes: 1024, // Trigger truncation.
		SpillRoot:      t.TempDir(),
	})
	res := <-h.Done()
	if res.ExitCode != 0 {
		t.Fatalf("退出码应 0, 实际 %d", res.ExitCode)
	}
	co := h.Collected().Stdout().ReadOutput(0)
	if !co.Truncated {
		t.Fatal("输出超过阈值应 truncated")
	}
	if co.SpillPath == "" {
		t.Fatal("启用 spill 时应生成 spill 路径")
	}
	// Read the spill file directly to verify the complete output.
	data := readSpill(t, co.SpillPath)
	if len(data) <= 1024 {
		t.Fatalf("spill 应含完整流(>1KB), 实际 %d 字节", len(data))
	}
}

func readSpill(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读 spill 失败: %v", err)
	}
	return b
}

// TestSubprocessTerminate settles Done after process-tree termination and reaping.
func TestSubprocessTerminate(t *testing.T) {
	var argv []string
	if runtime.GOOS == "windows" {
		argv = []string{"cmd", "/c", "ping -n 60 127.0.0.1 >nul"}
	} else {
		argv = []string{"sh", "-c", "sleep 1000"}
	}
	rt := subprocess.NewLocal()
	h := rt.Spawn(subprocess.SubprocessSpawnSpec{Argv: argv, Cwd: t.TempDir(), GraceMs: 100})
	h.Terminate()
	select {
	case res := <-h.Done():
		// Forced termination must produce a nonzero exit code.
		if res.ExitCode == 0 {
			t.Fatalf("终止后退出码不应为 0, 实际 %+v", res)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("terminate 后 Done 未在超时内结算")
	}
}

// TestSubprocessOutcomeSuccess reports exit code zero for normal completion.
func TestSubprocessOutcomeSuccess(t *testing.T) {
	var argv []string
	if runtime.GOOS == "windows" {
		argv = []string{"cmd", "/c", "exit 0"}
	} else {
		argv = []string{"sh", "-c", "exit 0"}
	}
	rt := subprocess.NewLocal()
	h := rt.Spawn(subprocess.SubprocessSpawnSpec{Argv: argv, Cwd: t.TempDir()})
	res := <-h.Done()
	if res.ExitCode != 0 {
		t.Fatalf("正常退出码应 0, 实际 %+v", res)
	}
}

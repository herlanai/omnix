// M37 shell tests cover truncating large output at the default 64 KB while retaining all 100000
// lines in spill storage, an independent timedOut flag, and normal exitCode=0 with timeoutMs=0.
package tests

import (
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/herlanai/omnix/pkg/shell"
)

// bigSeqCmd constructs a command that emits many lines.
func bigSeqCmd() string {
	if runtime.GOOS == "windows" {
		return "for /L %i in (1,1,20000) do @echo line %i"
	}
	return "seq 1 100000"
}

// TestBashSpillFullContent verifies 64 KB truncation and complete spill content.
func TestBashSpillFullContent(t *testing.T) {
	s := shell.New("workspace-write")
	res, br, err := s.Run(shell.ExecRequest{Command: bigSeqCmd(), Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("正常命令应 exitCode=0, 实际 %+v", res)
	}
	if res.TimeoutMs != 0 {
		t.Fatalf("未设超时应 timeoutMs=0, 实际 %d", res.TimeoutMs)
	}
	if !br.Truncated {
		t.Fatal("大输出应被截断")
	}
	if br.SpillPath == "" {
		t.Fatal("截断时应提供 spillPath")
	}
	// Read the full output from spill storage.
	data, err := os.ReadFile(br.SpillPath)
	if err != nil {
		t.Fatalf("读 spill 失败: %v", err)
	}
	rows := strings.Count(string(data), "\n")
	want := 100000
	if runtime.GOOS == "windows" {
		want = 20000
	}
	if rows < want-10 {
		t.Fatalf("spill 应含约 %d 行, 实际 %d 行", want, rows)
	}
	// Model-facing output must direct the caller to the spill file.
	formatted := shell.FormatOutput(&br)
	if !strings.Contains(formatted, "full output at") {
		t.Fatalf("格式输出应引导读 spill: %q", formatted)
	}
}

// TestBashTimeout verifies timedOut=true on timeout.
func TestBashTimeout(t *testing.T) {
	s := shell.New("workspace-write")
	var cmd string
	if runtime.GOOS == "windows" {
		cmd = "ping -n 30 127.0.0.1 >nul"
	} else {
		cmd = "sleep 30"
	}
	res, br, err := s.Run(shell.ExecRequest{Command: cmd, Cwd: t.TempDir(), TimeoutMs: 500})
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut {
		t.Fatalf("应 timedOut=true, 实际 %+v", res)
	}
	if res.TimeoutMs != 500 {
		t.Fatalf("timeoutMs 应记录 500, 实际 %d", res.TimeoutMs)
	}
	_ = br
}

// TestBashOutputLines returns short output inline without truncation.
func TestBashOutputLines(t *testing.T) {
	s := shell.New("workspace-write")
	var cmd string
	if runtime.GOOS == "windows" {
		cmd = "echo hello"
	} else {
		cmd = "echo hello"
	}
	_, br, err := s.Run(shell.ExecRequest{Command: cmd, Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if br.Truncated {
		t.Fatal("短输出不应截断")
	}
	if !strings.Contains(br.Output, "hello") {
		t.Fatalf("输出应含 hello, 实际 %q", br.Output)
	}
}

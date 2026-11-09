// Local backend profile tests cover bwrap arguments, Landlock access arguments, and macOS
// sandbox-exec SBPL scripts.
package tests

import (
	"runtime"
	"strings"
	"testing"

	"github.com/herlanai/omnix/pkg/sandbox"
)

// TestBwrapProfileReadOnly verifies the read-only bwrap profile.
func TestBwrapProfileReadOnly(t *testing.T) {
	policy := sandbox.SandboxPolicy{
		SandboxExecutionPolicy: sandbox.SandboxExecutionPolicy{
			Mode:          sandbox.ModeReadOnly,
			WorkspaceRoot: "/workspace",
		},
		Mode: sandbox.ConfinedReadOnly,
	}
	args := sandbox.BwrapProfileArgs(policy)
	// Include the baseline arguments.
	expected := []string{"--ro-bind", "/", "/", "--dev", "/dev", "--unshare-pid", "--proc", "/proc", "--die-with-parent"}
	for i, e := range expected {
		if args[i] != e {
			t.Fatalf("参数 %d 应为 %q, 实际 %q", i, e, args[i])
		}
	}
	// read-only must not bind the workspace as writable.
	for _, a := range args {
		if a == "--bind" {
			t.Fatal("read-only 不应包含 --bind")
		}
	}
}

// TestBwrapProfileWorkspaceWrite verifies the writable-workspace bwrap profile.
func TestBwrapProfileWorkspaceWrite(t *testing.T) {
	policy := sandbox.SandboxPolicy{
		SandboxExecutionPolicy: sandbox.SandboxExecutionPolicy{
			Mode:          sandbox.ModeWorkspaceWrite,
			WorkspaceRoot: "/workspace/app",
		},
		Mode: sandbox.ConfinedWorkspaceWrite,
	}
	args := sandbox.BwrapProfileArgs(policy)
	// Include --tmpfs /tmp.
	foundTmpfs := false
	foundBind := false
	for i, a := range args {
		if a == "--tmpfs" && i+1 < len(args) && args[i+1] == "/tmp" {
			foundTmpfs = true
		}
		if a == "--bind" && i+1 < len(args) && args[i+1] == "/workspace/app" {
			foundBind = true
		}
	}
	if !foundTmpfs {
		t.Fatal("workspace-write 应包含 --tmpfs /tmp")
	}
	if !foundBind {
		t.Fatal("workspace-write 应包含 --bind workspaceRoot")
	}
}

// TestLandlockProfileReadOnly verifies the read-only Landlock profile.
func TestLandlockProfileReadOnly(t *testing.T) {
	policy := sandbox.SandboxPolicy{
		SandboxExecutionPolicy: sandbox.SandboxExecutionPolicy{
			Mode:          sandbox.ModeReadOnly,
			WorkspaceRoot: "/workspace",
		},
		Mode: sandbox.ConfinedReadOnly,
	}
	args := sandbox.LandlockProfileArgs(policy)
	// Allow read-only access to / and write access to /dev/null.
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--read-only /") {
		t.Fatalf("应包含 --read-only /, 实际: %s", joined)
	}
	if !strings.Contains(joined, "--read-write /dev/null") {
		t.Fatalf("应包含 --read-write /dev/null, 实际: %s", joined)
	}
	// Do not include workspaceRoot among writable roots in read-only mode.
	if strings.Contains(joined, "--read-write /workspace") {
		t.Fatalf("read-only 不应在 read-write 中包含 workspace, 实际: %s", joined)
	}
}

// TestLandlockProfileWorkspaceWrite verifies the writable-workspace Landlock profile.
func TestLandlockProfileWorkspaceWrite(t *testing.T) {
	policy := sandbox.SandboxPolicy{
		SandboxExecutionPolicy: sandbox.SandboxExecutionPolicy{
			Mode:          sandbox.ModeWorkspaceWrite,
			WorkspaceRoot: "/workspace/app",
		},
		Mode: sandbox.ConfinedWorkspaceWrite,
	}
	args := sandbox.LandlockProfileArgs(policy)
	joined := strings.Join(args, " ")
	// Writable roots must include /dev/null, /tmp, and workspaceRoot.
	if !strings.Contains(joined, "--read-write /dev/null") {
		t.Fatalf("应包含 --read-write /dev/null, 实际: %s", joined)
	}
	if !strings.Contains(joined, "--read-write /tmp") {
		t.Fatalf("应包含 --read-write /tmp, 实际: %s", joined)
	}
	if !strings.Contains(joined, "--read-write /workspace/app") {
		t.Fatalf("应包含 --read-write /workspace/app, 实际: %s", joined)
	}
}

// TestSeatbeltProfileReadOnly verifies the read-only SBPL script.
func TestSeatbeltProfileReadOnly(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("seatbelt 仅在 macOS 上有意义")
	}
	policy := sandbox.SandboxPolicy{
		SandboxExecutionPolicy: sandbox.SandboxExecutionPolicy{
			Mode:          sandbox.ModeReadOnly,
			WorkspaceRoot: "/workspace",
		},
		Mode: sandbox.ConfinedReadOnly,
	}
	args := sandbox.SeatbeltProfileArgs(policy)
	// The first argument must be -p.
	if args[0] != "-p" {
		t.Fatalf("第一个参数应为 -p, 实际 %q", args[0])
	}
	// The SBPL script must include baseline rules.
	sbpl := args[1]
	if !strings.Contains(sbpl, "(version 1)") {
		t.Fatalf("SBPL 应包含 (version 1), 实际: %s", sbpl)
	}
	if !strings.Contains(sbpl, "(allow default)") {
		t.Fatalf("SBPL 应包含 (allow default), 实际: %s", sbpl)
	}
	if !strings.Contains(sbpl, "(deny file-write*)") {
		t.Fatalf("SBPL 应包含 (deny file-write*), 实际: %s", sbpl)
	}
	if !strings.Contains(sbpl, "/dev/null") {
		t.Fatalf("SBPL 应包含 /dev/null, 实际: %s", sbpl)
	}
}

// TestSeatbeltProfileWorkspaceWrite verifies the writable-workspace SBPL script.
func TestSeatbeltProfileWorkspaceWrite(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("seatbelt 仅在 macOS 上有意义")
	}
	policy := sandbox.SandboxPolicy{
		SandboxExecutionPolicy: sandbox.SandboxExecutionPolicy{
			Mode:          sandbox.ModeWorkspaceWrite,
			WorkspaceRoot: "/workspace/app",
		},
		Mode: sandbox.ConfinedWorkspaceWrite,
	}
	args := sandbox.SeatbeltProfileArgs(policy)
	sbpl := args[1]
	// Authorize the workspaceRoot subpath for writing.
	if !strings.Contains(sbpl, "/workspace/app") {
		t.Fatalf("SBPL 应包含 workspaceRoot, 实际: %s", sbpl)
	}
	if !strings.Contains(sbpl, "(subpath") {
		t.Fatalf("SBPL 应包含 (subpath ...) 授权, 实际: %s", sbpl)
	}
}

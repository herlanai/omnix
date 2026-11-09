// Sandbox invariant tests.
package tests

import (
	"testing"

	"github.com/herlanai/omnix/pkg/sandbox"
)

// TestInvariantValidMode accepts valid modes without panic.
func TestInvariantValidMode(t *testing.T) {
	sandbox.InvariantEnabled = true
	defer func() { sandbox.InvariantEnabled = true }()
	// Must not panic.
	sandbox.AssertValidMode(sandbox.ModeReadOnly)
	sandbox.AssertValidMode(sandbox.ModeWorkspaceWrite)
	sandbox.AssertValidMode(sandbox.ModeDangerFullAccess)
}

// TestInvariantInvalidMode panics for an invalid mode.
func TestInvariantInvalidMode(t *testing.T) {
	sandbox.InvariantEnabled = true
	defer func() { sandbox.InvariantEnabled = true }()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("非法模式应 panic")
		}
	}()
	sandbox.AssertValidMode(sandbox.SandboxMode("invalid"))
}

// TestInvariantExecutionPolicy checks policy consistency.
func TestInvariantExecutionPolicy(t *testing.T) {
	sandbox.InvariantEnabled = true
	defer func() { sandbox.InvariantEnabled = true }()
	// workspace-write requires a root.
	sandbox.AssertExecutionPolicy(sandbox.SandboxExecutionPolicy{
		Mode:          sandbox.ModeWorkspaceWrite,
		WorkspaceRoot: "/workspace",
	})
	// read-only may omit the root.
	sandbox.AssertExecutionPolicy(sandbox.SandboxExecutionPolicy{
		Mode: sandbox.ModeReadOnly,
	})
}

// TestInvariantExecutionPolicyNoRoot panics for workspace-write without a root.
func TestInvariantExecutionPolicyNoRoot(t *testing.T) {
	sandbox.InvariantEnabled = true
	defer func() { sandbox.InvariantEnabled = true }()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("workspace-write 无 root 应 panic")
		}
	}()
	sandbox.AssertExecutionPolicy(sandbox.SandboxExecutionPolicy{
		Mode: sandbox.ModeWorkspaceWrite,
	})
}

// TestInvariantDisabled skips validation when disabled.
func TestInvariantDisabled(t *testing.T) {
	sandbox.InvariantEnabled = false
	defer func() { sandbox.InvariantEnabled = true }()
	// Must not panic when disabled.
	sandbox.AssertValidMode(sandbox.SandboxMode("invalid"))
	sandbox.AssertExecutionPolicy(sandbox.SandboxExecutionPolicy{
		Mode: sandbox.ModeWorkspaceWrite,
	})
}

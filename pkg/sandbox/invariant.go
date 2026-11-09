// Sandbox invariants follow upstream sandbox, sandbox-policy, and sandbox-local
// invariant modules. InvariantEnabled controls checks; failures panic with
// package attribution. Checks cover valid modes, consistent policy fields,
// workspace roots, forbidden danger-mode confinement, escalation targets,
// and nonempty confined argument vectors.
package sandbox

import (
	"fmt"
	"strings"
)

// InvariantEnabled defaults to true for development checks.
// Production callers may set it false to skip validation.
var InvariantEnabled = true

// invariantError identifies a failed invariant and its owning package.
type invariantError struct {
	pkg     string
	message string
}

func (e *invariantError) Error() string {
	return fmt.Sprintf("[%s invariant] %s", e.pkg, e.message)
}

// assert panics when an enabled invariant condition fails.
func assert(condition bool, format string, args ...any) {
	if !InvariantEnabled {
		return
	}
	if !condition {
		panic(&invariantError{pkg: "sandbox", message: fmt.Sprintf(format, args...)})
	}
}

// AssertValidMode validates a sandbox mode.
func AssertValidMode(mode SandboxMode) {
	assert(mode == ModeReadOnly || mode == ModeWorkspaceWrite || mode == ModeDangerFullAccess,
		"invalid sandbox mode %q (expected read-only, workspace-write, or danger-full-access)", mode)
}

// AssertExecutionPolicy checks execution-policy field consistency.
func AssertExecutionPolicy(policy SandboxExecutionPolicy) {
	AssertValidMode(policy.Mode)
	// Workspace-write requires a nonempty workspace root.
	if policy.Mode == ModeWorkspaceWrite {
		assert(strings.TrimSpace(policy.WorkspaceRoot) != "",
			"workspace-write mode requires non-empty workspaceRoot")
	}
}

// AssertConfinedPolicy validates fields of a confined SandboxPolicy.
func AssertConfinedPolicy(policy SandboxPolicy) {
	assert(policy.Mode == ConfinedReadOnly || policy.Mode == ConfinedWorkspaceWrite,
		"confined policy mode must be read-only or workspace-write, got %q", policy.Mode)
	if policy.Mode == ConfinedWorkspaceWrite {
		assert(strings.TrimSpace(policy.WorkspaceRoot) != "",
			"workspace-write confined policy requires non-empty workspaceRoot")
	}
}

// AssertNotDangerBeforeConfine rejects danger mode before ToConfined.
func AssertNotDangerBeforeConfine(mode SandboxMode) {
	assert(mode != ModeDangerFullAccess,
		"cannot confine danger-full-access policy (must bypass sandbox and spawn raw argv)")
}

// AssertEscalationTarget validates membership in the closed escalation target set.
func AssertEscalationTarget(target SandboxMode) {
	assert(target == ModeWorkspaceWrite || target == ModeDangerFullAccess,
		"escalation target %q is not in closed target vocabulary [workspace-write, danger-full-access]", target)
}

// AssertConfinedArgv checks that the provider returned a valid, nonempty command.
func AssertConfinedArgv(result ConfinedArgv) {
	assert(len(result.Argv) > 0, "confined argv must not be empty")
	assert(result.Enforcement == EnforcementFull || result.Enforcement == EnforcementPartial,
		"invalid enforcement level %q (expected full or partial)", result.Enforcement)
}

// AssertWritableRoots requires roots for workspace-write and none for other modes.
func AssertWritableRoots(mode SandboxMode, roots []string) {
	if mode == ModeWorkspaceWrite {
		assert(len(roots) > 0, "workspace-write mode should have non-empty writable roots")
	} else {
		assert(len(roots) == 0, "mode %q should have empty writable roots, got %v", mode, roots)
	}
}

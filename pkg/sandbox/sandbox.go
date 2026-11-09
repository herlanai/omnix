// Package sandbox defines process confinement, following upstream sandbox and
// sandbox-policy (M26). Modes constrain filesystem effects only. Each call
// resolves a complete execution policy, including workspaceRoot even in danger
// mode. Bash and filesystem consumers share policy resolution to keep mode
// precedence and root fallback consistent. Confine applies only to constrained
// modes and must fail closed with SANDBOX_UNAVAILABLE if no backend exists.
package sandbox

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/herlanai/omnix/pkg/brand"
)

// Modes and enforcement levels.

// SandboxMode constrains filesystem side effects.
type SandboxMode string

// Modes match pkg/session.SandboxMode.
const (
	ModeReadOnly         SandboxMode = "read-only"
	ModeWorkspaceWrite   SandboxMode = "workspace-write"
	ModeDangerFullAccess SandboxMode = "danger-full-access"
)

// ConfinedSandboxMode excludes danger-full-access. Danger-mode consumers
// execute their original argv directly without calling the sandbox provider.
type ConfinedSandboxMode string

// Supported confined modes.
const (
	ConfinedReadOnly       ConfinedSandboxMode = "read-only"
	ConfinedWorkspaceWrite ConfinedSandboxMode = "workspace-write"
)

// SandboxEnforcement reports actual coverage. Full covers all filesystem
// effects promised by the mode; partial covers only a subset because of backend
// or kernel limitations. Consumers needing strict boundaries must reject partial.
type SandboxEnforcement string

// Enforcement levels.
const (
	EnforcementFull    SandboxEnforcement = "full"
	EnforcementPartial SandboxEnforcement = "partial"
)

// Execution policy.

// SandboxExecutionPolicy is the resolved filesystem policy for one capability
// call. It retains workspaceRoot in danger mode so consumers resolve once
// before choosing the execution path.
type SandboxExecutionPolicy struct {
	// Mode selects filesystem effects for this execution.
	Mode SandboxMode `json:"mode"`
	// WorkspaceRoot is the absolute writable workspace root.
	WorkspaceRoot string `json:"workspaceRoot"`
	// SessionID identifies the calling session; empty gives agentless calls per-call backend state.
	SessionID brand.SessionID `json:"sessionId,omitempty"`
}

// IsDanger reports whether to bypass confinement and spawn the original argv.
func (p SandboxExecutionPolicy) IsDanger() bool {
	return p.Mode == ModeDangerFullAccess
}

// SandboxPolicy carries the boundary for each confined call, not fixed backend state.
type SandboxPolicy struct {
	SandboxExecutionPolicy
	// Mode is restricted to read-only or workspace-write.
	Mode ConfinedSandboxMode `json:"mode"`
}

// Policy resolution service.

// Session exposes the immutable working directory as the workspace boundary
// and the last logged sandbox mode as a session override.
type Session interface {
	// ID returns the branded session identifier.
	ID() brand.SessionID
	// Cwd returns the immutable workspace boundary, or empty if absent.
	Cwd() string
	// SandboxMode returns the latest logged override, with ok false if absent.
	SandboxMode() (SandboxMode, bool)
}

// SandboxPolicyRequest supplies inputs for resolving one capability call's policy.
type SandboxPolicyRequest struct {
	// Session supplies the immutable working-directory boundary.
	Session Session
	// Mode is an explicit approved override taking precedence over session policy.
	Mode *SandboxMode
}

// SandboxPolicyService implements ctx.sandboxPolicy, sharing mode precedence
// and root fallback between Bash and filesystem consumers.
type SandboxPolicyService struct {
	// DefaultMode is the deployment default, including agentless calls.
	DefaultMode SandboxMode
	// FallbackRoot is the workspace root for agentless sessions or missing cwd.
	FallbackRoot string
}

// NewPolicyService creates a policy resolver.
func NewPolicyService(defaultMode SandboxMode, fallbackRoot string) *SandboxPolicyService {
	return &SandboxPolicyService{DefaultMode: defaultMode, FallbackRoot: fallbackRoot}
}

// OverrideOf reads only the session override, returning ok false rather than applying defaults.
func (s *SandboxPolicyService) OverrideOf(session Session) (SandboxMode, bool) {
	if session == nil {
		return "", false
	}
	return session.SandboxMode()
}

// Resolve selects a complete policy for one call. Precedence is explicit
// req.Mode, latest logged session mode, then deployment default. The root is
// the canonical session cwd or the configured agentless fallback.
func (s *SandboxPolicyService) Resolve(req SandboxPolicyRequest) SandboxExecutionPolicy {
	mode := s.DefaultMode
	if req.Session != nil {
		if m, ok := req.Session.SandboxMode(); ok {
			mode = m
		}
	}
	if req.Mode != nil {
		mode = *req.Mode
	}

	// Prefer the session cwd, otherwise use the fallback root.
	root := s.FallbackRoot
	if req.Session != nil {
		if cwd := req.Session.Cwd(); cwd != "" {
			root = cwd
		}
	}
	root = canonicalize(root)

	var sid brand.SessionID
	if req.Session != nil {
		sid = req.Session.ID()
	}
	return SandboxExecutionPolicy{
		Mode:          mode,
		WorkspaceRoot: root,
		SessionID:     sid,
	}
}

// ToConfined converts execution policy after the caller has excluded danger mode.
func (p SandboxExecutionPolicy) ToConfined() (SandboxPolicy, error) {
	switch p.Mode {
	case ModeReadOnly:
		return SandboxPolicy{SandboxExecutionPolicy: p, Mode: ConfinedReadOnly}, nil
	case ModeWorkspaceWrite:
		return SandboxPolicy{SandboxExecutionPolicy: p, Mode: ConfinedWorkspaceWrite}, nil
	default:
		return SandboxPolicy{}, fmt.Errorf("sandbox: cannot confine danger-full-access policy %+v", p)
	}
}

// canonicalize resolves filesystem paths, then lexically cleans the absolute root.
func canonicalize(root string) string {
	if root == "" {
		return root
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return filepath.Clean(root)
	}
	return filepath.Clean(abs)
}

// Provider interface and confined commands.

// RunnerFailureRule identifies runner failure before the command executes.
type RunnerFailureRule struct {
	// AllowedExitCodes optionally restricts matching to listed nonzero exit codes.
	AllowedExitCodes []int `json:"allowedExitCodes,omitempty"`
	// FatalSignatures are case-insensitive diagnostic substrings on individual stderr lines.
	FatalSignatures []string `json:"fatalSignatures"`
	// InformationalLines excludes harmless case-insensitive whole-line matches before fatal checks.
	InformationalLines []string `json:"informationalLines,omitempty"`
}

// Matches checks the exit code, excludes informational lines, then searches for fatal substrings.
func (r RunnerFailureRule) Matches(exitCode int, stderr string) (string, bool) {
	if exitCode == 0 {
		return "", false
	}
	if len(r.AllowedExitCodes) > 0 {
		allowed := false
		for _, c := range r.AllowedExitCodes {
			if c == exitCode {
				allowed = true
				break
			}
		}
		if !allowed {
			return "", false
		}
	}
	// Remove informational lines using case-insensitive whole-line matching.
	info := map[string]bool{}
	for _, l := range r.InformationalLines {
		info[strings.ToLower(l)] = true
	}
	var remaining []string
	for _, line := range strings.Split(stderr, "\n") {
		if !info[strings.ToLower(strings.TrimSpace(line))] {
			remaining = append(remaining, line)
		}
	}
	// Match fatal substrings within each remaining line.
	joined := strings.Join(remaining, "\n")
	lower := strings.ToLower(joined)
	for _, sig := range r.FatalSignatures {
		if strings.Contains(lower, strings.ToLower(sig)) {
			return sig, true
		}
	}
	return "", false
}

// ConfinedArgv contains wrapped argv, achieved enforcement, and independent
// stderr classifiers for denials and runner failures.
type ConfinedArgv struct {
	// Argv combines runner, profile, separator, and original command arguments.
	Argv []string `json:"argv"`
	// Enforcement reports the backend's coverage for this policy.
	Enforcement SandboxEnforcement `json:"enforcement"`
	// DenialSignatures contains backend-specific stderr denial substrings.
	DenialSignatures []string `json:"denialSignatures"`
	// RunnerFailureRules contains structured evidence of runner failure.
	RunnerFailureRules []RunnerFailureRule `json:"runnerFailureRules"`
}

// SandboxProvider abstracts process confinement through ctx.sandbox.
type SandboxProvider interface {
	// Confine wraps argv under the policy and must fail closed on missing backends
	// or wrapping errors; it must never silently execute without confinement.
	Confine(argv []string, policy SandboxPolicy) (ConfinedArgv, error)
}

// SandboxUnavailableErrorCode is the stable unavailable-backend error code.
const SandboxUnavailableErrorCode = "SANDBOX_UNAVAILABLE"

// SandboxUnavailableError indicates no usable backend.
type SandboxUnavailableError struct {
	Msg string
}

func (e *SandboxUnavailableError) Error() string {
	return fmt.Sprintf("%s: %s", SandboxUnavailableErrorCode, e.Msg)
}

// UnavailableProvider always fails closed with SANDBOX_UNAVAILABLE and is
// the production fallback when no backend is available.
type UnavailableProvider struct{}

// Confine always reports unavailable, enforcing fail-closed behavior.
func (*UnavailableProvider) Confine([]string, SandboxPolicy) (ConfinedArgv, error) {
	return ConfinedArgv{}, &SandboxUnavailableError{Msg: "no usable sandbox backend"}
}

// This file implements sandbox escalation, following upstream escalation.ts.
// Read-only may widen to workspace-write or danger-full-access; workspace-write
// may widen only to danger-full-access, which cannot widen further. Schemas
// expose the closed target set; runtime checks compare the actual effective
// mode. sandbox_permissions and nonempty justification must appear together.
// Approval fails closed: check widening first, request approval, then map the
// result. Invalid widening never prompts; allowed-once grants only this call,
// while rejection, cancellation, and unavailable approval have distinct errors.
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Strictly wider sandbox modes.

// widerModes lists permitted escalation targets for each effective mode.
// The call's actual mode is checked at execution time.
var widerModes = map[SandboxMode][]SandboxMode{
	ModeReadOnly:       {ModeWorkspaceWrite, ModeDangerFullAccess},
	ModeWorkspaceWrite: {ModeDangerFullAccess},
}

// CanEscalateTo permits strictly wider modes only, never equal or narrower
// modes. Danger-full-access is the terminal mode.
func CanEscalateTo(effectiveMode, targetMode SandboxMode) bool {
	targets, ok := widerModes[effectiveMode]
	if !ok {
		return false // Danger and unknown modes cannot escalate.
	}
	for _, t := range targets {
		if t == targetMode {
			return true
		}
	}
	return false
}

// EscalationTargets returns workspace-write and danger-full-access for
// capability schemas. Read-only is the baseline, never an escalation target.
func EscalationTargets() []SandboxMode {
	return []SandboxMode{ModeWorkspaceWrite, ModeDangerFullAccess}
}

// Argument validation.

// ValidateEscalationArgs requires sandbox_permissions and justification
// together or neither; justification must remain nonempty after trimming.
// An unexplained approval request or an unused justification is malformed.
func ValidateEscalationArgs(sandboxPermissions, justification string) error {
	if sandboxPermissions != "" && justification == "" {
		return errors.New("invalid escalation: sandbox_permissions requires a justification")
	}
	if justification != "" && sandboxPermissions == "" {
		return errors.New("invalid escalation: justification is only valid together with sandbox_permissions")
	}
	if justification != "" && strings.TrimSpace(justification) == "" {
		return errors.New("invalid justification: expected a non-empty sentence")
	}
	return nil
}

// Model-visible markers.

// SandboxDenialMarker is shared by Bash and filesystem enforcement so the
// model recognizes policy denials consistently, whether imposed by the kernel
// or filesystem mutation guards.
func SandboxDenialMarker(mode SandboxMode) string {
	return fmt.Sprintf("[sandbox: file access denied under %s mode]", mode)
}

// EscalationHintMarker provides an immediate retry hint when escalation fields
// are advertised: repeat the same operation with the narrowest sufficient
// wider sandbox_permissions and a justification. Subject names the operation
// family: command for Bash or operation for filesystem tools.
func EscalationHintMarker(subject string) string {
	return fmt.Sprintf(
		"[sandbox: escalation available — retry this exact %s once with sandbox_permissions (the narrowest wider mode that suffices) + justification; the approval prompt asks the user]",
		subject,
	)
}

// Approval interfaces and outcomes.

// EscalationOutcome is the closed set of approval outcomes, matching ApprovalOutcome.
type EscalationOutcome string

const (
	// EscalationAllowedOnce grants only the current invocation, not permanent access.
	EscalationAllowedOnce EscalationOutcome = "allowed-once"
	// EscalationRejected indicates user rejection.
	EscalationRejected EscalationOutcome = "rejected"
	// EscalationCancelled indicates canceled approval.
	EscalationCancelled EscalationOutcome = "cancelled"
	// EscalationUnavailable indicates an unavailable approval channel.
	EscalationUnavailable EscalationOutcome = "unavailable"
)

// EscalationApprovalRequest includes attribution, tool, call ID, and rationale for auditing.
type EscalationApprovalRequest struct {
	// ToolName records the requested tool.
	ToolName string
	// CallID identifies the call associated with the approval prompt.
	CallID string
	// Reason combines the target mode and justification for auditing.
	Reason string
}

// EscalationApprover is supplied by the tool layer. This package interprets
// results without importing the approval or agent packages.
type EscalationApprover interface {
	// Request asks for one operation's approval and returns a closed-set outcome.
	Request(ctx context.Context, req EscalationApprovalRequest) (EscalationOutcome, error)
}

// EscalationRequest is evaluated by ApproveEscalation.
type EscalationRequest struct {
	// RequestedMode is the schema-constrained target from EscalationTargets.
	RequestedMode SandboxMode
	// Justification is the model's rationale, shown unchanged within the audit reason.
	Justification string
	// EffectiveMode is the session override or composition default; the target must be wider.
	EffectiveMode SandboxMode
	// Subject names the operation family in user-facing text: command or operation.
	Subject string
	// ToolName is recorded in the approval request.
	ToolName string
	// CallID identifies the tool call attached to the approval prompt.
	CallID string
}

// Escalation errors.

var (
	// ErrEscalationNotWider indicates the target is not strictly wider than the effective mode.
	ErrEscalationNotWider = errors.New("escalation not strictly wider")
	// ErrEscalationNoApprover indicates missing approval service; escalation fails closed.
	ErrEscalationNoApprover = errors.New("escalation requires approval, but no approver is available")
	// ErrEscalationRejected indicates user rejection.
	ErrEscalationRejected = errors.New("escalation rejected by user")
	// ErrEscalationCancelled indicates canceled approval.
	ErrEscalationCancelled = errors.New("escalation approval cancelled")
	// ErrEscalationUnavailable indicates an unavailable approval channel.
	ErrEscalationUnavailable = errors.New("escalation requires approval, but no approval channel is available")
)

// Escalation approval flow.

// ApproveEscalation resolves escalation before execution: validate strict
// widening without prompting for invalid targets, validate paired arguments,
// require an approver, then map the result to a granted mode or precise error.
// The returned grant applies only to the requesting invocation.
func ApproveEscalation(ctx context.Context, req EscalationRequest, approver EscalationApprover) (SandboxMode, error) {
	// Check strict widening against this call's effective mode.
	if !CanEscalateTo(req.EffectiveMode, req.RequestedMode) {
		return "", fmt.Errorf("%w: sandbox escalation to %q is not strictly wider than this call's current %q mode",
			ErrEscalationNotWider, req.RequestedMode, req.EffectiveMode)
	}

	// Validate paired arguments.
	if err := ValidateEscalationArgs(string(req.RequestedMode), req.Justification); err != nil {
		return "", err
	}

	// Resolve the approval channel; fail closed if absent.
	if approver == nil {
		return "", fmt.Errorf("%w: escalation to %q requires approval, but no approver is composed",
			ErrEscalationNoApprover, req.RequestedMode)
	}

	// Request approval with the target mode and justification in the audit reason.
	outcome, err := approver.Request(ctx, EscalationApprovalRequest{
		ToolName: req.ToolName,
		CallID:   req.CallID,
		Reason:   fmt.Sprintf("escalate sandbox to %s: %s", req.RequestedMode, req.Justification),
	})
	if err != nil {
		return "", fmt.Errorf("escalation approval request failed: %w", err)
	}

	// Map the approval outcome.
	switch outcome {
	case EscalationAllowedOnce:
		return req.RequestedMode, nil
	case EscalationRejected:
		return "", fmt.Errorf("%w: the user rejected escalating this %s to %q",
			ErrEscalationRejected, req.Subject, req.RequestedMode)
	case EscalationCancelled:
		return "", fmt.Errorf("%w: approval for escalating to %q was cancelled",
			ErrEscalationCancelled, req.RequestedMode)
	case EscalationUnavailable:
		return "", fmt.Errorf("%w: escalation to %q requires approval, but no approval channel is available",
			ErrEscalationUnavailable, req.RequestedMode)
	default:
		return "", fmt.Errorf("unknown escalation outcome: %q", outcome)
	}
}

// Sandbox escalation tests cover strictly wider modes, closed target vocabulary, paired argument
// validation, denial and escalation markers, and approval-result mapping after width validation.
package tests

import (
	"context"
	"errors"
	"testing"

	"github.com/herlanai/omnix/pkg/sandbox"
)

// TestWiderModes verifies the strictly wider mode hierarchy.
func TestWiderModes(t *testing.T) {
	// read-only can escalate to workspace-write or danger-full-access.
	if !sandbox.CanEscalateTo(sandbox.ModeReadOnly, sandbox.ModeWorkspaceWrite) {
		t.Fatal("read-only 应可升级到 workspace-write")
	}
	if !sandbox.CanEscalateTo(sandbox.ModeReadOnly, sandbox.ModeDangerFullAccess) {
		t.Fatal("read-only 应可升级到 danger-full-access")
	}
	// workspace-write can escalate only to danger-full-access.
	if !sandbox.CanEscalateTo(sandbox.ModeWorkspaceWrite, sandbox.ModeDangerFullAccess) {
		t.Fatal("workspace-write 应可升级到 danger-full-access")
	}
	// Equal or narrower modes are not escalations.
	if sandbox.CanEscalateTo(sandbox.ModeReadOnly, sandbox.ModeReadOnly) {
		t.Fatal("read-only 不应升级到自身")
	}
	if sandbox.CanEscalateTo(sandbox.ModeWorkspaceWrite, sandbox.ModeReadOnly) {
		t.Fatal("workspace-write 不应降级到 read-only")
	}
	if sandbox.CanEscalateTo(sandbox.ModeDangerFullAccess, sandbox.ModeReadOnly) {
		t.Fatal("danger 不应降级")
	}
}

// TestEscalationTargets verifies the closed target vocabulary.
func TestEscalationTargets(t *testing.T) {
	targets := sandbox.EscalationTargets()
	if len(targets) != 2 {
		t.Fatalf("应有 2 个升级目标, 实际 %d", len(targets))
	}
	// read-only is the baseline, not an escalation target.
	for _, target := range targets {
		if target == sandbox.ModeReadOnly {
			t.Fatal("read-only 不应在升级目标中（底线）")
		}
	}
}

// TestValidateEscalationArgs validates paired arguments.
func TestValidateEscalationArgs(t *testing.T) {
	// Both arguments are present: accept.
	if err := sandbox.ValidateEscalationArgs("workspace-write", "需要写入日志文件"); err != nil {
		t.Fatalf("成对参数应通过: %v", err)
	}
	// Only sandbox_permissions is present: reject.
	if err := sandbox.ValidateEscalationArgs("workspace-write", ""); err == nil {
		t.Fatal("缺少 justification 应报错")
	}
	// Only justification is present: reject.
	if err := sandbox.ValidateEscalationArgs("", "需要写入"); err == nil {
		t.Fatal("缺少 sandbox_permissions 应报错")
	}
	// An empty justification is invalid.
	if err := sandbox.ValidateEscalationArgs("workspace-write", "   "); err == nil {
		t.Fatal("空白 justification 应报错")
	}
}

// TestSandboxDenialMarker verifies denial-marker formatting.
func TestSandboxDenialMarker(t *testing.T) {
	marker := sandbox.SandboxDenialMarker(sandbox.ModeReadOnly)
	expected := "[sandbox: file access denied under read-only mode]"
	if marker != expected {
		t.Fatalf("拒绝标记格式不符\n期望: %s\n实际: %s", expected, marker)
	}
}

// TestEscalationHintMarker verifies the escalation hint.
func TestEscalationHintMarker(t *testing.T) {
	hint := sandbox.EscalationHintMarker("command")
	if hint == "" {
		t.Fatal("升级提示不应为空")
	}
	// The hint must mention sandbox_permissions.
	if !containsStr(hint, "sandbox_permissions") {
		t.Fatalf("升级提示应包含 sandbox_permissions, 实际: %s", hint)
	}
	// The hint must mention justification.
	if !containsStr(hint, "justification") {
		t.Fatalf("升级提示应包含 justification, 实际: %s", hint)
	}
}

// fakeApprover supplies test approval decisions.
type fakeApprover struct {
	outcome sandbox.EscalationOutcome
	err     error
}

func (f *fakeApprover) Request(ctx context.Context, req sandbox.EscalationApprovalRequest) (sandbox.EscalationOutcome, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.outcome, nil
}

// TestApproveEscalationAllowed returns the requested mode after approval.
func TestApproveEscalationAllowed(t *testing.T) {
	approver := &fakeApprover{outcome: sandbox.EscalationAllowedOnce}
	result, err := sandbox.ApproveEscalation(context.Background(), sandbox.EscalationRequest{
		RequestedMode: sandbox.ModeWorkspaceWrite,
		Justification: "需要写入日志",
		EffectiveMode: sandbox.ModeReadOnly,
		Subject:       "command",
		ToolName:      "bash",
		CallID:        "call-1",
	}, approver)
	if err != nil {
		t.Fatalf("审批通过不应报错: %v", err)
	}
	if result != sandbox.ModeWorkspaceWrite {
		t.Fatalf("应返回 workspace-write, 实际 %s", result)
	}
}

// TestApproveEscalationNotWider rejects non-widening changes without prompting.
func TestApproveEscalationNotWider(t *testing.T) {
	approver := &fakeApprover{outcome: sandbox.EscalationAllowedOnce}
	_, err := sandbox.ApproveEscalation(context.Background(), sandbox.EscalationRequest{
		RequestedMode: sandbox.ModeReadOnly, // read-only to read-only does not widen access.
		Justification: "需要写入",
		EffectiveMode: sandbox.ModeReadOnly,
		Subject:       "command",
		ToolName:      "bash",
		CallID:        "call-1",
	}, approver)
	if err == nil {
		t.Fatal("非严格更宽应报错")
	}
}

// TestApproveEscalationRejected returns an error after rejection.
func TestApproveEscalationRejected(t *testing.T) {
	approver := &fakeApprover{outcome: sandbox.EscalationRejected}
	_, err := sandbox.ApproveEscalation(context.Background(), sandbox.EscalationRequest{
		RequestedMode: sandbox.ModeWorkspaceWrite,
		Justification: "需要写入",
		EffectiveMode: sandbox.ModeReadOnly,
		Subject:       "command",
		ToolName:      "bash",
		CallID:        "call-1",
	}, approver)
	if err == nil {
		t.Fatal("用户拒绝应报错")
	}
	if !errors.Is(err, sandbox.ErrEscalationRejected) {
		t.Fatalf("应返回 ErrEscalationRejected, 实际: %v", err)
	}
}

// TestApproveEscalationCancelled returns an error after cancellation.
func TestApproveEscalationCancelled(t *testing.T) {
	approver := &fakeApprover{outcome: sandbox.EscalationCancelled}
	_, err := sandbox.ApproveEscalation(context.Background(), sandbox.EscalationRequest{
		RequestedMode: sandbox.ModeWorkspaceWrite,
		Justification: "需要写入",
		EffectiveMode: sandbox.ModeReadOnly,
		Subject:       "command",
		ToolName:      "bash",
		CallID:        "call-1",
	}, approver)
	if err == nil {
		t.Fatal("审批取消应报错")
	}
}

// TestApproveEscalationUnavailable returns an error when approval is unavailable.
func TestApproveEscalationUnavailable(t *testing.T) {
	approver := &fakeApprover{outcome: sandbox.EscalationUnavailable}
	_, err := sandbox.ApproveEscalation(context.Background(), sandbox.EscalationRequest{
		RequestedMode: sandbox.ModeWorkspaceWrite,
		Justification: "需要写入",
		EffectiveMode: sandbox.ModeReadOnly,
		Subject:       "command",
		ToolName:      "bash",
		CallID:        "call-1",
	}, approver)
	if err == nil {
		t.Fatal("审批不可用应报错")
	}
}

// TestApproveEscalationNoApprover fails closed without an approver.
func TestApproveEscalationNoApprover(t *testing.T) {
	_, err := sandbox.ApproveEscalation(context.Background(), sandbox.EscalationRequest{
		RequestedMode: sandbox.ModeWorkspaceWrite,
		Justification: "需要写入",
		EffectiveMode: sandbox.ModeReadOnly,
		Subject:       "command",
		ToolName:      "bash",
		CallID:        "call-1",
	}, nil)
	if err == nil {
		t.Fatal("无审批者应 fail-closed")
	}
}

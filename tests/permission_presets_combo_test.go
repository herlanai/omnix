// M28 permission preset tests verify safe/danger/review/custom tuples, fallback to custom for
// unknown names, and derived fields with IsCustom after overrides.
package tests

import (
	"testing"

	"github.com/herlanai/omnix/pkg/approval"
	"github.com/herlanai/omnix/pkg/presets"
	"github.com/herlanai/omnix/pkg/sandbox"
)

// TestPermissionPresetsCombo verifies each preset's sandbox-mode/approval-policy pair.
func TestPermissionPresetsCombo(t *testing.T) {
	cases := []struct {
		name    string
		sandbox sandbox.SandboxMode
		approx  approval.Policy
	}{
		{presets.PresetSafe, sandbox.ModeReadOnly, approval.PolicyAskDangerous},
		{presets.PresetDanger, sandbox.ModeDangerFullAccess, approval.PolicyAllowAll},
		{presets.PresetReview, sandbox.ModeReadOnly, approval.PolicyAskDangerousEdit},
		{presets.PresetCustom, sandbox.ModeWorkspaceWrite, approval.PolicyAskDangerous},
	}
	for _, c := range cases {
		p, ok := presets.Resolve(c.name)
		if !ok {
			t.Fatalf("预设 %q Resolve 应成功", c.name)
		}
		if p.SandboxMode != c.sandbox || p.ApprovalPolicy != c.approx {
			t.Errorf("预设 %q 组合错误: got %+v, want sandbox=%s approval=%s", c.name, p, c.sandbox, c.approx)
		}
	}
	// The set of preset names is stable.
	names := presets.PresetNames()
	if len(names) != 4 {
		t.Fatalf("应有 4 个预设, 实际 %v", names)
	}
}

// TestPermissionPresetResolveUnknownFallsBackToCustom verifies unknown-name fallback.
func TestPermissionPresetResolveUnknownFallsBackToCustom(t *testing.T) {
	p, ok := presets.Resolve("nonexistent-preset")
	if !ok {
		t.Fatal("未知预设应回落 custom 且 ok=true")
	}
	if p.Name != presets.PresetCustom {
		t.Fatalf("应回落 custom, 实际 %s", p.Name)
	}
	if p.SandboxMode != sandbox.ModeWorkspaceWrite {
		t.Fatalf("custom sandbox 应为 workspace-write, 实际 %s", p.SandboxMode)
	}
}

// TestPermissionPresetMappers verifies mappings used to wire approval.Service.
func TestPermissionPresetMappers(t *testing.T) {
	am := presets.ApprovalMapper()
	sb := presets.SandboxMapper()
	pol, _ := am("danger")
	if pol != approval.PolicyAllowAll {
		t.Fatalf("danger→approval 应 allow-all, 实际 %s", pol)
	}
	scale, _ := sb("review")
	if scale != sandbox.ModeReadOnly {
		t.Fatalf("review→sandbox 应 read-only, 实际 %s", scale)
	}
}

// TestPermissionPresetDerive checks derived state and IsCustom.
func TestPermissionPresetDerive(t *testing.T) {
	// No override means the preset is not custom.
	st := presets.Derive(presets.PresetSafe, nil, nil)
	if st.IsCustom {
		t.Fatal("无 override 不应是 custom")
	}
	if st.ActivePreset != presets.PresetSafe || st.SandboxMode != sandbox.ModeReadOnly {
		t.Fatalf("safe 派生状态错误: %+v", st)
	}
	// Overrides update derived fields and set IsCustom=true.
	ws := sandbox.ModeDangerFullAccess
	allow := approval.PolicyAllowAll
	st = presets.Derive(presets.PresetCustom, &ws, &allow)
	if !st.IsCustom {
		t.Fatal("custom + override 应为 IsCustom=true")
	}
	if st.SandboxMode != sandbox.ModeDangerFullAccess || st.ApprovalPolicy != approval.PolicyAllowAll {
		t.Fatalf("override 派生字段错误: %+v", st)
	}
}

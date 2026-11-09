// Permission presets combine sandbox mode and approval policy (M28), matching
// interaction/permission-presets. Built-ins are safe, danger, review, and custom;
// custom supplies a baseline for user overrides. Injected policy mappings avoid
// a dependency cycle with approval. DerivedState exposes the final preset and
// override combination to UI and SDK consumers.
package presets

import (
	"sort"

	"github.com/herlanai/omnix/pkg/approval"
	"github.com/herlanai/omnix/pkg/sandbox"
)

// PermissionPreset combines sandbox mode and approval policy.
type PermissionPreset struct {
	// Name identifies the preset.
	Name string `json:"name"`
	// SandboxMode is the preset's sandbox mode.
	SandboxMode sandbox.SandboxMode `json:"sandboxMode"`
	// ApprovalPolicy is the preset's approval policy.
	ApprovalPolicy approval.Policy `json:"approvalPolicy"`
}

// Preset names.
const (
	PresetSafe   = "safe"
	PresetDanger = "danger"
	PresetReview = "review"
	PresetCustom = "custom"
)

// presetTable matches the session presetSandbox and presetApproval definitions.
var presetTable = map[string]PermissionPreset{
	PresetSafe: {
		Name:           PresetSafe,
		SandboxMode:    sandbox.ModeReadOnly,
		ApprovalPolicy: approval.PolicyAskDangerous,
	},
	PresetDanger: {
		Name:           PresetDanger,
		SandboxMode:    sandbox.ModeDangerFullAccess,
		ApprovalPolicy: approval.PolicyAllowAll,
	},
	PresetReview: {
		Name:           PresetReview,
		SandboxMode:    sandbox.ModeReadOnly,
		ApprovalPolicy: approval.PolicyAskDangerousEdit,
	},
	// The custom baseline is workspace-write plus ask-dangerous, with user overrides.
	PresetCustom: {
		Name:           PresetCustom,
		SandboxMode:    sandbox.ModeWorkspaceWrite,
		ApprovalPolicy: approval.PolicyAskDangerous,
	},
}

// PresetNames returns built-in names in lexical order.
func PresetNames() []string {
	names := make([]string, 0, len(presetTable))
	for n := range presetTable {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Resolve resolves a preset name, falling back to the custom baseline for unknown names.
func Resolve(name string) (PermissionPreset, bool) {
	p, ok := presetTable[name]
	if !ok {
		// Unknown presets use the custom baseline with ok=true.
		return presetTable[PresetCustom], true
	}
	return p, true
}

// ApprovalMapper supplies preset-to-policy mapping to approval.Service.
// Unknown presets use the custom ask-dangerous policy.
func ApprovalMapper() func(string) (approval.Policy, bool) {
	return func(name string) (approval.Policy, bool) {
		p, _ := Resolve(name)
		return p.ApprovalPolicy, true
	}
}

// SandboxMapper maps preset names to sandbox modes.
func SandboxMapper() func(string) (sandbox.SandboxMode, bool) {
	return func(name string) (sandbox.SandboxMode, bool) {
		p, _ := Resolve(name)
		return p.SandboxMode, true
	}
}

// DerivedState exposes permission state after preset and override resolution.
type DerivedState struct {
	// ActivePreset names the effective preset.
	ActivePreset string `json:"activePreset"`
	// SandboxMode includes any custom override.
	SandboxMode sandbox.SandboxMode `json:"sandboxMode"`
	// ApprovalPolicy is the derived approval policy.
	ApprovalPolicy approval.Policy `json:"approvalPolicy"`
	// IsCustom indicates a custom derived state.
	IsCustom bool `json:"isCustom"`
}

// Derive applies optional sandbox and approval overrides to a preset.
// Nonnil overrides replace their fields; IsCustom signals the resulting custom state.
func Derive(presetName string, sandboxOverride *sandbox.SandboxMode, approvalOverride *approval.Policy) DerivedState {
	base, _ := Resolve(presetName)
	isCustom := sandboxOverride != nil || approvalOverride != nil
	if presetName != PresetCustom {
		isCustom = false
	}
	if sandboxOverride != nil {
		base.SandboxMode = *sandboxOverride
	}
	if approvalOverride != nil {
		base.ApprovalPolicy = *approvalOverride
	}
	return DerivedState{
		ActivePreset:   base.Name,
		SandboxMode:    base.SandboxMode,
		ApprovalPolicy: base.ApprovalPolicy,
		IsCustom:       isCustom,
	}
}

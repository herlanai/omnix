// M24 restriction tests cover host denial overridden by scope exemption, combined denials,
// nearest-scope precedence, and filtering for presets and subagent capabilities.
package tests

import (
	"context"
	"testing"

	"github.com/herlanai/omnix/pkg/tools"
)

// mkTool constructs an executable tool definition.
func mkTool(name string) *tools.Tool {
	return &tools.Tool{Name: name, Execute: func(ctx context.Context, input map[string]any) (any, error) {
		return "ok", nil
	}}
}

// TestToolRestrictionHostDenyScopeExempt restores a tool through a scope exemption.
func TestToolRestrictionHostDenyScopeExempt(t *testing.T) {
	rs := tools.NewRestrictionSet()
	// Deny bash and rm at host level.
	rs.Host(tools.RestrictionDeny("bash", "rm"))
	// Exempt rm at scope level.
	rs.Scope("session:main", tools.RestrictionAllow("rm"))

	if !rs.Allowed("rm") {
		t.Fatal("scope exempt 应覆盖 host deny, rm 应可用")
	}
	if rs.Allowed("bash") {
		t.Fatal("bash 无 scope exempt, 应仍被 host deny 拒绝")
	}
	if !rs.Allowed("goal_list") {
		t.Fatal("未提及的工具应默认放行")
	}
}

// TestToolRestrictionIntersect preserves denial across both layers.
func TestToolRestrictionIntersect(t *testing.T) {
	rs := tools.NewRestrictionSet()
	rs.Host(tools.RestrictionDeny("rm", "bash"))
	rs.Scope("session:child", tools.RestrictionDeny("bash")) // The child layer also denies bash.

	if rs.Allowed("bash") {
		t.Fatal("两层均 deny bash, 应拒绝")
	}
	if !rs.Allowed("ls") {
		t.Fatal("未提及的 ls 应放行")
	}
}

// TestToolRestrictionNearestScopeWins lets a nearer child scope deny a parent-allowed tool.
func TestToolRestrictionNearestScopeWins(t *testing.T) {
	rs := tools.NewRestrictionSet()
	rs.Host(tools.RestrictionAllow("web_fetch"))              // Allow at host level.
	rs.Scope("session:a", tools.RestrictionDeny("web_fetch")) // Deny at session level.

	if rs.Allowed("web_fetch") {
		t.Fatal("最近会话层 deny 应覆盖 host allow")
	}
}

// TestToolRestrictionFilterHideForPreset filters tools hidden by a preset.
func TestToolRestrictionFilterHideForPreset(t *testing.T) {
	defs := []*tools.Tool{mkTool("bash"), mkTool("rm"), mkTool("ls"), mkTool("goal_list")}
	rs := tools.NewRestrictionSet()
	rs.Host(tools.RestrictionDeny("bash", "rm")) // Hide dangerous tools in the preset.

	kept := rs.Filter(defs)
	if len(kept) != 2 {
		t.Fatalf("应保留 2 个工具(lts/goal), 实际 %v", names(kept))
	}
	if kept[0].Name != "ls" || kept[1].Name != "goal_list" {
		t.Fatalf("保留顺序/内容错误: %v", names(kept))
	}
}

// TestToolRestrictionSubagentParentLimitsChild verifies parent-imposed child capabilities.
func TestToolRestrictionSubagentParentLimitsChild(t *testing.T) {
	defs := []*tools.Tool{mkTool("bash"), mkTool("fs_edit"), mkTool("goal_list")}
	// The parent excludes bash and fs_edit from the child's defaults.
	rs := tools.NewRestrictionSet()
	rs.Host(tools.RestrictionDeny("bash", "fs_edit"))
	// The child explicitly permits only goal_list in its nearer scope.
	rs.Scope("subagent:child", tools.RestrictionAllow("goal_list"))

	kept := rs.Filter(defs)
	if len(kept) != 1 || kept[0].Name != "goal_list" {
		t.Fatalf("子代理应仅保留 goal_list, 实际 %v", names(kept))
	}
}

// names extracts tool names.
func names(ts []*tools.Tool) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		if t != nil {
			out = append(out, t.Name)
		}
	}
	return out
}

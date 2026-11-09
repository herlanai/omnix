// M11 plan-mode guidance and approval-based exit tests.
package tests

import (
	"context"
	"testing"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/plan"
	"github.com/herlanai/omnix/pkg/session"
	"github.com/herlanai/omnix/pkg/sysprompt"
)

// TestPlanModeInjectPolicy injects plan:policy when entering plan mode.
func TestPlanModeInjectPolicy(t *testing.T) {
	sl := session.NewSessionLog(brand.NewSessionID("plan_1"))
	sys := sysprompt.New()

	if err := plan.Enter(sl, sys); err != nil {
		t.Fatalf("Enter 失败: %v", err)
	}
	if !plan.IsActive(sl) {
		t.Fatal("Enter 后应处于 plan mode")
	}
	if !sys.Has(plan.PlanPolicySectionName) {
		t.Fatal("plan:policy section 应注入")
	}
	if !plan.IsActive(sl) {
		t.Fatal("应处于 plan mode")
	}
}

// TestPlanModeApprovalPass removes plan:policy and exits after approval.
func TestPlanModeApprovalPass(t *testing.T) {
	sl := session.NewSessionLog(brand.NewSessionID("plan_2"))
	sys := sysprompt.New()
	_ = plan.Enter(sl, sys)

	tool := plan.NewExitPlanModeTool(sl, sys)
	// The stub without an asker approves by default.
	res, err := tool.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("Execute 失败: %v", err)
	}
	m := res.(map[string]any)
	if m["exited"] != true || m["approved"] != true {
		t.Fatalf("审批通过应退出: %v", m)
	}

	// Remove plan:policy so it is absent from the next request.
	if sys.Has(plan.PlanPolicySectionName) {
		t.Fatal("审批通过后 plan:policy 应被移除")
	}
	if plan.IsActive(sl) {
		t.Fatal("审批通过后应退出 plan mode")
	}
}

// TestPlanModeApprovalReject retains plan:policy after rejection.
func TestPlanModeApprovalReject(t *testing.T) {
	sl := session.NewSessionLog(brand.NewSessionID("plan_3"))
	sys := sysprompt.New()
	_ = plan.Enter(sl, sys)

	tool := plan.NewExitPlanModeTool(sl, sys)
	// The custom asker returns 1 to continue planning.
	tool.SetAsker(func(ctx context.Context, prompt string, choices []string) (int, error) {
		return 1, nil
	})
	res, err := tool.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("Execute 失败: %v", err)
	}
	m := res.(map[string]any)
	if m["exited"] != false || m["approved"] != false {
		t.Fatalf("审批拒绝不应退出: %v", m)
	}

	// Retain plan:policy and remain in plan mode.
	if !sys.Has(plan.PlanPolicySectionName) {
		t.Fatal("审批拒绝后 plan:policy 应保留")
	}
	if !plan.IsActive(sl) {
		t.Fatal("审批拒绝后应仍处于 plan mode")
	}
}

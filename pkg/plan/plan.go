// Package plan provides plan-mode guidance and approval-gated exit, matching
// core/plan-mode. The fixed plan:policy section (order 500) appears in the system
// prompt only while plan mode is active. Entering emits plan/mode(on). The
// exit_plan_mode tool asks through M14 User Questions: approval removes the
// section and emits plan/mode(off); rejection keeps plan mode active.
package plan

import (
	"context"

	"github.com/herlanai/omnix/pkg/session"
	"github.com/herlanai/omnix/pkg/sysprompt"
)

// PlanPolicySectionName names the plan:policy section.
const PlanPolicySectionName = "plan_policy"

// planPolicyText is fixed upstream policy text; D2 requires it to remain stable.
const planPolicyText = "当前处于计划模式：只输出方案，不执行改动，直到用户批准。"

// Enter emits plan/mode(on) and installs the plan:policy section.
func Enter(sl *session.SessionLog, sys *sysprompt.Assembler) error {
	if _, err := sl.Append(session.PlanModeData{Mode: "on"}); err != nil {
		return err
	}
	sys.Register(PlanPolicySectionName, sysprompt.SectionOrderPlanPolicy, planPolicyText)
	return nil
}

// Exit emits plan/mode(off) and removes the plan:policy section.
func Exit(sl *session.SessionLog, sys *sysprompt.Assembler) error {
	if _, err := sl.Append(session.PlanModeData{Mode: "off"}); err != nil {
		return err
	}
	sys.Unregister(PlanPolicySectionName)
	return nil
}

// IsActive reports whether plan mode is active.
func IsActive(sl *session.SessionLog) bool {
	mode := session.FoldPlanMode(sl.Events())
	return mode.Present && mode.Mode == "on"
}

// ExitPlanModeResult reports exit_plan_mode approval.
type ExitPlanModeResult struct {
	// Approved reports whether approval was granted.
	Approved bool
	// Exited reports actual exit, which requires approval.
	Exited bool
}

// ExitPlanModeTool removes plan:policy only after UserQuestion approval.
type ExitPlanModeTool struct {
	// ask is the M14 question callback; nil uses the approving stub.
	ask func(ctx context.Context, prompt string, choices []string) (int, error)
	// sl is the session log.
	sl *session.SessionLog
	// sys assembles the system prompt and removes plan:policy.
	sys *sysprompt.Assembler
}

// NewExitPlanModeTool creates the exit_plan_mode tool.
func NewExitPlanModeTool(sl *session.SessionLog, sys *sysprompt.Assembler) *ExitPlanModeTool {
	return &ExitPlanModeTool{sl: sl, sys: sys}
}

// SetAsker injects a question callback; the default test stub approves.
func (t *ExitPlanModeTool) SetAsker(f func(ctx context.Context, prompt string, choices []string) (int, error)) {
	t.ask = f
}

// Name returns the tool name.
func (t *ExitPlanModeTool) Name() string { return "exit_plan_mode" }

// Description returns the tool description.
func (t *ExitPlanModeTool) Description() string { return "退出计划模式（需用户批准）" }

// Execute requests approval, exiting and removing plan:policy only if approved.
func (t *ExitPlanModeTool) Execute(ctx context.Context, input map[string]any) (any, error) {
	// Emit the plan/approval request.
	_, _ = t.sl.Append(session.PlanApprovalData{Approved: false})

	// Request User Questions approval.
	approved := resolveApproval(t, ctx)

	// Record the approval result.
	_, _ = t.sl.Append(session.PlanApprovalData{Approved: approved})

	if approved {
		// Approval removes plan:policy and exits.
		if err := Exit(t.sl, t.sys); err != nil {
			return nil, err
		}
		return map[string]any{"exited": true, "approved": true}, nil
	}
	// Rejection retains plan:policy.
	return map[string]any{"exited": false, "approved": false}, nil
}

// resolveApproval asks the user; the stub approves by default.
func resolveApproval(t *ExitPlanModeTool, ctx context.Context) bool {
	if t.ask != nil {
		idx, err := t.ask(ctx, "是否退出计划模式？", []string{"批准退出", "继续计划"})
		return err == nil && idx == 0
	}
	// The default stub approves.
	return true
}

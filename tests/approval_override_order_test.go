// M27 approval tests cover preset/user/session precedence, allow/deny/ask decisions, one-call-only
// approval with repeated prompts, and successful or failed User Questions requests.
package tests

import (
	"context"
	"testing"

	"github.com/herlanai/omnix/pkg/approval"
	"github.com/herlanai/omnix/pkg/userq"
)

// fixedAnswerProvider returns a controlled answer: 0 allows and 1 denies.
type fixedAnswerProvider struct{ ans int }

func (f *fixedAnswerProvider) Ask(_ context.Context, _ userq.QuestionOptions) (*userq.QuestionResult, error) {
	return &userq.QuestionResult{SelectedIndex: f.ans, SelectedIndices: []int{f.ans}}, nil
}

// failingAnswerProvider always fails to simulate a User Questions error.
type failingAnswerProvider struct{}

func (*failingAnswerProvider) Ask(context.Context, userq.QuestionOptions) (*userq.QuestionResult, error) {
	return nil, context.Canceled
}

// presetPolicy maps test presets to policies.
func presetPolicy(preset string) (approval.Policy, bool) {
	switch preset {
	case "safe":
		return approval.PolicyDenyAll, true
	case "danger":
		return approval.PolicyAllowAll, true
	case "default":
		return approval.PolicyAskDangerous, true
	}
	return approval.PolicyDenyAll, false
}

// TestApprovalThreeLayerOverrideOrder verifies preset < user < session precedence.
func TestApprovalThreeLayerOverrideOrder(t *testing.T) {
	svc := approval.New(presetPolicy, userq.New(userq.NewStub()))

	// The danger preset selects allow-all.
	if e := svc.Resolve("danger", ""); e.Source != approval.SourcePreset || e.Policy != approval.PolicyAllowAll {
		t.Fatalf("预设层错误: %+v", e)
	}

	// The user's deny-all override takes precedence over the preset.
	svc.SetUserPolicy(approval.PolicyDenyAll)
	if e := svc.Resolve("danger", ""); e.Source != approval.SourceUser || e.Policy != approval.PolicyDenyAll {
		t.Fatalf("用户层 override 错误: %+v", e)
	}

	// The session's allow-all override has highest priority.
	svc.SetSessionPolicy("sess1", approval.PolicyAllowAll)
	if e := svc.Resolve("danger", "sess1"); e.Source != approval.SourceSession || e.Policy != approval.PolicyAllowAll {
		t.Fatalf("会话层 override 错误: %+v", e)
	}

	// Other sessions remain independent of this session's override.
	if e := svc.Resolve("danger", "sess-other"); e.Source != approval.SourceUser {
		t.Fatalf("其他会话应命中用户层: %+v", e)
	}
}

// TestApprovalEvaluateAllowDeny verifies direct allow and deny decisions.
func TestApprovalEvaluateAllowDeny(t *testing.T) {
	svc := approval.New(presetPolicy, userq.New(userq.NewStub()))
	dec, _ := svc.Evaluate(approval.Request{Tool: "bash", Preset: "danger", CallID: "c1"})
	if dec != approval.DecideAllow {
		t.Fatalf("danger(allow-all) 应 allow, 实际 %s", dec)
	}
	dec, _ = svc.Evaluate(approval.Request{Tool: "bash", Preset: "safe", CallID: "c2"})
	if dec != approval.DecideDeny {
		t.Fatalf("safe(deny-all) 应 deny, 实际 %s", dec)
	}
}

// TestApprovalAskAllowedOnce permits only the approved call and prompts again for the next one.
func TestApprovalAskAllowedOnce(t *testing.T) {
	// Answer 0 allows the call.
	svc := approval.New(presetPolicy, userq.New(&fixedAnswerProvider{ans: 0}))

	// Default ask-dangerous plus bash triggers a prompt, whose approval allows the call.
	dec, err := svc.Evaluate(approval.Request{Tool: "bash", Preset: "default", CallID: "call-1"})
	if err != nil {
		t.Fatal(err)
	}
	if dec != approval.DecideAllow {
		t.Fatalf("ask 且用户准许应 allow, 实际 %s", dec)
	}
	if svc.AskCount() != 1 {
		t.Fatalf("应恰好 ask 1 次, 实际 %d", svc.AskCount())
	}

	// The next call to the same tool prompts again; approval is not permanent.
	dec, err = svc.Evaluate(approval.Request{Tool: "bash", Preset: "default", CallID: "call-2"})
	if err != nil {
		t.Fatal(err)
	}
	if svc.AskCount() != 2 {
		t.Fatalf("allowed-once 语义下同工具下一次应继续 ask, 实际次数 %d", svc.AskCount())
	}
	if dec != approval.DecideAllow {
		t.Fatalf("第二次用户仍准许应 allow, 实际 %s", dec)
	}
}

// TestApprovalAskRejected denies a rejected prompt.
func TestApprovalAskRejected(t *testing.T) {
	// Answer 1 denies the call.
	svc := approval.New(presetPolicy, userq.New(&fixedAnswerProvider{ans: 1}))
	dec, err := svc.Evaluate(approval.Request{Tool: "bash", Preset: "default", CallID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if dec != approval.DecideDeny {
		t.Fatalf("ask 被拒绝应 deny, 实际 %s", dec)
	}
}

// TestApprovalUQFailureDeny fails closed and propagates User Questions errors.
func TestApprovalUQFailureDeny(t *testing.T) {
	svc := approval.New(presetPolicy, userq.New(&failingAnswerProvider{}))
	dec, err := svc.Evaluate(approval.Request{Tool: "bash", Preset: "default", CallID: "c1"})
	if err == nil {
		t.Fatal("UQ 失败应上抛 error")
	}
	if dec != approval.DecideDeny {
		t.Fatalf("UQ 失败应 fail-closed deny, 实际 %s", dec)
	}
}

// TestApprovalNonDangerousAllows permits safe tools under ask-dangerous without prompting.
func TestApprovalNonDangerousAllows(t *testing.T) {
	svc := approval.New(presetPolicy, userq.New(&fixedAnswerProvider{ans: 1})) // The provider must not be invoked, even though it would reject.
	dec, err := svc.Evaluate(approval.Request{Tool: "goal_list", Preset: "default", CallID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if dec != approval.DecideAllow {
		t.Fatalf("非危险工具应直接 allow, 实际 %s", dec)
	}
	if svc.AskCount() != 0 {
		t.Fatalf("非危险工具不应 ask, 实际次数 %d", svc.AskCount())
	}
}

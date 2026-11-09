// M22 pre-tool decision tests cover allow/deny/ask wiring, denied calls not executing,
// one-call-only approval with repeated prompts, and cooperation with M27 approval policy.
package tests

import (
	"context"
	"sync"
	"testing"

	"github.com/herlanai/omnix/pkg/approval"
	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/tools"
)

// newToolCallRequest constructs a tool-call request.
func newToolCallRequest(callID, tool string, input map[string]any) *tools.ToolCallRequest {
	return &tools.ToolCallRequest{
		CallID: brand.NewToolCallID(callID),
		Tool:   tool,
		Input:  input,
	}
}

// countingAsk counts prompts and returns the configured alwaysAllowed decision.
type countingAsk struct {
	sync.Mutex
	calls   int
	allowed bool
}

func (c *countingAsk) Ask(req *tools.ToolCallRequest) (bool, error) {
	c.Lock()
	defer c.Unlock()
	c.calls++
	return c.allowed, nil
}

func (c *countingAsk) count() int {
	c.Lock()
	defer c.Unlock()
	return c.calls
}

// decisionByTool selects allow, deny, or ask based on the tool name.
func decisionByTool(allow map[string]bool, ask map[string]bool) tools.DecisionFunc {
	return func(req *tools.ToolCallRequest) (tools.PreToolDecision, error) {
		if ask[req.Tool] {
			return tools.PreAsk, nil
		}
		if allow[req.Tool] {
			return tools.PreAllow, nil
		}
		return tools.PreDeny, nil
	}
}

// TestPreToolDecisionAllowDenyAsk executes allowed calls, short-circuits denied calls, and grants
// only the approved call.
func TestPreToolDecisionAllowDenyAsk(t *testing.T) {
	tool := &tools.Tool{Name: "test_tool", Execute: func(ctx context.Context, input map[string]any) (any, error) {
		return "executed", nil
	}}

	executed := false
	// Record actual execution through a closure.
	inner := tool.Execute
	tool.Execute = func(ctx context.Context, input map[string]any) (any, error) {
		executed = true
		return inner(ctx, input)
	}

	ask := &countingAsk{allowed: true}
	pipeline := tools.NewPipeline()
	pipeline.UsePre(tools.PreDecisionMiddleware(
		decisionByTool(map[string]bool{"allow_tool": true}, map[string]bool{"ask_tool": true}),
		ask.Ask,
	))
	pipeline.WithTool(tool)

	// Allow executes successfully.
	res := pipeline.Run(context.Background(), newToolCallRequest("c-allow", "allow_tool", nil), tool)
	if res.IsError || res.Value != "executed" {
		t.Fatalf("allow 应执行成功: %+v", res)
	}

	// Deny skips execution and returns isError.
	executed = false
	res = pipeline.Run(context.Background(), newToolCallRequest("c-deny", "deny_tool", nil), tool)
	if !res.IsError {
		t.Fatal("deny 应导致 isError")
	}
	if executed {
		t.Fatal("deny 的工具不应被执行")
	}

	// User approval permits this requested execution.
	executed = false
	res = pipeline.Run(context.Background(), newToolCallRequest("c-ask-1", "ask_tool", nil), tool)
	if res.IsError || res.Value != "executed" {
		t.Fatalf("ask 且用户准许应执行: %+v", res)
	}
	if !executed {
		t.Fatal("ask 准许后工具应执行")
	}
	if ask.count() != 1 {
		t.Fatalf("应 ask 1 次, 实际 %d", ask.count())
	}
}

// TestPreToolDecisionAskOnceNotPermanent prompts again for later calls to the same tool.
func TestPreToolDecisionAskOnceNotPermanent(t *testing.T) {
	tool := &tools.Tool{Name: "t", Execute: func(ctx context.Context, input map[string]any) (any, error) {
		return "ok", nil
	}}
	ask := &countingAsk{allowed: true}
	pipeline := tools.NewPipeline()
	pipeline.UsePre(tools.PreDecisionMiddleware(
		decisionByTool(nil, map[string]bool{"ask_tool": true}),
		ask.Ask,
	))
	pipeline.WithTool(tool)

	// Both identical tool calls must prompt; never persist approval by tool name.
	pipeline.Run(context.Background(), newToolCallRequest("c1", "ask_tool", nil), tool)
	pipeline.Run(context.Background(), newToolCallRequest("c2", "ask_tool", nil), tool)

	if ask.count() != 2 {
		t.Fatalf("allowed-once 语义下两次调用应各自 ask（共 2 次）, 实际 %d", ask.count())
	}
}

// TestPreToolDecisionAskDeniedNotExecutes short-circuits rejected prompts without executing the
// tool.
func TestPreToolDecisionAskDeniedNotExecutes(t *testing.T) {
	executed := false
	tool := &tools.Tool{Name: "t", Execute: func(ctx context.Context, input map[string]any) (any, error) {
		executed = true
		return "ok", nil
	}}
	ask := &countingAsk{allowed: false}
	pipeline := tools.NewPipeline()
	pipeline.UsePre(tools.PreDecisionMiddleware(
		decisionByTool(nil, map[string]bool{"ask_tool": true}),
		ask.Ask,
	))
	pipeline.WithTool(tool)

	res := pipeline.Run(context.Background(), newToolCallRequest("c1", "ask_tool", nil), tool)
	if !res.IsError {
		t.Fatal("ask 被拒应 isError")
	}
	if executed {
		t.Fatal("ask 被拒后工具不应执行")
	}
}

// TestPreDecisionFromApproval verifies M22/M27 integration through DecisionFromApproval.
func TestPreDecisionFromApproval(t *testing.T) {
	// Use an approval evaluator that always denies to verify propagation.
	evaluate := func(*tools.ToolCallRequest) (approval.Decision, error) {
		return approval.DecideDeny, nil
	}
	decide := tools.DecisionFromApproval(evaluate)
	req := newToolCallRequest("c1", "bash", nil)
	dec, err := decide(req)
	if err != nil {
		t.Fatal(err)
	}
	if dec != tools.PreDeny {
		t.Fatalf("应透传 deny, 实际 %d", dec)
	}
	// AskFromApproval maps an allow decision to true.
	evaluateAllow := func(*tools.ToolCallRequest) (approval.Decision, error) {
		return approval.DecideAllow, nil
	}
	if ok, _ := tools.AskFromApproval(evaluateAllow)(req); !ok {
		t.Fatal("AskFromApproval 在 allow 时应返回 true")
	}
}

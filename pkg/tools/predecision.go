// Pre-execute decisions use allow, deny, and ask from pkg/approval (M22). Deny short-circuits
// execution with an error. Ask delegates to AskFunc, and approval permits only the current
// invocation; it never creates a permanent tool-name exemption. DecisionFromApproval connects
// the approval service's layered overrides and one-time approval handling to this pipeline
// stage.
package tools

import (
	"fmt"

	"github.com/herlanai/omnix/pkg/approval"
	"github.com/herlanai/omnix/pkg/waterfall"
)

// PreToolDecision is the three-state policy decision for a tool invocation.
type PreToolDecision = approval.Decision

// Decision constants share approval semantics.
const (
	PreAllow = approval.DecideAllow
	PreDeny  = approval.DecideDeny
	PreAsk   = approval.DecideAsk
)

// DecisionFunc evaluates the policy for one tool invocation.
type DecisionFunc func(req *ToolCallRequest) (PreToolDecision, error)

// AskFunc asks whether to allow the current invocation. A true result grants one-time permission
// only.
type AskFunc func(req *ToolCallRequest) (bool, error)

// denyReasonKey stores the denial reason and decision category in Meta.
const denyReasonKey = "preDecision.reason"

// DecisionFromApproval adapts the approval service to DecisionFunc. Service.Evaluate already
// resolves ask through user questions, so this adapter forwards the resolved decision without
// asking twice.
func DecisionFromApproval(decide func(req *ToolCallRequest) (approval.Decision, error)) DecisionFunc {
	return func(req *ToolCallRequest) (PreToolDecision, error) {
		return decide(req)
	}
}

// PreDecisionMiddleware passes allowed calls through, marks denied calls as errors without
// execution, and delegates ask decisions to AskFunc. A successful answer allows this invocation
// only; rejection or failure denies it.
func PreDecisionMiddleware(decide DecisionFunc, ask AskFunc) waterfall.Handler[ExecContext] {
	return func(ec *ExecContext, next waterfall.NextFunc) error {
		if ec == nil || ec.Request == nil || decide == nil {
			return next()
		}
		decision, err := decide(ec.Request)
		if err != nil {
			ec.Denied = true
			ec.Meta[denyReasonKey] = fmt.Sprintf("decision error: %v", err)
			return next()
		}
		switch decision {
		case PreAllow:
			// Allow the call to proceed to execution.
			return next()
		case PreDeny:
			ec.Denied = true
			ec.Meta[denyReasonKey] = "denied by pre-tool decision"
			return next()
		case PreAsk:
			if ask == nil {
				ec.Denied = true
				ec.Meta[denyReasonKey] = "ask requested but no ask handler"
				return next()
			}
			allowed, aerr := ask(ec.Request)
			if aerr != nil || !allowed {
				ec.Denied = true
				ec.Meta[denyReasonKey] = "ask denied by user"
				return next()
			}
			// Record one-time approval for auditing without persisting a reusable grant.
			ec.Meta["allowedOnce"] = ec.Request.CallID.Raw()
			return next()
		default:
			ec.Denied = true
			ec.Meta[denyReasonKey] = fmt.Sprintf("unknown decision %d", decision)
			return next()
		}
	}
}

// AskFromApproval adapts an approval evaluator to AskFunc. The evaluator is expected to resolve
// user interaction to allow or deny before returning; only allow grants execution.
func AskFromApproval(evaluate func(req *ToolCallRequest) (approval.Decision, error)) AskFunc {
	return func(req *ToolCallRequest) (bool, error) {
		dec, err := evaluate(req)
		if err != nil {
			return false, err
		}
		return dec == approval.DecideAllow, nil
	}
}

# Registering and governing tools

[Documentation](README.md) · [简体中文](../zh-CN/tools.md) · [Agent](agent.md)

A tool combines a model-visible name and schema with a Go execution function. `Pipeline` validates arguments, invokes one selected tool, and runs middleware around the invocation. Permissions and external effects remain application responsibilities unless you wire enforcement explicitly.

## Run a tool with an explicit policy

Save this complete program as `/tmp/omnix-tools-guide.go`. Run `go run /tmp/omnix-tools-guide.go` from the repository root. It invokes a tool once, then denies the same tool through pre-execute middleware.

```go
package main

import (
	"context"
	"fmt"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/tools"
)

func main() {
	schema, err := tools.Compile([]byte(`{
        "type": "object",
        "properties": {"name": {"type": "string"}},
        "required": ["name"],
        "additionalProperties": false
    }`))
	if err != nil {
		panic(err)
	}
	policy := tools.NewRestrictionSet()
	pipeline := tools.NewPipeline().WithTool(&tools.Tool{
		Name: "greet", Description: "Return a greeting for a name.",
		Schema: schema, TimeoutMs: 1000,
		Execute: func(ctx context.Context, input map[string]any) (any, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return "Hello, " + input["name"].(string), nil
		},
	})
	pipeline.UsePre(tools.PreDecisionMiddleware(
		func(req *tools.ToolCallRequest) (tools.PreToolDecision, error) {
			if !policy.Allowed(req.Tool) {
				return tools.PreDeny, nil
			}
			return tools.PreAllow, nil
		}, nil,
	))

	invoke := func(id string) *tools.ToolCallResult {
		return pipeline.Run(context.Background(), &tools.ToolCallRequest{
			CallID: brand.NewToolCallID(id), Tool: "greet",
			Input: map[string]any{"name": "Ada"},
		}, nil)
	}
	result := invoke("call-1")
	if result.IsError {
		panic(result.Error)
	}
	fmt.Println(result.Value)

	// The pre-execute middleware consults the policy on every call.
	policy.Host(tools.RestrictionDeny("greet"))
	fmt.Printf("denied: %t\n", invoke("call-2").IsError)
}
```

Expected output:

```text
Hello, Ada
denied: true
```

Passing nil as the last `Run` argument selects the tool from the pipeline registry. Passing a non-nil `*Tool` supplies the implementation directly. Always provide a non-nil context and request.

## Connect the pipeline to an agent

Pass the configured pipeline to `agent.NewAgent` as shown in the [Agent guide](agent.md). The agent uses `Pipeline.Tools()` for name-sorted model schemas and registry lookup for execution. `SetToolProvider` overrides execution lookup only; it does not advertise schemas for extra tools.

`WithTool` mutates the existing registry and returns the same pipeline. Registering the same name replaces its tool. Configure tools and middleware before concurrent use; the registry and middleware chains are not synchronized for concurrent mutation. Execution functions must protect their own shared state.

## Define the accepted input

[`tools.Compile`](../../pkg/tools/schema.go) parses the implemented JSON Schema subset and rejects unsupported keywords. Supported fields include object properties/required fields, primitive types, arrays, enum/const, numeric bounds, string bounds/patterns, and `oneOf`. This is not a full JSON Schema implementation: `oneOf` currently accepts any matching branch rather than requiring exactly one match.

`default` and `examples` describe inputs but do not insert values during validation. Use `additionalProperties: false` when extra fields should be rejected. Schema validation occurs in the dispatch terminal, after pre-execute middleware; validate again in custom dispatch paths that bypass the pipeline.

## Middleware execution order

| Registration method | Purpose and actual flow |
|---|---|
| `UsePre` | Inspect or rewrite input, consult policy, or deny. An error or `Denied=true` ends the call before execution, post, and result stages. |
| `UseExecute` | Wrap dispatch. The terminal checks cancellation, resolves the tool, validates its schema, and invokes it. |
| `UsePost` | Inspect or transform the result after dispatch, including execution errors; it may mark the result denied. |
| `UseResult` | Final result processing on paths that reach it. It is not a guaranteed finally hook. |

Handlers receive `*tools.ExecContext` and `waterfall.NextFunc`. Calling `next()` advances within that stage; call it at most once. Omitting `next()` short-circuits that stage, not necessarily the entire pipeline. To deny from a pre-handler, set `ec.Denied` or return an error rather than merely returning nil.

Tool `Execute` panics are recovered as error results. Middleware panics are not automatically recovered by these pipeline stages. Use ordinary `defer` for cleanup that must run regardless of which stage exits.

## Restrictions and approval

[`RestrictionSet`](../../pkg/tools/restriction.go) is a policy helper, not automatic enforcement. The example explicitly consults it in `PreDecisionMiddleware`. An unmentioned tool is allowed; the nearest layer mentioning a tool decides, and a nearer allow can override an outer deny. It is not a deny-only intersection or an immutable host security boundary.

Use `Filter` to select which tools to register and advertise, and enforce access again at invocation time. Filtering schemas alone does not prevent custom or stale calls. Updating an existing restriction layer changes its mask in place; adding a new layer appends it at the nearest position.

[`PreDecisionMiddleware`](../../pkg/tools/predecision.go) accepts allow, deny, or ask decisions. Supply an `AskFunc` for interactive approval; a missing handler, rejection, or error denies an ask. Successful approval grants that invocation only. The host owns user interaction, identity checks, audit persistence, and cancellation handling in decision callbacks.

## Cancellation, errors, and external effects

`TimeoutMs` derives a timeout context for `Execute`; the pipeline still calls the function synchronously. It cannot interrupt code that ignores `ctx`. Pass context to network calls, wait on `ctx.Done()` where appropriate, and make cleanup explicit.

A tool-local deadline maps to `TOOL_TIMEOUT`; parent cancellation is a distinct context failure. Read `ToolCallResult.IsError`, `Error`, and optional `ErrorCode` for direct calls. An agent records error text and `IsError` in `tool/result`, but does not retain the result's `ErrorCode` field there.

`WrapTimeout` is a separate helper that runs execution in a goroutine; returning on timeout does not kill that goroutine or undo its side effects. Do not assume automatic retries are safe for non-idempotent tools.

Go callbacks do not acquire OS isolation simply by running in a pipeline. Filesystem boundaries, sandbox providers, subprocess execution, credential access, and approval services need explicit host wiring. `ToolRunContext` and conclusion callbacks are separate helpers; the current Agent constructor does not automatically install every tool runtime helper.

## Check your integration

The same guard logic supports normal and pooled execution. `SetPooled(true)` reuses internal execution state; do not retain `ExecContext` or its `Meta` map after a call. Keep configuration fixed during use and verify any performance benefit with your workload.

See [dispatch and guard tests](../../pkg/tools/dispatch_test.go), [pool tests](../../pkg/tools/pool_test.go), and [restriction tests](../../tests/tools_restriction_intersect_test.go).

```sh
go test ./pkg/tools
go test ./tests -run 'TestToolRestriction|TestPreToolDecision|TestPreDecisionFromApproval'
```

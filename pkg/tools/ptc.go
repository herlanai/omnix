// Programmatic tool calling bridges upstream ptc.ts without its full scheduler. The model
// supplies an async function body to run_code, and tools.name(args) invokes registered tools
// through the normal pipeline, preserving permissions, sandboxing, and guards. The injected code
// runtime returns logs and a completion value; only the formatted outer result enters model
// history. Each binding runs its pipeline synchronously; runtime backends manage any
// program-level concurrency.
package tools

import (
	"context"
	"fmt"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/coderuntime"
)

// RunCodeName is the model-visible name of the programmatic tool-calling entry point.
const RunCodeName = "run_code"

// RunCodeInput contains run_code arguments.
type RunCodeInput struct {
	// Code is an async function body supporting top-level await and return.
	Code string `json:"code"`
	// Description is a short active-voice summary of the program.
	Description string `json:"description"`
}

// ToolLookup resolves registered tools by name for program bindings.
type ToolLookup func(name string) (*Tool, bool)

// NewRunCodeTool creates the run_code bridge using an injected code runtime and tool lookup.
// Each programmatic call runs through pipeline; nil selects a minimal pipeline containing only
// the tool implementation.
func NewRunCodeTool(runtime coderuntime.Runtime, lookup ToolLookup, pipeline *Pipeline) *Tool {
	if lookup == nil {
		lookup = func(string) (*Tool, bool) { return nil, false }
	}
	execute := func(ctx context.Context, input map[string]any) (any, error) {
		var in RunCodeInput
		in.Code, _ = input["code"].(string)
		in.Description, _ = input["description"].(string)
		if in.Code == "" {
			return nil, fmt.Errorf("run_code: empty code")
		}
		if in.Description == "" {
			return nil, fmt.Errorf("run_code: empty description")
		}

		// Expose registered tools through the tools binding namespace.
		ns := coderuntime.BindingNamespace{
			Global:     "tools",
			ErrorClass: "ToolError",
			Functions:  map[string]coderuntime.BindingFunction{
				// Dynamic tools.<name>(args) access is routed by the runtime's property bridge to CallMember,
				// using the requested member name.
			},
		}
		// Expose dynamic member access through Dispatch. The runtime maps tools.<name>(args) to this
		// binding and supplies name.
		ns.Functions["__dispatch__"] = func(c context.Context, args any) (any, error) {
			m, ok := args.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("invalid dispatch args")
			}
			name, _ := m["name"].(string)
			toolArgs, _ := m["arguments"].(map[string]any)
			return invokeToolInProgram(c, lookup, pipeline, name, toolArgs)
		}

		res, err := runtime.Run(&coderuntime.RunRequest{
			Program:  in.Code,
			Bindings: []coderuntime.BindingNamespace{ns},
			Ctx:      ctx,
		})
		if err != nil {
			return nil, fmt.Errorf("run_code runtime error: %w", err)
		}
		if res.Failed() {
			// Return the failure category and message so the model can correct the program.
			return nil, fmt.Errorf("run_code %s: %s", res.Failure.Kind, res.Failure.Message)
		}
		return PresentRunCodeResult(res), nil
	}

	return &Tool{
		Name:        RunCodeName,
		Description: "Execute a program against the available tools; body of an async function with top-level await/return.",
		Execute:     execute,
	}
}

// invokeToolInProgram runs a programmatic tool call through permission, sandbox, and guard
// middleware.
func invokeToolInProgram(ctx context.Context, lookup ToolLookup, pipeline *Pipeline, name string, args map[string]any) (any, error) {
	tool, ok := lookup(name)
	if !ok {
		return nil, fmt.Errorf("unknown tool %q", name)
	}
	req := &ToolCallRequest{
		CallID: brand.NewToolCallID(fmt.Sprintf("ptc-%s", name)),
		Tool:   name,
		Input:  args,
	}
	if pipeline == nil {
		pipeline = NewPipeline().WithTool(tool)
	} else {
		// Copy the shared pipeline before attaching the tool implementation.
		pipeline = pipeline.WithTool(tool)
	}
	result := pipeline.Run(ctx, req, tool)
	if result.IsError {
		return nil, fmt.Errorf("%s", result.Error)
	}
	return result.Value, nil
}

// RunCodeOutput is the structured run_code result.
type RunCodeOutput struct {
	Logs   []string `json:"logs"`
	Result any      `json:"result,omitempty"`
}

// PresentRunCodeResult concatenates ordered logs and the completion value into model-visible
// text.
func PresentRunCodeResult(res *coderuntime.RunResult) RunCodeOutput {
	return RunCodeOutput{Logs: res.Logs, Result: res.Value}
}

// The tool execution pipeline has four middleware stages (M23), matching upstream tools:
// pre-execute can deny or rewrite inputs; execute invokes the tool and can cancel; post-execute
// can block, truncate, or annotate results; result performs final wrapping and instrumentation.
// A denial skips execution and produces an isError result. Cancellation or post-execute blocking
// also marks the result as an error.
package tools

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/waterfall"
)

// Signal controls tool execution.
type Signal string

// Supported execution signals.
const (
	// SignalContinue allows execution to continue.
	SignalContinue Signal = "continue"
	// SignalCancel cancels execution and may be set by execute middleware.
	SignalCancel Signal = "cancel"
)

// ToolExecuteFunc implements a tool's execution function.
type ToolExecuteFunc func(ctx context.Context, input map[string]any) (any, error)

// Tool defines a tool's name, schema, and implementation.
type Tool struct {
	// Name is the model-visible tool name.
	Name string
	// Description describes the tool in the prompt.
	Description string
	// Schema describes and validates tool arguments.
	Schema *JsonSchemaNode
	// TimeoutMs is a cooperative time budget; zero is unlimited. Tools must honor ctx, and deadline
	// expiry maps to TOOL_TIMEOUT.
	TimeoutMs int
	// Execute is the tool implementation.
	Execute ToolExecuteFunc
}

// ToolCallRequest describes one tool invocation.
type ToolCallRequest struct {
	// CallID pairs tool/call with tool/result.
	CallID brand.ToolCallID
	// Tool is the tool name.
	Tool string
	// Input contains parsed arguments.
	Input map[string]any
}

// ToolCallResult holds the outcome of a tool invocation.
type ToolCallResult struct {
	// CallID pairs this result with its request.
	CallID brand.ToolCallID
	// IsError reports denial, blocking, cancellation, or a tool execution error.
	IsError bool
	// Value is the structured result.
	Value any
	// Error describes a failure when IsError is true.
	Error string
	// ErrorCode is an optional stable code for routing and replay.
	ErrorCode string
}

// ExecContext carries shared state through all four pipeline stages.
type ExecContext struct {
	// Request is the original invocation request.
	Request *ToolCallRequest
	// Result is populated by the pipeline stages.
	Result *ToolCallResult
	// Signal controls execution and may be changed by execute middleware.
	Signal Signal
	// Denied reports rejection by pre-execute or post-execute middleware.
	Denied bool
	// Meta holds display and audit metadata added by middleware.
	Meta map[string]any
	// Ctx carries the parent execution context, propagating cancellation, deadlines, and tracing to
	// tools (H01).
	Ctx context.Context
}

// Pipeline implements the four-stage tool execution chain.
type Pipeline struct {
	pre     *waterfall.Chain[ExecContext]
	execute *waterfall.Chain[ExecContext]
	post    *waterfall.Chain[ExecContext]
	result  *waterfall.Chain[ExecContext]
	// pooled enables reuse of ExecContext and its Meta map (H06). It defaults to false;
	// SetPooled(true) selects the pooled Run path.
	pooled     bool
	registered map[string]*Tool
}

// SetPooled enables opt-in ExecContext and Meta-map reuse to reduce allocations. Results remain
// separately allocated and safe for callers to retain.
func (p *Pipeline) SetPooled(enabled bool) *Pipeline {
	p.pooled = enabled
	return p
}

// NewPipeline creates an empty pipeline whose stages accept middleware.
func NewPipeline() *Pipeline {
	return &Pipeline{
		pre:     waterfall.New[ExecContext](),
		execute: waterfall.New[ExecContext](),
		post:    waterfall.New[ExecContext](),
		result:  waterfall.New[ExecContext](),
	}
}

// UsePre appends middleware that can inspect, rewrite, or deny inputs.
func (p *Pipeline) UsePre(h ...waterfall.Handler[ExecContext]) *Pipeline {
	p.pre.Use(h...)
	return p
}

// UseExecute appends middleware wrapping tool execution and its signal.
func (p *Pipeline) UseExecute(h ...waterfall.Handler[ExecContext]) *Pipeline {
	p.execute.Use(h...)
	return p
}

// UsePost appends middleware that can accept, block, truncate, or annotate results.
func (p *Pipeline) UsePost(h ...waterfall.Handler[ExecContext]) *Pipeline {
	p.post.Use(h...)
	return p
}

// UseResult appends final-result middleware.
func (p *Pipeline) UseResult(h ...waterfall.Handler[ExecContext]) *Pipeline {
	p.result.Use(h...)
	return p
}

// Run executes one invocation through all four stages and returns its result. When pooling is
// enabled, RunPooled provides equivalent semantics while reusing internal state.
func (p *Pipeline) Run(ctx context.Context, req *ToolCallRequest, tool *Tool) *ToolCallResult {
	if p.pooled {
		return p.RunPooled(ctx, req, tool)
	}
	ec := &ExecContext{
		Request: req,
		Result:  &ToolCallResult{CallID: req.CallID},
		Signal:  SignalContinue,
		Denied:  false,
		Meta:    map[string]any{},
		Ctx:     ctx,
	}

	return p.runStages(ctx, ec, tool)
}

// executeInner constructs the innermost middleware that invokes the tool.
func executeInner(tool *Tool) waterfall.Handler[ExecContext] {
	return func(ec *ExecContext, next waterfall.NextFunc) error {
		if tool == nil || tool.Execute == nil {
			ec.Result.IsError = true
			ec.Result.Error = "tool implementation missing"
			return next()
		}
		// Propagate the parent context's cancellation, deadline, and tracing rather than replacing it
		// with Background (H01).
		ctx := withExecContext(ec.Ctx, ec)
		if tool.TimeoutMs > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, time.Duration(tool.TimeoutMs)*time.Millisecond)
			defer cancel()
		}
		val, err := invokeTool(ctx, tool, ec.Request.Input)
		if errors.Is(ctx.Err(), context.DeadlineExceeded) && ec.Ctx.Err() == nil {
			err = &TimeoutError{Code: ToolTimeoutCode, Budget: time.Duration(tool.TimeoutMs) * time.Millisecond, Cause: ErrToolTimeout}
			ec.Result.ErrorCode = ToolTimeoutCode
		}
		if err != nil {
			ec.Result.IsError = true
			ec.Result.Error = err.Error()
		} else {
			ec.Result.Value = val
		}
		return next()
	}
}

// WithTool copies the pipeline and appends the tool's execute stage.
func (p *Pipeline) WithTool(tool *Tool) *Pipeline {
	if p.registered == nil {
		p.registered = make(map[string]*Tool)
	}
	if tool != nil {
		p.registered[tool.Name] = tool
	}
	return p
}

// contextWithMeta exposes ExecContext metadata and signals to tool implementations.
func contextWithMeta(ec *ExecContext) context.Context {
	return withExecContext(context.Background(), ec)
}

// execContextKey identifies ExecContext within a context.
type execContextKey struct{}

// withExecContext returns a context carrying ExecContext.
func withExecContext(ctx context.Context, ec *ExecContext) context.Context {
	return context.WithValue(ctx, execContextKey{}, ec)
}

// ExecContextFrom retrieves execution state inside a tool implementation.
func ExecContextFrom(ctx context.Context) (*ExecContext, bool) {
	ec, ok := ctx.Value(execContextKey{}).(*ExecContext)
	return ec, ok
}

// Lookup resolves a tool registered during pipeline construction.
func (p *Pipeline) Lookup(name string) *Tool { return p.registered[name] }

// Tools returns registered tools in stable name order for model requests.
func (p *Pipeline) Tools() []*Tool {
	names := make([]string, 0, len(p.registered))
	for name := range p.registered {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]*Tool, 0, len(names))
	for _, name := range names {
		out = append(out, p.registered[name])
	}
	return out
}

func (p *Pipeline) dispatch(ctx context.Context, ec *ExecContext, tool *Tool) {
	err := p.execute.RunWithTerminal(ctx, ec, func() error {
		if ec.Signal == SignalCancel || ec.Denied {
			return nil
		}
		if ctx.Err() != nil {
			ec.Result = AbortedBeforeDispatchResult(ec.Request.CallID)
			return nil
		}
		if tool == nil {
			tool = p.Lookup(ec.Request.Tool)
		}
		if tool != nil && tool.Schema != nil {
			if err := tool.Schema.Validate(ec.Request.Input); err != nil {
				return err
			}
		}
		return executeInner(tool)(ec, func() error { return nil })
	})
	if err != nil {
		ec.Result.IsError = true
		ec.Result.Error = err.Error()
	}
}

func (p *Pipeline) runStages(ctx context.Context, ec *ExecContext, tool *Tool) *ToolCallResult {
	fail := func(err error) bool {
		if err == nil {
			return false
		}
		ec.Result.IsError = true
		ec.Result.Error = err.Error()
		return true
	}
	if ctx.Err() != nil {
		return AbortedBeforeDispatchResult(ec.Request.CallID)
	}
	if fail(p.pre.Run(ctx, ec)) {
		return ec.Result
	}
	if ec.Denied {
		fail(fmt.Errorf("denied by pre-execute middleware"))
		return ec.Result
	}
	p.dispatch(ctx, ec, tool)
	if ec.Signal == SignalCancel {
		ec.Result.IsError = true
	}
	fail(p.post.Run(ctx, ec))
	if ec.Denied {
		ec.Result.IsError = true
		if ec.Result.Error == "" {
			ec.Result.Error = "blocked by post-execute middleware"
		}
	}
	fail(p.result.Run(ctx, ec))
	return ec.Result
}

func invokeTool(ctx context.Context, tool *Tool, input map[string]any) (value any, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("tool %q panicked: %v", tool.Name, recovered)
		}
	}()
	return tool.Execute(ctx, input)
}

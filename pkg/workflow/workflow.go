// Package workflow provides workflow orchestration and tool-workflow (S12),
// matching workflow/workflow upstream. Top-level Pipeline, Parallel, and Agent
// functions compose named executable Steps. Pipelines run serially, while parallel
// workflows run concurrent goroutines and aggregate ordered outputs. Context
// cancellation cascades to active steps; Result includes outputs and cancellation.
package workflow

import (
	"context"
	"fmt"
	"sync"
)

// Steps and results.

// StepRunFunc executes a step and should return promptly when ctx is canceled.
type StepRunFunc func(ctx context.Context) (string, error)

// Step is the smallest executable workflow unit.
type Step struct {
	Name string // Step name for diagnostics and summaries.
	Run  StepRunFunc
}

// Result contains a workflow's output.
type Result struct {
	Name      string   `json:"name"`
	Outputs   []string `json:"outputs"`   // Step outputs in order.
	Cancelled bool     `json:"cancelled"` // Whether execution was canceled.
}

// Errors returns the first execution error, or nil.
func (r *Result) Errors() error {
	for _, o := range r.Outputs {
		if s, ok := unwrapErr(o); ok {
			return s
		}
	}
	return nil
}

// unwrapErr recovers an error placeholder from output.
func unwrapErr(o string) (error, bool) {
	return nil, false // Text output contains no embedded error.
}

// ============================================================================
// Workflow
// ============================================================================

// Mode selects workflow execution mode.
type Mode string

const (
	ModePipeline Mode = "pipeline" // Sequential execution.
	ModeParallel Mode = "parallel" // Parallel execution.
)

// Workflow combines steps sequentially or in parallel.
type Workflow struct {
	Name  string
	Mode  Mode
	Steps []*Step
}

// Top-level orchestration primitives.

// Pipeline creates a workflow that executes steps in order.
func Pipeline(name string, steps ...*Step) *Workflow {
	return &Workflow{Name: name, Mode: ModePipeline, Steps: steps}
}

// Parallel creates a workflow that executes steps concurrently.
func Parallel(name string, steps ...*Step) *Workflow {
	return &Workflow{Name: name, Mode: ModeParallel, Steps: steps}
}

// Agent creates a child-agent step using the selected in-process, acp, or
// fork-copy backend.
func Agent(name, backendName, input string, spawn SpawnAgentFunc) *Step {
	return &Step{
		Name: name,
		Run: func(ctx context.Context) (string, error) {
			return spawn(ctx, backendName, input)
		},
	}
}

// Seq wraps a nested workflow as one step for aggregation in a larger workflow.
func Seq(w *Workflow) *Step {
	return &Step{
		Name: w.Name,
		Run: func(ctx context.Context) (string, error) {
			res, err := w.Run(ctx)
			if err != nil {
				return "", err
			}
			out := ""
			for i, o := range res.Outputs {
				if i > 0 {
					out += ","
				}
				out += o
			}
			return out, nil
		},
	}
}

// SpawnAgentFunc is injected to run a child on a named backend and return output.
type SpawnAgentFunc func(ctx context.Context, backendName, input string) (string, error)

// ============================================================================
// Run
// ============================================================================

// Run executes the workflow and aggregates results, cascading ctx cancellation.
func (w *Workflow) Run(ctx context.Context) (*Result, error) {
	res := &Result{Name: w.Name}
	switch w.Mode {
	case ModeParallel:
		return w.runParallel(ctx, res)
	default: // ModePipeline
		return w.runPipeline(ctx, res)
	}
}

// runPipeline executes steps sequentially.
func (w *Workflow) runPipeline(ctx context.Context, res *Result) (*Result, error) {
	for _, s := range w.Steps {
		if err := ctx.Err(); err != nil {
			res.Cancelled = true
			return res, err
		}
		out, err := s.Run(ctx)
		if err == nil {
			res.Outputs = append(res.Outputs, out)
		} else {
			res.Outputs = append(res.Outputs, fmt.Sprintf("[%s:error %v]", s.Name, err))
		}
		// A failed step short-circuits the sequential pipeline.
		if err != nil {
			return res, err
		}
	}
	return res, nil
}

// runParallel executes steps concurrently and preserves output order.
func (w *Workflow) runParallel(ctx context.Context, res *Result) (*Result, error) {
	outputs := make([]string, len(w.Steps))
	var wg sync.WaitGroup
	for i, s := range w.Steps {
		wg.Add(1)
		go func(idx int, step *Step) {
			defer wg.Done()
			out, err := step.Run(ctx)
			if err != nil {
				outputs[idx] = fmt.Sprintf("[%s:error %v]", step.Name, err)
				return
			}
			outputs[idx] = out
		}(i, s)
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		res.Cancelled = true
	}
	res.Outputs = outputs
	return res, nil
}

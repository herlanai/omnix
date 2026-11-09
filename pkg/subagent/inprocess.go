// The S02 in-process fork backend reuses process context, including the LLM
// adapter and tool pipeline, to execute a subtask and return lineage and output.
// It backs the in-process acceptance tests; ACP and fork-copy remain interface stubs.
package subagent

import (
	"context"
	"sync"

	"github.com/herlanai/omnix/pkg/brand"
)

// InProcessRunner executes the injected transactional subtask implementation
// and returns the child session ID, output text, and error.
type InProcessRunner func(ctx context.Context, req SpawnRequest) (sid brand.SessionID, output string, err error)

// InProcessProvider implements in-process forks.
type InProcessProvider struct {
	runner InProcessRunner
	mu     sync.Mutex
}

// NewInProcessProvider creates a provider; nil runner uses a synchronous placeholder.
func NewInProcessProvider(runner InProcessRunner) *InProcessProvider {
	return &InProcessProvider{runner: runner}
}

// Name implements Provider.
func (p *InProcessProvider) Name() string { return "in-process" }

// Spawn runs the subtask through runner and returns its handle.
func (p *InProcessProvider) Spawn(ctx context.Context, req SpawnRequest) (*Handle, error) {
	runner := p.runner
	if runner == nil {
		// The placeholder uses input as the session ID and echoes it as output.
		defaultRunner := func(_ context.Context, r SpawnRequest) (brand.SessionID, string, error) {
			sid := brand.NewSessionID("inprocess-" + r.Input)
			return sid, "in-process:" + r.Input, nil
		}
		runner = defaultRunner
	}

	sid, output, err := runner(ctx, req)
	h := newHandle("in-process", ForkLineage{Parent: req.Parent, Session: sid})
	if err != nil {
		// Failures still return an error-marked, immediately drained handle.
		h.markDrained()
		return h, err
	}
	_ = output
	// Mark synchronous execution complete immediately; no background goroutine remains.
	h.markDrained()
	return h, nil
}

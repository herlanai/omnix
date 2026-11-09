// This file implements Agent Initiator attribution (M33), following the upstream
// agent initiator module. WithInitiator adds agent identity and operation to
// the context; RequireInitiator rejects anonymous calls with a structured
// Unauthorized error. WithoutInitiator explicitly removes identity when
// entering an uncontrolled execution scope.
package agent

import (
	"context"
	"fmt"

	"github.com/herlanai/omnix/pkg/brand"
)

// initiatorKey is the context key type for initiator attribution.
type initiatorKey struct{}

// Initiator identifies the agent initiating an operation.
type Initiator struct {
	// AgentID is the initiating agent's session ID.
	AgentID brand.SessionID
	// Op names the operation, such as Run, Followup, or executeTool.
	Op string
}

// UnauthorizedInitiatorError indicates a RequireInitiator call without an initiator.
type UnauthorizedInitiatorError struct {
	Op string
}

// Error implements the error interface.
func (e *UnauthorizedInitiatorError) Error() string {
	return fmt.Sprintf("agent: unauthorized initiator: operation %q called without withInitiator context", e.Op)
}

// WithInitiator returns a context carrying the initiator.
func WithInitiator(ctx context.Context, agentID brand.SessionID, op string) context.Context {
	return context.WithValue(ctx, initiatorKey{}, &Initiator{AgentID: agentID, Op: op})
}

// RequireInitiator returns the initiator or UnauthorizedInitiatorError.
// Use it at operation boundaries requiring security attribution.
func RequireInitiator(ctx context.Context) (*Initiator, error) {
	ini, ok := ctx.Value(initiatorKey{}).(*Initiator)
	if !ok || ini == nil {
		return nil, &UnauthorizedInitiatorError{Op: "unknown"}
	}
	return ini, nil
}

// WithoutInitiator returns a context with initiator identity removed.
func WithoutInitiator(ctx context.Context) context.Context {
	return context.WithValue(ctx, initiatorKey{}, nil)
}

// MustInitiator panics if no initiator exists; use only where attribution is guaranteed.
func MustInitiator(ctx context.Context) *Initiator {
	ini, err := RequireInitiator(ctx)
	if err != nil {
		panic(err)
	}
	return ini
}

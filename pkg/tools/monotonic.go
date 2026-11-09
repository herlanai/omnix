// The monotonic guard sits between pre-execute policy middleware and execution. Once assigned, a
// decision may only become stricter: deny > ask > allow. The first explicit decision may resolve
// the initial ask state; later updates cannot relax it. Deny is final, while allow can still be
// tightened to ask or deny.
package tools

import (
	"errors"
)

// ErrGuardRelaxed reports an attempt to relax an established guard decision.
var ErrGuardRelaxed = errors.New("tools monotonic guard: cannot relax an already stricter decision")

// guardRank assigns larger ranks to stricter decisions.
func guardRank(d PreToolDecision) int {
	switch d {
	case PreDeny:
		return 2
	case PreAsk:
		return 1
	default: // PreAllow
		return 0
	}
}

// MonotonicGuard converges policy decisions for a single tool call.
type MonotonicGuard struct {
	current PreToolDecision
	settled bool // Whether a decision has been explicitly assigned.
}

// NewMonotonicGuard creates an unassigned guard with ask semantics.
func NewMonotonicGuard() *MonotonicGuard {
	return &MonotonicGuard{current: PreAsk}
}

// Update accepts the first assignment and subsequent decisions with an equal or higher
// strictness rank. Attempts to relax the decision return ErrGuardRelaxed without changing the
// existing decision.
func (g *MonotonicGuard) Update(next PreToolDecision) error {
	if !g.settled {
		g.current = next
		g.settled = true
		return nil
	}
	if guardRank(next) < guardRank(g.current) {
		return ErrGuardRelaxed
	}
	g.current = next
	return nil
}

// Decision returns the current guard decision.
func (g *MonotonicGuard) Decision() PreToolDecision {
	if !g.settled {
		return PreAsk
	}
	return g.current
}

// IsDenied reports a fail-closed denial.
func (g *MonotonicGuard) IsDenied() bool {
	return g.settled && g.current == PreDeny
}

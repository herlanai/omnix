// This file follows upstream packages/core/agent/src/consumed-work.ts.
// It accounts for consumed work that Turn/Step events alone cannot distinguish:
// a turn aborted before its first step differs from a balanced no-op turn
// created by a rejected or empty claim. Inbox removedCount and canceled
// outcomes distinguish claimed input from work discarded before execution.
package agent

import (
	"github.com/herlanai/omnix/pkg/session"
)

// ConsumedWork accounts for work consumed by an agent log.
type ConsumedWork struct {
	// End is the last closed turn that accounted for consumed work: it entered a
	// model step or claimed inbox input and then failed, aborted, or was blocked.
	// It is nil if no turn accounted for work.
	End *session.SessionEvent
	// DroppedUnrun reports accepted inbox work canceled after End without running.
	// No turn/end event exists for input canceled before a turn could start.
	DroppedUnrun bool
}

// accountsForClaim reports whether a turn ending before its first step accounts
// for claimed input. Completed does not: a rewritten claim may leave no work.
// Blocked does account for input discarded by a pre-step rejection.
func accountsForClaim(reason session.TurnEndReason) bool {
	switch reason {
	case session.ReasonCompleted, session.ReasonFinished:
		return false
	case session.ReasonBlocked, session.ReasonAborted, session.ReasonInterrupted, session.ReasonError:
		return true
	default:
		// An unrecognized ending after consuming input must not be treated as success.
		// Max-tokens requires a step and cannot reach this path; backend extensions may.
		return true
	}
}

// FoldConsumedWork accounts for consumed work in one pass over an agent log or
// an owned suffix. It uses only persisted events, so callers need not sample
// live state before cancellation; teardown, parent interrupts, and plugin
// unloading all have the same interpretation.
func FoldConsumedWork(events []session.SessionEvent) ConsumedWork {
	stepped := make(map[uint64]struct{})
	claimed := make(map[uint64]struct{})
	var open uint64
	hasOpen := false
	var end *session.SessionEvent
	droppedUnrun := false

	for i := range events {
		ev := &events[i]
		switch ev.Type {
		case session.EventTurnStart:
			if d, ok := ev.Data.(session.TurnStartData); ok {
				open = d.Turn
				hasOpen = true
			}
		case session.EventStepStart:
			if d, ok := ev.Data.(session.StepStartData); ok {
				stepped[d.Turn] = struct{}{}
			}
		case session.EventAgentInboxSpliced:
			d, ok := ev.Data.(session.InboxSplicedData)
			if !ok || d.RemovedCount == 0 {
				continue
			}
			if d.Outcome == "canceled" {
				// Replacement retains pending work under a new identity; only cancellation leaving nothing counts as a drop.
				droppedUnrun = droppedUnrun || len(d.Inserted) == 0
			} else if hasOpen {
				// The loop claims input at a step boundary, always inside a turn.
				claimed[open] = struct{}{}
			}
		case session.EventTurnEnd:
			d, ok := ev.Data.(session.TurnEndData)
			if !ok {
				continue
			}
			hasOpen = false
			_, didStep := stepped[d.Turn]
			_, didClaim := claimed[d.Turn]
			if didStep || (didClaim && accountsForClaim(d.Reason)) {
				cp := *ev
				end = &cp
				// The turn's outcome accounts for drops before it closes; only later drops remain unaccounted for.
				droppedUnrun = false
			}
			delete(stepped, d.Turn)
			delete(claimed, d.Turn)
		}
	}

	return ConsumedWork{End: end, DroppedUnrun: droppedUnrun}
}

// This file implements the five Agent cancellation categories (M18), following
// upstream packages/core/agent/Agent.ts. Cancellation is recorded as
// "cancel:<cause>" in turn-stopping; ExtractCancelCause recovers the category,
// and turn/end closes the turn as aborted.
package agent

import (
	"strings"

	"github.com/herlanai/omnix/pkg/session"
)

// cancelReasonPrefix prefixes cancellation reasons in turn-stopping events.
const cancelReasonPrefix = "cancel:"

// RecordCancel records the cancellation cause in a turn-stopping event.
func RecordCancel(sl *session.SessionLog, cause CancelCause) error {
	_, err := sl.Append(session.TurnStoppingData{Reason: cancelReasonPrefix + string(cause)})
	return err
}

// ExtractCancelCause returns the last cancellation cause in the event log.
// The boolean reports whether a cancellation record was found.
func ExtractCancelCause(events []session.SessionEvent) (CancelCause, bool) {
	var found CancelCause
	ok := false
	for _, ev := range events {
		if ev.Type != session.EventTurnStopping {
			continue
		}
		d, isData := ev.Data.(session.TurnStoppingData)
		if !isData || !strings.HasPrefix(d.Reason, cancelReasonPrefix) {
			continue
		}
		found = CancelCause(strings.TrimPrefix(d.Reason, cancelReasonPrefix))
		ok = true
	}
	return found, ok
}

// AllCancelCauses returns all five causes for validation and tests.
func AllCancelCauses() []CancelCause {
	return []CancelCause{CancelUser, CancelParent, CancelHook, CancelDisposed, CancelLegacy}
}

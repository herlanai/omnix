// Inbox follows upstream packages/core/agent/src/inbox.ts.
// It incrementally projects two ordered pending-input lists owned by Agent:
// next-turn holds prompts awaiting a turn, and next-step holds input awaiting
// the next step boundary.
package agent

import (
	"sync"

	"github.com/herlanai/omnix/pkg/session"
)

// InboxNotifications publishes live inbox changes.
type InboxNotifications struct {
	// Inserted publishes an inserted message.
	Inserted func(message UserMessage)
	// Discarded publishes a discarded message.
	Discarded func(message UserMessage)
	// Claimed publishes a claimed message within its owning turn.
	Claimed func(message UserMessage, turn uint64)
}

// Inbox incrementally projects persisted agent inbox events into
// next-turn and next-step lists, updated by splice operations.
type Inbox struct {
	mu            sync.Mutex
	nextTurn      []UserMessage
	nextStep      []UserMessage
	notifications InboxNotifications
}

// NewInbox creates an inbox and replays all agent/inbox/spliced events in the log.
func NewInbox(log *session.SessionLog, notifications InboxNotifications) *Inbox {
	inbox := &Inbox{
		notifications: notifications,
	}
	if log != nil {
		// Replay persisted events.
		for _, ev := range log.Events() {
			if ev.Type == session.EventAgentInboxSpliced {
				if data, ok := ev.Data.(session.InboxSplicedData); ok {
					inbox.apply(data)
				}
			}
		}
	}
	return inbox
}

// NextTurn returns pending turn input for read-only use.
func (inbox *Inbox) NextTurn() []UserMessage {
	inbox.mu.Lock()
	defer inbox.mu.Unlock()
	result := make([]UserMessage, len(inbox.nextTurn))
	copy(result, inbox.nextTurn)
	return result
}

// NextStep returns input awaiting the next step boundary for read-only use.
func (inbox *Inbox) NextStep() []UserMessage {
	inbox.mu.Lock()
	defer inbox.mu.Unlock()
	result := make([]UserMessage, len(inbox.nextStep))
	copy(result, inbox.nextStep)
	return result
}

// HasPending reports whether either pending-input list contains work.
func (inbox *Inbox) HasPending() bool {
	inbox.mu.Lock()
	defer inbox.mu.Unlock()
	return len(inbox.nextTurn) > 0 || len(inbox.nextStep) > 0
}

// Clear persists cancellation of all pending input, clearing next-step before next-turn.
func (inbox *Inbox) Clear(log *session.SessionLog) {
	inbox.splice(log, session.InboxNextStep, 0, len(inbox.nextStep), nil, "canceled")
	inbox.splice(log, session.InboxNextTurn, 0, len(inbox.nextTurn), nil, "canceled")
}

// Claim removes and returns the complete proposed step batch and publishes
// each claimed message. The persisted splice only deletes entries.
// Target determines whether to consume a queued turn as well; turn identifies
// the owning turn. Next-step inputs precede the queued turn, if requested.
func (inbox *Inbox) Claim(log *session.SessionLog, target session.InboxTarget, turn uint64) []UserMessage {
	inbox.mu.Lock()
	claimed := inbox.mutate(session.InboxNextStep, 0, len(inbox.nextStep), nil)
	if target == session.InboxNextTurn {
		claimed = append(claimed, inbox.mutate(session.InboxNextTurn, 0, 1, nil)...)
	}
	inbox.mu.Unlock()

	// Publish claim notifications.
	if inbox.notifications.Claimed != nil {
		for _, msg := range claimed {
			inbox.notifications.Claimed(msg, turn)
		}
	}

	// Persist the removal.
	if log != nil {
		if len(claimed) > 0 {
			// Compute the number removed from the pending lists.
			stepRemoved := len(inbox.NextStep()) // This is the post-removal count, not the removed count.
			_ = stepRemoved
			// Record the splice event directly.
			_, _ = log.Append(session.InboxSplicedData{
				Target:       session.InboxNextStep,
				Start:        0,
				RemovedCount: len(claimed),
				Inserted:     nil,
			})
		}
	}

	return claimed
}

// Push appends a message to the selected list and persists the change.
func (inbox *Inbox) Push(log *session.SessionLog, target session.InboxTarget, message UserMessage) {
	inbox.splice(log, target, -1, 0, []UserMessage{message}, "")
}

// splice persists removal of removedCount entries at start and insertion of inserted.
// A start of -1 appends at the end.
func (inbox *Inbox) splice(log *session.SessionLog, target session.InboxTarget, start, removedCount int, inserted []UserMessage, outcome string) {
	inbox.mu.Lock()
	removed := inbox.mutate(target, start, removedCount, inserted)
	inbox.mu.Unlock()

	// Publish notifications.
	if inbox.notifications.Discarded != nil {
		for _, msg := range removed {
			inbox.notifications.Discarded(msg)
		}
	}
	if inbox.notifications.Inserted != nil {
		for _, msg := range inserted {
			inbox.notifications.Inserted(msg)
		}
	}

	// Persist the change.
	if log != nil {
		insertedContents := make([]string, len(inserted))
		for i, msg := range inserted {
			insertedContents[i] = msg.Content
		}
		_, _ = log.Append(session.InboxSplicedData{
			Target:       target,
			Start:        start,
			RemovedCount: removedCount,
			Inserted:     insertedContents,
			Outcome:      outcome,
		})
	}
}

// mutate updates the list and returns removed messages. The caller must hold the lock.
func (inbox *Inbox) mutate(target session.InboxTarget, start, removedCount int, inserted []UserMessage) []UserMessage {
	var list *[]UserMessage
	switch target {
	case session.InboxNextTurn:
		list = &inbox.nextTurn
	case session.InboxNextStep:
		list = &inbox.nextStep
	default:
		return nil
	}

	if start == -1 {
		start = len(*list)
	}

	// Check bounds.
	if start < 0 {
		start = 0
	}
	if start > len(*list) {
		start = len(*list)
	}
	if removedCount < 0 {
		removedCount = 0
	}
	if start+removedCount > len(*list) {
		removedCount = len(*list) - start
	}

	// Save the removed messages.
	removed := make([]UserMessage, removedCount)
	copy(removed, (*list)[start:start+removedCount])

	// Build the replacement list.
	newList := make([]UserMessage, 0, len(*list)-removedCount+len(inserted))
	newList = append(newList, (*list)[:start]...)
	newList = append(newList, inserted...)
	newList = append(newList, (*list)[start+removedCount:]...)
	*list = newList

	return removed
}

// apply replays a persisted splice event into internal state.
func (inbox *Inbox) apply(data session.InboxSplicedData) {
	inbox.mu.Lock()
	defer inbox.mu.Unlock()

	// Convert content strings into UserMessage values.
	inserted := make([]UserMessage, len(data.Inserted))
	for i, content := range data.Inserted {
		inserted[i] = UserMessage{Content: content}
	}

	inbox.mutate(data.Target, data.Start, data.RemovedCount, inserted)
}

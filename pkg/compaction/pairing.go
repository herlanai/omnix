// Tool pairing matches compaction/tool-pairing upstream. Safe cut points depend
// on balanced tool calls and results, not just step markers. Cutting between
// a call and its result leaves invalid history that can stall the model.
package compaction

import (
	"errors"

	"github.com/herlanai/omnix/pkg/session"
)

// ErrUnbalanced indicates an unmatched tool result or unbalanced history.
var ErrUnbalanced = errors.New("compaction: unbalanced tool pairing")

// toolCallDelta counts outstanding calls: assistant messages add declared calls,
// tool results subtract one, and other events contribute zero.
func toolCallDelta(ev session.SessionEvent) int {
	switch ev.Type {
	case session.EventAssistantMessage:
		if d, ok := ev.Data.(session.AssistantMessageData); ok {
			return len(d.ToolCallIDs)
		}
	case session.EventToolResult:
		return -1
	}
	return 0
}

// BalancedCuts reports balance at N+1 cuts for N events: cut[0] precedes all
// events, cut[i] follows event i, and cut[N] is last. Zero outstanding calls is balanced.
func BalancedCuts(events []session.SessionEvent) ([]bool, error) {
	cuts := make([]bool, len(events)+1)
	inProgress := 0
	cuts[0] = true
	for i, ev := range events {
		inProgress += toolCallDelta(ev)
		if inProgress < 0 {
			return nil, ErrUnbalanced
		}
		cuts[i+1] = inProgress == 0
	}
	return cuts, nil
}

// IsBalancedAt checks a cut in the range 0..len(events).
func IsBalancedAt(events []session.SessionEvent, cut int) (bool, error) {
	cuts, err := BalancedCuts(events)
	if err != nil {
		return false, err
	}
	if cut < 0 || cut >= len(cuts) {
		return false, ErrUnbalanced
	}
	return cuts[cut], nil
}

// NearestBalancedFrom finds the first balanced cut at or after start, or -1.
// It advances a proposed compaction boundary without splitting tool pairs.
func NearestBalancedFrom(events []session.SessionEvent, start int) int {
	cuts, err := BalancedCuts(events)
	if err != nil {
		return -1
	}
	for i := start; i < len(cuts); i++ {
		if cuts[i] {
			return i
		}
	}
	return -1
}

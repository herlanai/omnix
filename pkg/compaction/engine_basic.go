// BasicEngine is the deterministic, LLM-free summarizer for S01. It extracts
// user messages into a past-conversation summary bounded by maxRunes. This keeps
// compaction available offline or without credentials; LLMEngine can replace it
// when more concise model-generated summaries are desired.
package compaction

import (
	"context"
	"sort"
	"strings"

	"github.com/herlanai/omnix/pkg/session"
)

// BasicEngineSumm is the summary title or prefix used by BasicEngine.
const BasicEngineSumm = "【摘要】过去对话摘要如下："

// BasicEngine produces deterministic summaries without an LLM.
type BasicEngine struct{}

// Summarize uses the first few user messages, truncated to maxRunes.
func (b *BasicEngine) Summarize(ctx context.Context, events []session.SessionEvent, maxRunes int) (string, error) {
	_ = ctx
	// Collect user messages in sequence order.
	type pair struct {
		seq uint64
		msg string
	}
	msgs := []pair{}
	// Copy first to avoid modifying the input.
	cp := append([]session.SessionEvent(nil), events...)
	sort.SliceStable(cp, func(i, j int) bool { return cp[i].Seq < cp[j].Seq })
	for _, ev := range cp {
		if ev.Type == session.EventUserMessage {
			if d, ok := ev.Data.(session.UserMessageData); ok {
				msgs = append(msgs, pair{ev.Seq, d.Content})
			}
		}
	}
	// Use at most the first three messages as representatives.
	const maxItems = 3
	var sb strings.Builder
	sb.WriteString(BasicEngineSumm)
	for i, m := range msgs {
		if i >= maxItems {
			break
		}
		sb.WriteString(m.msg)
		if i < maxItems-1 && i < len(msgs)-1 {
			sb.WriteString("；")
		}
	}
	return truncateRunes(sb.String(), maxRunes), nil
}

// truncateRunes truncates by rune and appends an ellipsis.
func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

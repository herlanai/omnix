package agent

import (
	"encoding/json"

	"github.com/herlanai/omnix/pkg/llm"
	"github.com/herlanai/omnix/pkg/session"
)

// deriveModelMessages rebuilds model history from durable, effective events.
// Streaming chunks are presentation events; only finalized messages are replayed.
func deriveModelMessages(events []session.SessionEvent) []llm.Message {
	effective := session.EffectiveEvents(events)
	calls := map[string]*llm.ToolCall{}
	results := map[string]bool{}
	for _, ev := range effective {
		switch d := ev.Data.(type) {
		case session.ToolCallData:
			var input map[string]any
			if len(d.Input) > 0 {
				if json.Unmarshal(d.Input, &input) != nil {
					continue
				}
			}
			calls[d.CallID.Raw()] = &llm.ToolCall{ID: d.CallID.Raw(), Name: d.Tool, Input: input}
		case session.ToolResultData:
			results[d.CallID.Raw()] = true
		}
	}
	var messages []llm.Message
	included := map[string]bool{}
	for _, ev := range effective {
		switch d := ev.Data.(type) {
		case session.UserMessageData:
			messages = append(messages, llm.NewUserMessage(d.Content))
		case session.AssistantMessageData:
			m := llm.Message{Role: llm.RoleAssistant}
			if d.Reasoning != "" {
				m.Content = append(m.Content, llm.Reasoning(d.Reasoning))
			}
			if d.Content != "" {
				m.Content = append(m.Content, llm.Text(d.Content))
			}
			for _, id := range d.ToolCallIDs {
				if call := calls[id]; call != nil && results[id] {
					m.Content = append(m.Content, llm.ToolUse(call))
					included[id] = true
				}
			}
			if len(m.Content) > 0 {
				messages = append(messages, m)
			}
		case session.ToolResultData:
			id := d.CallID.Raw()
			if included[id] {
				messages = append(messages, llm.Message{Role: llm.RoleUser, Content: []llm.ContentBlock{llm.ToolResult(&llm.ToolResultContent{ToolUseID: id, Content: d.Output, IsError: d.IsError})}})
			}
		}
	}
	return messages
}

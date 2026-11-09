package provider_deepseek

import (
	"encoding/json"
	"github.com/herlanai/omnix/pkg/llm"
	"testing"
)

func TestPayloadToolRoundTrip(t *testing.T) {
	d := &DeepSeek{}
	p := d.buildPayload(llm.ChatRequest{Messages: []llm.Message{
		{Role: llm.RoleAssistant, Content: []llm.ContentBlock{llm.Reasoning("reason"), llm.Text("checking"), llm.ToolUse(&llm.ToolCall{ID: "a", Name: "lookup", Input: map[string]any{"q": "x"}}), llm.ToolUse(&llm.ToolCall{ID: "b", Name: "lookup", Input: map[string]any{}})}},
		{Role: llm.RoleUser, Content: []llm.ContentBlock{llm.ToolResult(&llm.ToolResultContent{ToolUseID: "a", Content: "first"}), llm.ToolResult(&llm.ToolResultContent{ToolUseID: "b", Content: "second"})}},
	}})
	if len(p.Messages) != 3 {
		t.Fatalf("messages=%+v", p.Messages)
	}
	m := p.Messages[0]
	if len(m.ToolCalls) != 2 || m.ReasoningContent != "reason" || m.Content != "checking" {
		t.Fatalf("assistant=%+v", m)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(m.ToolCalls[0].Function.Arguments), &args); err != nil || args["q"] != "x" {
		t.Fatalf("arguments=%v err=%v", args, err)
	}
	for i, id := range []string{"a", "b"} {
		if m := p.Messages[i+1]; m.Role != "tool" || m.ToolCallID != id {
			t.Fatalf("result=%+v", m)
		}
	}
}

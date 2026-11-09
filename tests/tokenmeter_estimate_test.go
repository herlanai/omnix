// Fixed-density token estimation tests.
package tests

import (
	"testing"

	"github.com/herlanai/omnix/pkg/llm"
	"github.com/herlanai/omnix/pkg/tokenmeter"
)

func TestEstimateTextMessage(t *testing.T) {
	// Eight text characters cost ceil(8/4)=2 tokens plus four block and four role tokens, totaling
	// ten.
	msg := llm.NewUserMessage("12345678")
	got := tokenmeter.EstimateMessage(msg)
	if got != 10 {
		t.Fatalf("expected 10 tokens, got %d", got)
	}
}

func TestEstimateContentText(t *testing.T) {
	blocks := []llm.ContentBlock{llm.Text("abcd")}
	// ceil(4/4)=1 + 4 = 5
	if got := tokenmeter.EstimateContent(blocks); got != 5 {
		t.Fatalf("expected 5, got %d", got)
	}
}

func TestEstimateToolUseBlock(t *testing.T) {
	blocks := []llm.ContentBlock{
		llm.ToolUse(&llm.ToolCall{Name: "ls", Input: map[string]any{"a": 1}}),
	}
	got := tokenmeter.EstimateContent(blocks)
	if got <= 0 {
		t.Fatalf("tool-use block should cost positive tokens, got %d", got)
	}
}

func TestEstimateMessagesSum(t *testing.T) {
	msgs := []llm.Message{
		llm.NewUserMessage("hello"),
		llm.NewAssistantText("hi"),
	}
	if got := tokenmeter.EstimateMessages(msgs); got <= 0 {
		t.Fatalf("sum should be positive, got %d", got)
	}
}

func TestEstimateEmpty(t *testing.T) {
	if got := tokenmeter.EstimateContent(nil); got != 0 {
		t.Fatalf("empty content should be 0, got %d", got)
	}
}

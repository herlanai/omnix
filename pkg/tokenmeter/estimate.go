// Fixed-density token estimation matches token-meter/src/estimate.ts. Before
// actual tokenization, shared heuristics use one token per four text bytes plus
// block structure and message-role overhead so meters and projections agree.
package tokenmeter

import (
	"encoding/json"
	"math"

	"github.com/herlanai/omnix/pkg/llm"
)

const (
	charsPerToken = 4 // Fixed text density.
	blockOverhead = 4 // Per-block JSON framing and type-tag overhead.
	// RoleOverhead accounts for per-message role framing.
	RoleOverhead = 4
)

// ceilDiv rounds the text-length-to-token conversion upward.
func ceilDiv(n int) int {
	return int(math.Ceil(float64(n) / float64(charsPerToken)))
}

// EstimateContent estimates content tokens with fixed density and block overhead.
func EstimateContent(blocks []llm.ContentBlock) int {
	tokens := 0
	for _, b := range blocks {
		switch b.Kind {
		case llm.BlockText, llm.BlockReasoning:
			tokens += ceilDiv(len(b.Text)) + blockOverhead
		case llm.BlockToolUse:
			if b.ToolCall != nil {
				tokens += ceilDiv(len(b.ToolCall.Name))
				if raw, err := json.Marshal(b.ToolCall.Input); err == nil {
					tokens += ceilDiv(len(raw))
				}
			}
			tokens += blockOverhead
		case llm.BlockToolResult:
			if b.ToolResult != nil {
				// Estimate string result content using text density.
				tokens += ceilDiv(len(b.ToolResult.Content))
			}
			tokens += blockOverhead
		default:
			// Conservatively estimate unknown blocks, such as images, from JSON structure.
			if raw, err := json.Marshal(b); err == nil {
				tokens += blockOverhead + ceilDiv(len(raw))
			} else {
				tokens += blockOverhead
			}
		}
	}
	return tokens
}

// EstimateMessage includes content and role overhead for a model-visible message.
func EstimateMessage(msg llm.Message) int {
	return EstimateContent(msg.Content) + RoleOverhead
}

// EstimateMessages estimates tokens for the entire conversation history.
func EstimateMessages(msgs []llm.Message) int {
	total := 0
	for _, m := range msgs {
		total += EstimateMessage(m)
	}
	return total
}

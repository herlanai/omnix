package provider_deepseek

import (
	"context"
	"fmt"
	"github.com/herlanai/omnix/pkg/llm"
	"strings"
	"testing"
)

func TestStreamingToolArgumentsAreAssembledByIndex(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"a","function":{"name":"lookup","arguments":"{\"q\":"}},{"index":1,"id":"b","function":{"name":"lookup","arguments":"{\"q\":\"two\"}"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"one\"}"}}]}}]}`,
		`data: {"usage":{"prompt_tokens":7,"completion_tokens":3}}`,
		`data: [DONE]`,
		"",
	}, "\n")
	var chunks []llm.StreamChunk
	usage, err := (&DeepSeek{}).readSSE(context.Background(), strings.NewReader(stream), func(c llm.StreamChunk) { chunks = append(chunks, c) })
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 3 || chunks[2].Kind != llm.ChunkDone {
		t.Fatalf("chunks=%+v", chunks)
	}
	if chunks[0].ToolCall.ID != "a" || chunks[0].ToolCall.Input["q"] != "one" || chunks[1].ToolCall.ID != "b" || chunks[1].ToolCall.Input["q"] != "two" {
		t.Fatalf("calls=%+v %+v", chunks[0].ToolCall, chunks[1].ToolCall)
	}
	if usage.PromptTokens != 7 {
		t.Fatalf("usage=%+v", usage)
	}
}

func TestMalformedArgumentsNeverDispatch(t *testing.T) {
	for _, args := range []string{`{`, `null`, `[]`} {
		t.Run(args, func(t *testing.T) {
			stream := fmt.Sprintf("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"id\":\"a\",\"function\":{\"name\":\"lookup\",\"arguments\":%q}}]}}]}\ndata: [DONE]\n", args)
			emitted := 0
			_, err := (&DeepSeek{}).readSSE(context.Background(), strings.NewReader(stream), func(llm.StreamChunk) { emitted++ })
			if err == nil || emitted != 0 {
				t.Fatalf("error=%v emitted=%d", err, emitted)
			}
		})
	}
}

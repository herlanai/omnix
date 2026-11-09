// N01 DeepSeek client probe E2E tests parse prompt_cache_hit_tokens and prompt_cache_miss_tokens
// from mocked SSE matching the real response format.
package tests

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/credentials"
	"github.com/herlanai/omnix/pkg/llm"
	"github.com/herlanai/omnix/pkg/llm/provider_deepseek"
	"github.com/herlanai/omnix/pkg/storage"
)

// deepseekSSEWithUsage builds an SSE response containing usage and cache fields.
func deepseekSSEWithUsage() string {
	return "data: " + `{"choices":[{"delta":{"content":"hi"}}]}` + "\n\n" +
		"data: " + `{"choices":[],"usage":{"prompt_tokens":1000,"completion_tokens":50,"prompt_cache_hit_tokens":900,"prompt_cache_miss_tokens":100}}` + "\n\n" +
		"data: [DONE]\n\n"
}

// TestDeepSeekParseUsageFromMockSSE verifies cache-field parsing from SSE.
func TestDeepSeekParseUsageFromMockSSE(t *testing.T) {
	// Supply the API key through an in-memory credential store.
	store := credentials.NewStore(storage.NewMemoryKV())
	if err := store.Set(context.Background(), brand.NewCredentialRef("DEEPSEEK_API_KEY"), "sk-mock"); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(deepseekSSEWithUsage()))
	}))
	defer srv.Close()

	adapter := provider_deepseek.NewDeepSeek(store, provider_deepseek.WithBaseURL(srv.URL))

	text := ""
	usage, err := adapter.Chat(context.Background(), llm.ChatRequest{Model: "deepseek-chat"}, func(c llm.StreamChunk) {
		if c.Kind == llm.ChunkText {
			text += c.Text
		}
	})
	if err != nil {
		t.Fatalf("Chat 失败: %v", err)
	}
	if text != "hi" {
		t.Fatalf("应收到流式文本 hi, 实际 %q", text)
	}
	if usage.PromptCacheHitTokens != 900 || usage.PromptCacheMissTokens != 100 {
		t.Fatalf("缓存字段解析错误: %+v", usage)
	}
	if usage.PromptTokens != 1000 || usage.CompletionTokens != 50 {
		t.Fatalf("普通 token 解析错误: %+v", usage)
	}
	// Verify the cache-hit baseline.
	if r := llm.HitRatioOf(usage); r != 0.9 {
		t.Fatalf("命中率应 0.9, 实际 %v", r)
	}
}

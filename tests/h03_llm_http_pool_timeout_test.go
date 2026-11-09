// H03 DeepSeek HTTP tests verify default pool tuning, option overrides, cancellation during
// request headers and SSE reads, and LlmFailure cause chains preserving context.Canceled or
// DeadlineExceeded.
package tests

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/credentials"
	"github.com/herlanai/omnix/pkg/llm"
	"github.com/herlanai/omnix/pkg/llm/provider_deepseek"
)

// newCredsH03 creates an in-memory store containing the test API key sk-test-123.
func newCredsH03() *credentials.Store {
	s := credentials.NewMemoryStore()
	_ = s.Set(context.Background(), brand.NewCredentialRef("DEEPSEEK_API_KEY"), "sk-test-123")
	return s
}

// 1. Default transport connection-pool settings.

// TestH03DefaultPoolTuning compares NewDeepSeek transport defaults with the tuning constants.
func TestH03DefaultPoolTuning(t *testing.T) {
	d := provider_deepseek.NewDeepSeek(newCredsH03())
	tp := d.Transport()
	if tp == nil {
		t.Fatal("默认 Transport() 应为非 nil")
	}
	if tp.MaxIdleConns != provider_deepseek.DefaultMaxIdleConns {
		t.Fatalf("MaxIdleConns = %d, want %d", tp.MaxIdleConns, provider_deepseek.DefaultMaxIdleConns)
	}
	if tp.MaxIdleConnsPerHost != provider_deepseek.DefaultMaxIdleConnsPerHost {
		t.Fatalf("MaxIdleConnsPerHost = %d, want %d", tp.MaxIdleConnsPerHost, provider_deepseek.DefaultMaxIdleConnsPerHost)
	}
	if tp.IdleConnTimeout != provider_deepseek.DefaultIdleConnTimeout {
		t.Fatalf("IdleConnTimeout = %v, want %v", tp.IdleConnTimeout, provider_deepseek.DefaultIdleConnTimeout)
	}
	if tp.TLSHandshakeTimeout != provider_deepseek.DefaultTLSHandshakeTimeout {
		t.Fatalf("TLSHandshakeTimeout = %v, want %v", tp.TLSHandshakeTimeout, provider_deepseek.DefaultTLSHandshakeTimeout)
	}
	if !tp.ForceAttemptHTTP2 {
		t.Fatal("ForceAttemptHTTP2 应为 true（默认启用 H2）")
	}
	// Client.Timeout remains zero because the context controls deadlines.
	if d.HTTPClient().Timeout != 0 {
		t.Fatalf("http.Client.Timeout = %v, want 0（交由 H01 ctx 预算控制）", d.HTTPClient().Timeout)
	}
}

// 2. Override defaults through options.

// TestH03OptionsOverride verifies each H03 option overrides its default.
func TestH03OptionsOverride(t *testing.T) {
	d := provider_deepseek.NewDeepSeek(newCredsH03(),
		provider_deepseek.WithMaxIdleConnsPerHost(42),
		provider_deepseek.WithIdleConnTimeout(7*time.Second),
		provider_deepseek.WithTLSHandshakeTimeout(3*time.Second),
		provider_deepseek.DisableHTTP2(),
	)
	tp := d.Transport()
	if tp.MaxIdleConnsPerHost != 42 {
		t.Fatalf("WithMaxIdleConnsPerHost(42) 后 MaxIdleConnsPerHost=%d", tp.MaxIdleConnsPerHost)
	}
	if tp.IdleConnTimeout != 7*time.Second {
		t.Fatalf("WithIdleConnTimeout(7s) 后 IdleConnTimeout=%v", tp.IdleConnTimeout)
	}
	if tp.TLSHandshakeTimeout != 3*time.Second {
		t.Fatalf("WithTLSHandshakeTimeout(3s) 后 TLSHandshakeTimeout=%v", tp.TLSHandshakeTimeout)
	}
	if tp.ForceAttemptHTTP2 {
		t.Fatal("DisableHTTP2 后 ForceAttemptHTTP2 应为 false")
	}
	if tp.TLSNextProto == nil {
		t.Fatal("DisableHTTP2 后 TLSNextProto 应设置为非 nil 空 map（标准姿势）")
	}
}

// 3. Cancellation during client.Do returns promptly with the correct cause.

// TestH03CtxCancelDuringDo uses a blocked test server to verify prompt cancellation and
// errors.Is(err, context.Canceled). A separate release channel lets the handler exit before
// Server.Close, independently of server-side context propagation.
func TestH03CtxCancelDuringDo(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		// Wait for release; the test closes it after receiving the client error so Server.Close cannot
		// hang.
		<-release
	}))
	defer func() {
		// Defensively release the handler to prevent a goroutine leak.
		select {
		case <-release:
		default:
			close(release)
		}
		srv.Close()
	}()

	d := provider_deepseek.NewDeepSeek(newCredsH03(), provider_deepseek.WithBaseURL(srv.URL))

	// Cancel after 20 ms to allow the request to enter client.Do.
	parent, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	_, err := d.Chat(parent, llm.ChatRequest{Model: "deepseek-chat"}, nil)
	// Release the handler before closing the server.
	close(release)

	if err == nil {
		t.Fatal("ctx 取消后 Chat 应返回 error, got nil")
	}
	var f *llm.LlmFailure
	if !errors.As(err, &f) {
		t.Fatalf("Chat 返回错误类型应为 *LlmFailure, got %T: %v", err, err)
	}
	// The cause chain must contain context.Canceled.
	if !errors.Is(f, context.Canceled) {
		t.Fatalf("LlmFailure 错误链 errors.Is(f, context.Canceled) = false; err=%v cause=%v", f, f.Cause)
	}
}

// 4. Cancellation interrupts SSE body reads promptly.

// slowSSEWriter emits one line every 20 ms until cancellation. Reading all 10000 lines takes 200
// seconds; cancel after 80 ms and require Chat to return within 300 ms.
func TestH03CtxCancelDuringSSEBody(t *testing.T) {
	var sentCount atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for i := 0; i < 10000; i++ {
			select {
			case <-r.Context().Done():
				return
			default:
			}
			// Emit valid SSE lines so the scanner can process them; content and final usage are irrelevant
			// here.
			line := fmt.Sprintf("data: {\"choices\":[{\"delta\":{\"content\":\"%d\"}}]}\n\n", i)
			_, werr := fmt.Fprint(w, line)
			if werr != nil {
				return
			}
			flusher.Flush()
			sentCount.Add(1)
			time.Sleep(20 * time.Millisecond)
		}
	}))
	defer srv.Close()

	d := provider_deepseek.NewDeepSeek(newCredsH03(), provider_deepseek.WithBaseURL(srv.URL))

	start := time.Now()
	// An 80 ms timeout should interrupt after roughly three to five chunks rather than waiting for all
	// 10000.
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	var gotChunks int
	_, err := d.Chat(ctx, llm.ChatRequest{Model: "deepseek-chat"}, func(c llm.StreamChunk) {
		if c.Kind == llm.ChunkText {
			gotChunks++
		}
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("SSE 中 ctx 超时后 Chat 应返回 error, got nil")
	}
	var f *llm.LlmFailure
	if !errors.As(err, &f) {
		t.Fatalf("返回错误应为 *LlmFailure, got %T", err)
	}
	// The cause chain must contain DeadlineExceeded.
	if !errors.Is(f, context.DeadlineExceeded) {
		t.Fatalf("应能 errors.Is 到 DeadlineExceeded；err=%v cause=%v", f, f.Cause)
	}
	// Return promptly after timeout without waiting for the writer to finish.
	if elapsed > 500*time.Millisecond {
		t.Fatalf("ctx 超时返回耗时 = %v, 应 < 500ms（H03 的 SSE ctx 打断失败）", elapsed)
	}
	// Receiving a few chunks proves streaming began before cancellation.
	if gotChunks < 1 {
		t.Fatalf("读到的 chunk 数 = %d, 应 >= 1（否则 mock server 可能根本没传）", gotChunks)
	}
}

// 5. Normal SSE completion returns usage after [DONE].

// TestH03NormalSSEUsage verifies final-chunk usage survives the H03 streaming bridge.
func TestH03NormalSSEUsage(t *testing.T) {
	payload := strings.Join([]string{
		"data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}",
		"data: {\"choices\":[{\"delta\":{\"content\":\" World\"}}]}",
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2,\"prompt_cache_hit_tokens\":7,\"prompt_cache_miss_tokens\":3}}",
		"data: [DONE]",
		"",
	}, "\n\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()

	d := provider_deepseek.NewDeepSeek(newCredsH03(), provider_deepseek.WithBaseURL(srv.URL))

	var text strings.Builder
	usage, err := d.Chat(context.Background(), llm.ChatRequest{Model: "deepseek-chat"}, func(c llm.StreamChunk) {
		if c.Kind == llm.ChunkText {
			text.WriteString(c.Text)
		}
	})
	if err != nil {
		t.Fatalf("Chat 返回错误: %v", err)
	}
	if text.String() != "Hello World" {
		t.Fatalf("拼接内容 = %q, want \"Hello World\"", text.String())
	}
	if usage.PromptTokens != 10 || usage.CompletionTokens != 2 {
		t.Fatalf("usage = %+v, want prompt=10 completion=2", usage)
	}
	if usage.PromptCacheHitTokens != 7 || usage.PromptCacheMissTokens != 3 {
		t.Fatalf("cache usage hit=%d miss=%d, want hit=7 miss=3",
			usage.PromptCacheHitTokens, usage.PromptCacheMissTokens)
	}
}

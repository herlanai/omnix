// Package provider_deepseek implements the DeepSeek REST and SSE adapter,
// following upstream packages/llm/providers. It posts streaming requests to
// /chat/completions, maps content/reasoning_content/tool_calls to chunks,
// parses final usage including cache hit/miss tokens, and resolves credentials
// once per request. H03 adds connection reuse, context cancellation across
// dial/TLS/headers/body, a scanner-channel bridge for interruptible SSE reads,
// and functional transport options. Defaults retain 100 idle connections per
// host for 90 seconds, allow 10 seconds for TLS, and attempt HTTP/2.
package provider_deepseek

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/credentials"
	"github.com/herlanai/omnix/pkg/llm"
)

// H03 connection-pool defaults.

const (
	// DefaultMaxIdleConns limits idle connections across hosts.
	DefaultMaxIdleConns = 200
	// DefaultMaxIdleConnsPerHost limits idle connections per host, controlling
	// connection reuse for concurrent requests to the single DeepSeek host.
	DefaultMaxIdleConnsPerHost = 100
	// DefaultIdleConnTimeout closes idle connections before stale intermediary
	// connections can cause an EOF on reuse.
	DefaultIdleConnTimeout = 90 * time.Second
	// DefaultTLSHandshakeTimeout bounds TLS handshakes so callers do not block indefinitely.
	DefaultTLSHandshakeTimeout = 10 * time.Second
	// DefaultExpectContinueTimeout bounds the wait for a 100-continue response.
	DefaultExpectContinueTimeout = 5 * time.Second
)

// DefaultBaseURL is the default DeepSeek API endpoint.
const DefaultBaseURL = "https://api.deepseek.com"

// DeepSeek implements the DeepSeek adapter.
type DeepSeek struct {
	baseURL   string
	client    *http.Client
	transport *http.Transport // Keep the transport available for tuning assertions in tests.
	creds     *credentials.Store
	apiKeyRef brand.CredentialRef
}

// NewDeepSeek creates an adapter with pooled connections. Credentials are
// resolved through creds using apiKeyRef, defaulting to DEEPSEEK_API_KEY.
// The HTTP client has no overall timeout because valid SSE responses may last
// minutes. Callers should pass a context budget, for example with SetRunContext,
// to bound the complete request and release resources on cancellation.
func NewDeepSeek(creds *credentials.Store, opts ...Option) *DeepSeek {
	// Build the default pooled transport (H03).
	tp := newDefaultTransport()
	d := &DeepSeek{
		baseURL:   DefaultBaseURL,
		transport: tp,
		client: &http.Client{
			// Leave total timeout to the context across dial, TLS, headers, and body reads.
			Timeout:   0,
			Transport: tp,
		},
		creds:     creds,
		apiKeyRef: brand.NewCredentialRef("DEEPSEEK_API_KEY"),
	}
	for _, o := range opts {
		o(d)
	}
	return d
}

// newDefaultTransport builds the tuned H03 HTTP transport.
func newDefaultTransport() *http.Transport {
	// Clone DefaultTransport to retain defaults such as ProxyFromEnvironment.
	def, _ := http.DefaultTransport.(*http.Transport)
	tp := def.Clone()
	// Configure connection pooling.
	tp.MaxIdleConns = DefaultMaxIdleConns
	tp.MaxIdleConnsPerHost = DefaultMaxIdleConnsPerHost
	tp.MaxConnsPerHost = 0 // Leave total concurrency to higher-level token and concurrency budgets.
	tp.IdleConnTimeout = DefaultIdleConnTimeout
	// Bound TCP connection establishment.
	if tp.DialContext == nil {
		dialer := &net.Dialer{
			Timeout:   10 * time.Second, // TCP connection timeout.
			KeepAlive: 30 * time.Second,
		}
		tp.DialContext = dialer.DialContext
	}
	// TLS handshake timeout.
	tp.TLSHandshakeTimeout = DefaultTLSHandshakeTimeout
	// 100-continue timeout.
	tp.ExpectContinueTimeout = DefaultExpectContinueTimeout
	// Attempt HTTP/2 when supported by the backend.
	tp.ForceAttemptHTTP2 = true
	// A nil TLSClientConfig uses system CAs and verification. Callers can supply
	// a custom configuration through WithTransport.
	return tp
}

// Functional options, including H03 connection-pool and transport tuning.

// Option configures a DeepSeek adapter.
type Option func(*DeepSeek)

// WithBaseURL overrides the endpoint, for example with a test server.
func WithBaseURL(u string) Option {
	return func(d *DeepSeek) { d.baseURL = u }
}

// WithHTTPClient installs a custom client for testing or rate limiting.
// Its transport replaces all default H03 connection-pool settings.
func WithHTTPClient(c *http.Client) Option {
	return func(d *DeepSeek) {
		d.client = c
		if tp, ok := c.Transport.(*http.Transport); ok {
			d.transport = tp
		} else {
			d.transport = nil
		}
	}
}

// WithTransport replaces the transport with a caller-managed connection pool.
func WithTransport(tp *http.Transport) Option {
	return func(d *DeepSeek) {
		d.transport = tp
		d.client.Transport = tp
	}
}

// WithAPIKeyRef overrides the API-key credential reference.
func WithAPIKeyRef(ref brand.CredentialRef) Option {
	return func(d *DeepSeek) { d.apiKeyRef = ref }
}

// WithMaxIdleConnsPerHost tunes the default transport's per-host idle limit.
// It applies only without a custom client or transport.
func WithMaxIdleConnsPerHost(n int) Option {
	return func(d *DeepSeek) {
		if d.transport != nil {
			d.transport.MaxIdleConnsPerHost = n
		}
	}
}

// WithIdleConnTimeout sets the idle connection timeout.
func WithIdleConnTimeout(d time.Duration) Option {
	return func(tp *DeepSeek) {
		if tp.transport != nil {
			tp.transport.IdleConnTimeout = d
		}
	}
}

// WithTLSHandshakeTimeout sets the TLS handshake timeout.
func WithTLSHandshakeTimeout(t time.Duration) Option {
	return func(d *DeepSeek) {
		if d.transport != nil {
			d.transport.TLSHandshakeTimeout = t
		}
	}
}

// DisableHTTP2 disables HTTP/2 attempts for callers requiring HTTP/1.1.
func DisableHTTP2() Option {
	return func(d *DeepSeek) {
		if d.transport != nil {
			d.transport.ForceAttemptHTTP2 = false
			// A non-nil empty TLSNextProto map disables HTTP/2 negotiation explicitly,
			// without relying on the process-wide GODEBUG environment variable.
			d.transport.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
		}
	}
}

// Transport accessors for tests and operational diagnostics.

// Transport returns the active http.Transport, or nil for another transport
// type. Tests can inspect its tuning values.
func (d *DeepSeek) Transport() *http.Transport { return d.transport }

// HTTPClient returns the active client for inspection or testing.
func (d *DeepSeek) HTTPClient() *http.Client { return d.client }

// Name implements llm.LLMAdapter.
func (d *DeepSeek) Name() string {
	return "deepseek"
}

// Request and response structures include only fields used by this adapter.

type chatRequest struct {
	ReasoningEffort string         `json:"reasoning_effort,omitempty"`
	Model           string         `json:"model"`
	Messages        []chatMessage  `json:"messages"`
	Tools           []chatTool     `json:"tools,omitempty"`
	Stream          bool           `json:"stream"`
	Temperature     float64        `json:"temperature,omitempty"`
	MaxTokens       int            `json:"max_tokens,omitempty"`
	StreamOptions   *streamOptions `json:"stream_options,omitempty"`
}

// streamOptions uses the OpenAI-compatible object form {"include_usage": true}.
// A boolean value is invalid and causes a DeepSeek 400 response.
type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatMessage struct {
	Role             string         `json:"role"`
	Content          string         `json:"content"`
	ToolCalls        []chatToolCall `json:"tool_calls,omitempty"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	ToolCallID       string         `json:"tool_call_id,omitempty"`
}

type chatTool struct {
	Type     string       `json:"type"`
	Function chatFunction `json:"function"`
}

type chatFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type chatToolCall struct {
	Index    int                  `json:"index,omitempty"`
	ID       string               `json:"id"`
	Type     string               `json:"type"`
	Function chatToolCallFunction `json:"function"`
}

type chatToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// sseChunk represents one SSE data payload.
type sseChunk struct {
	Choices []struct {
		Delta struct {
			Content   string         `json:"content"`
			Reasoning string         `json:"reasoning_content"`
			ToolCalls []chatToolCall `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens          int `json:"prompt_tokens"`
		CompletionTokens      int `json:"completion_tokens"`
		PromptCacheHitTokens  int `json:"prompt_cache_hit_tokens"`
		PromptCacheMissTokens int `json:"prompt_cache_miss_tokens"`
	} `json:"usage"`
}

// Chat carries context cancellation through dial, TLS, headers, and body reads.

// Chat implements llm.LLMAdapter by parsing an SSE response into chunks.
// Callers supply cancellation and timeout budgets through ctx. The request
// context cancels connection establishment and header reads; readSSE uses a
// scanner-channel bridge so the caller can return when canceled even while
// the scanner is blocked in Read.
func (d *DeepSeek) Chat(ctx context.Context, req llm.ChatRequest, cb func(llm.StreamChunk)) (llm.Usage, error) {
	// Resolve the API key once per request (M39).
	apiKey, ok := d.creds.Resolve(ctx, d.apiKeyRef)
	if !ok || apiKey == "" {
		return llm.Usage{}, &llm.LlmFailure{Kind: llm.FailResponseRefusal, Message: "missing API key credential"}
	}

	payload := d.buildPayload(req)
	body, err := json.Marshal(payload)
	if err != nil {
		return llm.Usage{}, fmt.Errorf("deepseek: marshal request: %w", err)
	}

	// Bind ctx to the request for cancellation from dial through headers.
	// The bytes.Reader body is replayable; retries are handled by llm/retry.go
	// rather than inside the provider.
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, d.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return llm.Usage{}, fmt.Errorf("deepseek: new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := d.client.Do(httpReq)
	if err != nil {
		// Map failures to stable categories, preserving cancellation and deadline causes.
		if ctx.Err() != nil {
			return llm.Usage{}, &llm.LlmFailure{Kind: llm.FailOverload, Message: ctx.Err().Error(), Cause: ctx.Err()}
		}
		return llm.Usage{}, &llm.LlmFailure{Kind: llm.FailOverload, Message: err.Error(), Cause: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Classify provider details from the body first (R04), falling back to HTTP
		// status when the body cannot be read or recognized.
		body, rerr := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		if rerr == nil && len(body) > 0 {
			return llm.Usage{}, d.mapHTTPErrorBody(resp.StatusCode, string(body))
		}
		return llm.Usage{}, d.mapHTTPError(resp.StatusCode)
	}

	// Pass ctx through SSE reading.
	return d.readSSE(ctx, resp.Body, cb)
}

// buildPayload converts ChatRequest to a DeepSeek API payload.
func (d *DeepSeek) buildPayload(req llm.ChatRequest) chatRequest {
	messages := make([]chatMessage, 0, len(req.Messages)+1)
	if req.System != "" {
		messages = append(messages, chatMessage{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		cm := chatMessage{Role: string(m.Role)}
		flush := func() {
			if cm.Content != "" || cm.ReasoningContent != "" || len(cm.ToolCalls) > 0 {
				messages = append(messages, cm)
				cm = chatMessage{Role: string(m.Role)}
			}
		}
		for _, blk := range m.Content {
			switch blk.Kind {
			case llm.BlockText:
				cm.Content += blk.Text
			case llm.BlockReasoning:
				cm.ReasoningContent += blk.Text
			case llm.BlockToolUse:
				if blk.ToolCall != nil {
					args, _ := json.Marshal(blk.ToolCall.Input)
					cm.ToolCalls = append(cm.ToolCalls, chatToolCall{ID: blk.ToolCall.ID, Type: "function", Function: chatToolCallFunction{Name: blk.ToolCall.Name, Arguments: string(args)}})
				}
			case llm.BlockToolResult:
				if blk.ToolResult != nil {
					flush()
					messages = append(messages, chatMessage{Role: "tool", ToolCallID: blk.ToolResult.ToolUseID, Content: blk.ToolResult.Content})
				}
			}
		}
		flush()
	}

	tools := make([]chatTool, 0, len(req.Tools))
	for _, t := range req.Tools {
		tools = append(tools, chatTool{
			Type: "function",
			Function: chatFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}

	return chatRequest{
		Model:           req.Model,
		Messages:        messages,
		Tools:           tools,
		Stream:          true,
		Temperature:     req.Temperature,
		MaxTokens:       req.MaxTokens,
		ReasoningEffort: req.ReasoningEffort,
		StreamOptions:   &streamOptions{IncludeUsage: true},
	}
}

// SSE reading supports cancellation of blocking I/O (H03).

// sseScanResult carries a line or a terminal scanner result to the consumer.
// A terminal result sets done and includes the scanner error, if any.
type sseScanResult struct {
	line string
	err  error
	done bool // True when the scanner goroutine has exited, normally or with an error.
}

// readSSE reads lines, emits chunks, and returns usage. Cancellation lets the
// caller return promptly even when the scanner is blocked in a network read.
func (d *DeepSeek) readSSE(ctx context.Context, r io.Reader, cb func(llm.StreamChunk)) (llm.Usage, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	// Run the blocking scanner separately and deliver lines and completion through
	// the channel. A buffer of 16 lets scanning run slightly ahead of consumption.
	ch := make(chan sseScanResult, 16)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		defer close(ch)
		send := func(result sseScanResult) bool {
			select {
			case ch <- result:
				return true
			case <-stop:
				return false
			case <-ctx.Done():
				return false
			}
		}
		for scanner.Scan() {
			if !send(sseScanResult{line: scanner.Text()}) {
				return
			}
			if strings.TrimSpace(scanner.Text()) == "data: [DONE]" {
				return
			}
		}
		send(sseScanResult{err: scanner.Err(), done: true})
	}()
	pending := map[int]*chatToolCall{}
	finish := func() error {
		indexes := make([]int, 0, len(pending))
		for index := range pending {
			indexes = append(indexes, index)
		}
		sort.Ints(indexes)
		calls := make([]*llm.ToolCall, 0, len(indexes))
		for _, index := range indexes {
			tc := pending[index]
			if tc.ID == "" || tc.Function.Name == "" {
				return fmt.Errorf("deepseek: incomplete tool call at index %d", index)
			}
			input := map[string]any{}
			if tc.Function.Arguments != "" {
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &input); err != nil || input == nil {
					return fmt.Errorf("deepseek: invalid arguments for tool %q", tc.Function.Name)
				}
			}
			calls = append(calls, &llm.ToolCall{ID: tc.ID, Name: tc.Function.Name, Input: input})
		}
		if cb != nil {
			for _, call := range calls {
				cb(llm.StreamChunk{Kind: llm.ChunkToolCall, ToolCall: call})
			}
			cb(llm.StreamChunk{Kind: llm.ChunkDone})
		}
		return nil
	}

	var usage llm.Usage
	for {
		select {
		case <-ctx.Done():
			// Return the classified cancellation error promptly. Chat's deferred Body.Close
			// interrupts the scanner's pending Read so its goroutine can exit.
			return usage, &llm.LlmFailure{
				Kind:    llm.FailOverload,
				Message: "deepseek: sse read interrupted: " + ctx.Err().Error(),
				Cause:   ctx.Err(),
			}
		case res, ok := <-ch:
			if !ok {
				// The scanner may close without a done result after [DONE] or cancellation.
				return usage, finish()
			}
			if res.done {
				if res.err != nil {
					return usage, &llm.LlmFailure{Kind: llm.FailOverload, Message: res.err.Error(), Cause: res.err}
				}
				return usage, finish()
			}
			// Process one SSE line.
			line := strings.TrimSpace(res.line)
			if line == "" {
				continue
			}
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				return usage, finish()
			}

			var chunk sseChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				return usage, fmt.Errorf("deepseek: malformed SSE data: %w", err)
			}
			// Parse usage.
			if chunk.Usage != nil {
				usage = llm.Usage{
					PromptTokens:          chunk.Usage.PromptTokens,
					CompletionTokens:      chunk.Usage.CompletionTokens,
					PromptCacheHitTokens:  chunk.Usage.PromptCacheHitTokens,
					PromptCacheMissTokens: chunk.Usage.PromptCacheMissTokens,
				}
			}
			if len(chunk.Choices) == 0 {
				continue
			}
			delta := chunk.Choices[0].Delta
			for _, tc := range delta.ToolCalls {
				acc := pending[tc.Index]
				if acc == nil {
					acc = &chatToolCall{}
					pending[tc.Index] = acc
				}
				if tc.ID != "" {
					acc.ID = tc.ID
				}
				acc.Function.Name += tc.Function.Name
				acc.Function.Arguments += tc.Function.Arguments
			}

			if cb != nil {
				if delta.Reasoning != "" {
					cb(llm.StreamChunk{Kind: llm.ChunkReasoning, Reasoning: delta.Reasoning})
				}
				if delta.Content != "" {
					cb(llm.StreamChunk{Kind: llm.ChunkText, Text: delta.Content})
				}

			}
		}
	}
}

// Error mapping.

// mapHTTPError maps status codes to stable LlmFailure categories.
// 401/403 map to invalid-credential (R04); other cases include rate-limit
// and overload. Provider body details allow more precise classification.
func (d *DeepSeek) mapHTTPError(status int) *llm.LlmFailure {
	switch {
	case status == 429:
		return &llm.LlmFailure{Kind: llm.FailRateLimit, Message: fmt.Sprintf("http %d rate limited", status)}
	case status == 401:
		return &llm.LlmFailure{Kind: llm.FailInvalidCredential, Message: fmt.Sprintf("http %d invalid credential / unauthorized", status)}
	case status >= 500:
		return &llm.LlmFailure{Kind: llm.FailOverload, Message: fmt.Sprintf("http %d server overload", status)}
	case status == 400 || status == 422:
		return &llm.LlmFailure{Kind: llm.FailContextOverflow, Message: fmt.Sprintf("http %d bad request (possible context overflow)", status)}
	default:
		return &llm.LlmFailure{Kind: llm.FailResponseRefusal, Message: fmt.Sprintf("http %d", status)}
	}
}

// mapHTTPErrorBody first classifies provider details, then falls back to status
// when unrecognized. Message retains the body, truncated to about 200
// characters, for diagnosis and to avoid misleading status-only guesses
// such as treating every 400 response as context overflow.
func (d *DeepSeek) mapHTTPErrorBody(status int, body string) *llm.LlmFailure {
	const maxBody = 200
	snippet := body
	if len(snippet) > maxBody {
		snippet = snippet[:maxBody] + "..."
	}
	if f := llm.NewProviderFailure(body); f.Kind != "unknown" && f.Kind != "" {
		// Classify API details and preserve the body for troubleshooting.
		f.Message = fmt.Sprintf("http %d: %s", status, snippet)
		return f
	}
	// Fall back to status classification while retaining the original body.
	f := d.mapHTTPError(status)
	f.Message = fmt.Sprintf("%s (body: %s)", f.Message, snippet)
	return f
}

// Retain an explicit errors reference for future errors.Is usage.
var _ = errors.New

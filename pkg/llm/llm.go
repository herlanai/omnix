// Package llm defines provider integration and streaming protocol types,
// following upstream packages/llm/llm and providers. LLMAdapter supports
// streaming Chat with usage reporting. Messages contain text, tool-use,
// tool-result, or image blocks; streams carry text, reasoning, tool calls,
// and completion. LlmFailure provides stable provider-error categories for
// S15 retries and the M34 request-error waterfall, matching upstream error.ts.
package llm

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// Messages and content blocks.

// Role identifies a message role.
type Role string

// Supported message roles.
const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// ContentBlockKind identifies a content block type.
type ContentBlockKind string

// Supported content block kinds.
const (
	BlockText       ContentBlockKind = "text"
	BlockReasoning  ContentBlockKind = "reasoning"
	BlockToolUse    ContentBlockKind = "tool_use"
	BlockToolResult ContentBlockKind = "tool_result"
	BlockImage      ContentBlockKind = "image"
)

// ToolCall describes a model-requested tool invocation.
type ToolCall struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

// ToolResultContent contains a tool result block.
type ToolResultContent struct {
	ToolUseID string `json:"toolUseId"`
	Content   string `json:"content"`
	IsError   bool   `json:"isError,omitempty"`
}

// ImageContent refers to an image URL or attachment.
type ImageContent struct {
	URL        string `json:"url,omitempty"`
	Attachment string `json:"attachment,omitempty"`
}

// ContentBlock is a tagged content-block union within a message.
type ContentBlock struct {
	Kind       ContentBlockKind   `json:"kind"`
	Text       string             `json:"text,omitempty"`
	ToolCall   *ToolCall          `json:"toolCall,omitempty"`
	ToolResult *ToolResultContent `json:"toolResult,omitempty"`
	Image      *ImageContent      `json:"image,omitempty"`
}

// Text constructs a text block.
func Text(s string) ContentBlock {
	return ContentBlock{Kind: BlockText, Text: s}
}

// Reasoning constructs a reasoning block.
func Reasoning(s string) ContentBlock {
	return ContentBlock{Kind: BlockReasoning, Text: s}
}

// ToolUse constructs a tool invocation block.
func ToolUse(tc *ToolCall) ContentBlock {
	return ContentBlock{Kind: BlockToolUse, ToolCall: tc}
}

// ToolResult constructs a tool result block.
func ToolResult(tr *ToolResultContent) ContentBlock {
	return ContentBlock{Kind: BlockToolResult, ToolResult: tr}
}

// Image constructs an image block.
func Image(img *ImageContent) ContentBlock {
	return ContentBlock{Kind: BlockImage, Image: img}
}

// Message combines a role and a list of content blocks.
type Message struct {
	Role    Role           `json:"role"`
	Content []ContentBlock `json:"content"`
}

// NewUserMessage constructs a user text message.
func NewUserMessage(text string) Message {
	return Message{Role: RoleUser, Content: []ContentBlock{Text(text)}}
}

// NewAssistantText constructs an assistant text message.
func NewAssistantText(text string) Message {
	return Message{Role: RoleAssistant, Content: []ContentBlock{Text(text)}}
}

// Tool schemas.

// ToolSchema describes a model-visible tool using OpenAI function conventions.
type ToolSchema struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// Streaming chunks and failure classification.

// StreamChunkKind identifies a streaming chunk type.
type StreamChunkKind string

// Supported streaming chunk kinds.
const (
	ChunkText      StreamChunkKind = "text"
	ChunkReasoning StreamChunkKind = "reasoning"
	ChunkToolCall  StreamChunkKind = "tool-call"
	ChunkDone      StreamChunkKind = "done"
)

// StreamChunk is one streaming callback payload.
type StreamChunk struct {
	Kind      StreamChunkKind `json:"kind"`
	Text      string          `json:"text,omitempty"`
	Reasoning string          `json:"reasoning,omitempty"`
	ToolCall  *ToolCall       `json:"toolCall,omitempty"`
}

// Usage records request token usage, including cache statistics for instrumentation.
type Usage struct {
	PromptTokens          int `json:"promptTokens"`
	CompletionTokens      int `json:"completionTokens"`
	PromptCacheHitTokens  int `json:"promptCacheHitTokens,omitempty"`
	PromptCacheMissTokens int `json:"promptCacheMissTokens,omitempty"`
}

// Classified LLM failures.

// LlmFailureKind provides stable model-failure categories.
type LlmFailureKind string

// Failure categories correspond to stable upstream HarnessError codes in
// packages/llm/llm/src/error.ts, including CONTEXT_WINDOW_EXCEEDED, QUOTA,
// EMPTY_RESPONSE, INVALID_CREDENTIAL, RATE_LIMIT, and overload.
// Always route by Kind; never parse human-readable error messages.
const (
	// FailOverload indicates service overload; retrying is recommended.
	FailOverload LlmFailureKind = "overload"
	// FailRateLimit indicates rate limiting; retry with backoff.
	FailRateLimit LlmFailureKind = "rate-limit"
	// FailResponseRefusal indicates a model refusal and is not retryable.
	FailResponseRefusal LlmFailureKind = "response-refusal"
	// FailContextOverflow indicates the context requires compaction.
	FailContextOverflow LlmFailureKind = "context-overflow"
	// FailQuota indicates exhausted quota or balance (QUOTA); default retries exclude it.
	FailQuota LlmFailureKind = "quota"
	// FailEmptyResponse indicates normal completion without content (EMPTY_RESPONSE).
	// It yields no assistant message and can be retried safely.
	FailEmptyResponse LlmFailureKind = "empty-response"
	// FailInvalidCredential indicates malformed credentials (INVALID_CREDENTIAL),
	// not missing credentials: replace the stored value rather than add one.
	// Default retries exclude this category.
	FailInvalidCredential LlmFailureKind = "invalid-credential"
)

// Retryable matches the upstream default set: overload, rate-limit,
// context-overflow after compaction, and empty-response. Refusal, quota,
// invalid credentials, and unknown categories are not retryable.
func (f LlmFailureKind) Retryable() bool {
	switch f {
	case FailOverload, FailRateLimit, FailContextOverflow, FailEmptyResponse:
		return true
	default:
		return false
	}
}

// LlmFailure represents a model request failure with a stable category.
type LlmFailure struct {
	Kind    LlmFailureKind `json:"kind"`
	Message string         `json:"message"`
	// Cause retains the underlying error, such as context.DeadlineExceeded or
	// net.OpError, for errors.Is/As diagnostics. JSON omits it to avoid cycles and
	// sensitive details. Providers preserve cancellation/network causes so
	// errors.Is(f, context.Canceled) distinguishes cancellation from network faults.
	Cause error `json:"-"`
}

// Error implements the error interface.
func (e *LlmFailure) Error() string {
	return fmt.Sprintf("llm failure [%s]: %s", e.Kind, e.Message)
}

// Unwrap exposes Cause to errors.Is and errors.As.
func (e *LlmFailure) Unwrap() error { return e.Cause }

// ClassifyLlmError classifies arbitrary errors, using unknown when no category matches.
func ClassifyLlmError(err error) *LlmFailure {
	if err == nil {
		return nil
	}
	if f, ok := err.(*LlmFailure); ok {
		return f
	}
	// Fall back to unknown while preserving the original message.
	return &LlmFailure{Kind: "unknown", Message: err.Error()}
}

// Provider detail classification follows upstream packages/llm/llm/src/error.ts.

// Provider detail combines code, type, message, and body, then lowercases the
// result for pattern matching. As upstream does for context and quota errors,
// classifiers inspect text while downstream routing uses stable Kind values.
var (
	// Context overflow patterns match upstream CONTEXT_WINDOW_EXCEEDED, including
	// STRUCTURED_CONTEXT_OVERFLOW, maximum context length/window,
	// TOO_LARGE_FOR_CONTEXT, and too long/large for this/the model.
	reContextWindow = regexp.MustCompile(
		`(?:^|[^a-z0-9])context[\s_-](?:length|window)[\s_-](?:exceed(?:ed|s)?|overflow(?:ed)?|limit[\s_-]exceeded)(?:$|[^a-z0-9])` +
			`|(?:maximum|max)[\s_-]*(?:allowed|supported)?[\s_-]*context[\s_-]+(?:length|window)` +
			`|(?:^|[^a-z0-9])(?:prompt|input|request|messages?)[\s_-](?:too[\s_-](?:long|large)|exceed(?:ed)?)[\s_-]for[\s_-](?:the[\s_-])?(?:model)?(?:[^a-z0-9]|$)` +
			`|(?:^|[^a-z0-9])(?:input|prompt|request)[\s_-]+(?:is[\s_-]+)?too[\s_-](?:long|large)[\s_-]+for[\s_-]+(?:this|the)[\s_-]+model(?:[^a-z0-9]|$)`,
	)
	// Quota, balance, or credit exhaustion (upstream QUOTA).
	reQuota = regexp.MustCompile(
		`insufficient[\s_-]+(?:quota|balance|credits?)` +
			`|(?:quota|usage[\s_-]+limit)[\s_-]+(?:exceeded|exhausted|reached)` +
			`|(?:out[\s_-]+of|not[\s_-]+enough)[\s_-]+(?:credits?|budget|quota|balance)` +
			`|don.t[\s_-]+have[\s_-]+enough[\s_-]*(?:api[\s_-]+)?credits?` +
			`|(?:balance|credits?)[\s_-]+(?:exhausted|depleted)`,
	)
	// Invalid or mismatched credentials (INVALID_CREDENTIAL), distinct from missing credentials.
	reInvalidCred = regexp.MustCompile(
		`(?:invalid|wrong|incorrect|bad|unauthorized|auth)[\s_-]+(?:api[\s_-]?key|credential|token|secret)` +
			`|(?:api[\s_-]?key|credential)[\s_-]+(?:invalid|revoked|mismatch)` +
			`|401[\s_-]*(?:invalid|authentication)`,
	)
	// Rate limiting (upstream RATE_LIMIT).
	reRateLimit = regexp.MustCompile(`rate[\s_-]*limit|too[\s_-]+many[\s_-]+requests|\b429\b`)
	// Normal completion with no content (upstream EMPTY_RESPONSE).
	reEmptyResponse = regexp.MustCompile(`empty[\s_-]+response|no[\s_-]+content|empty[\s_-]+completion|zero[\s_-]+output`)
	// Model response refusal.
	reRefusal = regexp.MustCompile(`(?:refus|declin)[e\w]*|cannot[\s_-]+(?:answer|comply)|policy[\s_-]+(?:refus|declin)`)
)

// ClassifyProviderDetail normalizes combined provider code/type/message details
// and maps them to a stable LlmFailureKind. Unrecognized details return empty.
// Patterns follow upstream context-window and quota classifiers.
func ClassifyProviderDetail(detail string) LlmFailureKind {
	d := strings.ToLower(strings.TrimSpace(detail))
	if d == "" {
		return ""
	}
	switch {
	case reContextWindow.MatchString(d):
		return FailContextOverflow
	case reQuota.MatchString(d):
		return FailQuota
	case reInvalidCred.MatchString(d):
		return FailInvalidCredential
	case reRateLimit.MatchString(d):
		return FailRateLimit
	case reEmptyResponse.MatchString(d):
		return FailEmptyResponse
	case reRefusal.MatchString(d):
		return FailResponseRefusal
	default:
		return ""
	}
}

// NewProviderFailure classifies provider detail, falling back to unknown,
// and preserves the original detail in Message for diagnostics.
func NewProviderFailure(detail string) *LlmFailure {
	kind := ClassifyProviderDetail(detail)
	if kind == "" {
		kind = "unknown"
	}
	return &LlmFailure{Kind: kind, Message: detail}
}

// Request and adapter interfaces.

// ChatRequest describes one model conversation request.
type ChatRequest struct {
	// ReasoningEffort is an explicit, provider-owned reasoning setting.
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
	// Model names the requested model, such as deepseek-chat.
	Model string `json:"model"`
	// Messages contains the conversation.
	Messages []Message `json:"messages"`
	// System holds the system prompt separately for provider assembly.
	System string `json:"system,omitempty"`
	// Tools contains available tool definitions.
	Tools []ToolSchema `json:"tools,omitempty"`
	// Temperature controls sampling.
	Temperature float64 `json:"temperature,omitempty"`
	// MaxTokens limits generated output.
	MaxTokens int `json:"maxTokens,omitempty"`
}

// LLMAdapter is the common model invocation interface.
type LLMAdapter interface {
	// Name returns the adapter name, such as deepseek.
	Name() string
	// Chat streams chunks through cb and returns final usage.
	Chat(ctx context.Context, req ChatRequest, cb func(StreamChunk)) (Usage, error)
}

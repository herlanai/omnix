// R04 stable LLM failure tests align with upstream packages/llm/llm/src/error.ts: quota,
// empty-response, and invalid-credential classifications; the default retryable set; recognition
// of context, quota, credentials, rate limits, empty responses, and refusals; and unknown fallback
// for unrecognized details.
package tests

import (
	"testing"

	"github.com/herlanai/omnix/pkg/llm"
)

// 1. Failure-kind coverage.

func TestR04KindEnumComplete(t *testing.T) {
	want := []struct {
		kind llm.LlmFailureKind
		name string
	}{
		{llm.FailOverload, "overload"},
		{llm.FailRateLimit, "rate-limit"},
		{llm.FailResponseRefusal, "response-refusal"},
		{llm.FailContextOverflow, "context-overflow"},
		{llm.FailQuota, "quota"},
		{llm.FailEmptyResponse, "empty-response"},
		{llm.FailInvalidCredential, "invalid-credential"},
	}
	for _, w := range want {
		if string(w.kind) != w.name {
			t.Fatalf("Kind 常量值异常: %q != %q", w.kind, w.name)
		}
	}
}

// 2. Retryable behavior matches the upstream default set.

func TestR04RetryableSemantics(t *testing.T) {
	cases := []struct {
		kind llm.LlmFailureKind
		want bool
	}{
		{llm.FailOverload, true},
		{llm.FailRateLimit, true},
		{llm.FailContextOverflow, true},
		{llm.FailEmptyResponse, true},
		{llm.FailResponseRefusal, false},
		{llm.FailQuota, false},
		{llm.FailInvalidCredential, false},
		{llm.LlmFailureKind("unknown"), false},
	}
	for _, c := range cases {
		if got := c.kind.Retryable(); got != c.want {
			t.Fatalf("Retryable(%q) = %v, want %v", c.kind, got, c.want)
		}
	}
}

// 3. Provider error-text classification.

func TestR04ClassifyProviderDetail(t *testing.T) {
	cases := []struct {
		detail string
		want   llm.LlmFailureKind
	}{
		// Context-window overflow (CONTEXT_WINDOW_EXCEEDED).
		{"This model's maximum context length is 128K tokens. However, your messages resulted in 200K tokens", llm.FailContextOverflow},
		{"context_length_exceeded", llm.FailContextOverflow},
		{"prompt is too long for this model's context window", llm.FailContextOverflow},
		// Exhausted quota (QUOTA).
		{"Insufficient_quota", llm.FailQuota},
		{"quota exceeded for the current billing cycle", llm.FailQuota},
		{"You don't have enough API credits", llm.FailQuota},
		{"out of budget", llm.FailQuota},
		// Invalid credentials (INVALID_CREDENTIAL).
		{"invalid API key", llm.FailInvalidCredential},
		{"api_key_mismatch", llm.FailInvalidCredential},
		{"incorrect credential provided", llm.FailInvalidCredential},
		// Rate limiting (RATE_LIMIT).
		{"Rate limit reached", llm.FailRateLimit},
		{"too many requests", llm.FailRateLimit},
		{"HTTP 429 Too Many Requests", llm.FailRateLimit},
		// Empty response (EMPTY_RESPONSE).
		{"empty response from model, no content blocks", llm.FailEmptyResponse},
		{"finish_reason stop with no content", llm.FailEmptyResponse},
		// Response refusal.
		{"The model refused to answer the request due to safety policy", llm.FailResponseRefusal},
		// Unrecognized details map to unknown.
		{"Some unrelated network error: connection reset", llm.LlmFailureKind("unknown")},
	}
	for _, c := range cases {
		got := llm.ClassifyProviderDetail(c.detail)
		// Normalize the classifier's empty-string result to unknown for comparison.
		gotStr := string(got)
		if gotStr == "" {
			gotStr = "unknown"
		}
		if gotStr != string(c.want) {
			t.Fatalf("ClassifyProviderDetail(%q) = %q, want %q", c.detail, gotStr, c.want)
		}
	}
}

// TestR04EmptyDetail handles empty text without panic and returns unknown.
func TestR04EmptyDetail(t *testing.T) {
	if got := llm.NewProviderFailure(""); got.Kind != "unknown" {
		t.Fatalf("空 detail 应归 unknown, got %q", got.Kind)
	}
}

// TestR04NewProviderFailure preserves Message while assigning a stable kind or unknown.
func TestR04NewProviderFailure(t *testing.T) {
	f := llm.NewProviderFailure("quota exhausted for the workspace")
	if f.Kind != llm.FailQuota {
		t.Fatalf("NewProviderFailure quota = %q", f.Kind)
	}
	if f.Message != "quota exhausted for the workspace" {
		t.Fatalf("Message 应保留原文, got %q", f.Message)
	}
	u := llm.NewProviderFailure("gibberish unknown backend hiccup")
	if u.Kind != "unknown" {
		t.Fatalf("未识别应归 unknown, got %q", u.Kind)
	}
}

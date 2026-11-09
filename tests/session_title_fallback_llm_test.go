// S08 session-title tests cover rune-safe 30-character fallback prefixes, fallback when LLM is
// disabled/fails/returns empty text, adoption of valid generated titles, and latest-wins
// session/title folding.
package tests

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/llm"
	"github.com/herlanai/omnix/pkg/session"
	"github.com/herlanai/omnix/pkg/sessiontitle"
)

// ============================================================================
// Mock LLMAdapter
// ============================================================================

// mockLLM is a configurable LLMAdapter test double.
type mockLLM struct {
	// Replay text on each Chat call; an empty value returns err immediately to simulate failure.
	text string
	err  error
}

func (m *mockLLM) Name() string { return "mock-title" }

func (m *mockLLM) Chat(ctx context.Context, req llm.ChatRequest, cb func(llm.StreamChunk)) (llm.Usage, error) {
	if m.err != nil {
		return llm.Usage{}, m.err
	}
	// Stream one character at a time to simulate text chunks.
	for _, r := range m.text {
		cb(llm.StreamChunk{Kind: llm.ChunkText, Text: string(r)})
	}
	cb(llm.StreamChunk{Kind: llm.ChunkDone})
	return llm.Usage{}, nil
}

// Test cases.

// TestFallbackPrefix verifies 30-character truncation without splitting multibyte characters.
func TestFallbackPrefix(t *testing.T) {
	long := "这是一个非常长的用户消息，用来验证标题前缀会被截断到三十个字以内，后面的内容就不再出现在标题里了。"
	got := sessiontitle.Fallback(long)
	if len([]rune(got)) != 30 && len([]rune(got)) != 31 { // Thirty characters plus an optional ellipsis.
		t.Fatalf("fallback 应截断到约 30 字(不含…计30字)，实际 %d 字(%q)", len([]rune(got)), got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("超长输入应以省略号结尾，实际 %q", got)
	}
	// Truncation must preserve valid UTF-8.
	if !isValidUTF8(got) {
		t.Fatalf("fallback 输出包含非法 UTF-8: %q", got)
	}
}

// TestFallbackShort preserves short messages without an ellipsis.
func TestFallbackShort(t *testing.T) {
	got := sessiontitle.Fallback("修复登录报错")
	if got != "修复登录报错" {
		t.Fatalf("短消息应原样返回，实际 %q", got)
	}
}

// TestFallbackEmpty returns an empty title for empty content.
func TestFallbackEmpty(t *testing.T) {
	if got := sessiontitle.Fallback("   "); got != "" {
		t.Fatalf("空白内容应返回空标题，实际 %q", got)
	}
}

// TestNoLLMFallsBack uses fallback when the LLM is disabled.
func TestNoLLMFallsBack(t *testing.T) {
	gen := &sessiontitle.Generator{} // No LLM configured.
	res := gen.Generate(context.Background(), "请帮我部署一个 go 服务")
	if res.Source != sessiontitle.SourceFallback {
		t.Fatalf("无 LLM 时应 Source=fallback，实际 %v", res.Source)
	}
	if res.Title == "" {
		t.Fatal("fallback 标题不能为空")
	}
}

// TestLLMGenerated adopts a valid generated title.
func TestLLMGenerated(t *testing.T) {
	gen := &sessiontitle.Generator{LLM: &mockLLM{text: "【部署 Go 服务】"}}
	res := gen.Generate(context.Background(), "请帮我部署一个 go 服务")
	if res.Source != sessiontitle.SourceLLM {
		t.Fatalf("LLM 成功时应 Source=llm，实际 %v", res.Source)
	}
	// Remove surrounding quotation marks to obtain the plain title.
	if trimmed := sessiontitle.TrimTitle(res.Title); sessiontitle.TrimTitle(trimmed) == "" {
		t.Fatalf("LLM 标题为空: %q", res.Title)
	}
	if strings.Contains(res.Title, "【") || strings.Contains(res.Title, "】") {
		t.Fatalf("LLM 标题未清理装饰符号: %q", res.Title)
	}
	if res.Title != "部署 Go 服务" {
		t.Fatalf("LLM 标题清理后应为「部署 Go 服务」，实际 %q", res.Title)
	}
}

// TestLLMFailureFallsBack uses fallback after an LLM failure.
func TestLLMFailureFallsBack(t *testing.T) {
	gen := &sessiontitle.Generator{LLM: &mockLLM{err: &llm.LlmFailure{Kind: llm.FailOverload, Message: "overloaded"}}}
	res := gen.Generate(context.Background(), "请帮我部署一个 go 服务")
	if res.Source != sessiontitle.SourceFallback {
		t.Fatalf("LLM 失败应回退 fallback，实际 %v", res.Source)
	}
	if res.Title != "请帮我部署一个 go 服务" {
		t.Fatalf("回退标题应为首条消息前缀，实际 %q", res.Title)
	}
}

// TestLLMEmptyOutputFallsBack uses fallback for empty LLM output.
func TestLLMEmptyOutputFallsBack(t *testing.T) {
	gen := &sessiontitle.Generator{LLM: &mockLLM{text: "  \n  "}}
	res := gen.Generate(context.Background(), "帮我写测试用例")
	if res.Source != sessiontitle.SourceFallback {
		t.Fatalf("LLM 空输出应回退 fallback，实际 %v", res.Source)
	}
	if res.Title != "帮我写测试用例" {
		t.Fatalf("回退标题应为首条消息前缀，实际 %q", res.Title)
	}
}

// TestTitleEventFold reads the latest session/title event.
func TestTitleEventFold(t *testing.T) {
	sl := session.NewSessionLog(brand.NewSessionID("title-s1"))
	// Write an old title followed by a new one; folding must select the new title.
	if _, err := sl.Append(session.SessionTitleData{Title: "旧标题"}); err != nil {
		t.Fatalf("追加旧标题事件失败: %v", err)
	}
	if _, err := sl.Append(session.SessionTitleData{Title: "新标题"}); err != nil {
		t.Fatalf("追加新标题事件失败: %v", err)
	}
	proj := session.FoldAllFromLog(sl)
	if !proj.Title.Present || proj.Title.Title != "新标题" {
		t.Fatalf("latest-wins fold 应为「新标题」，实际 %+v", proj.Title)
	}
}

// isValidUTF8 verifies truncation has not split a multibyte character.
func isValidUTF8(s string) bool {
	return utf8.ValidString(s)
}

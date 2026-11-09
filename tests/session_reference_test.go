// M17 session-reference tests cover @session/<id> and #<path/file> parsing, mention removal and
// snapshot aggregation, stable missing/out-of-workspace/self-reference/limit errors, and
// user/message reference audit data.
package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/session"
)

// stubSessionResolver resolves sessions from a map and delegates files to a workspace resolver.
type stubSessionResolver struct {
	sessions map[string]string
	fs       *session.WorkspaceFileResolver
}

func (r *stubSessionResolver) ResolveSession(id brand.SessionID) (string, error) {
	if snap, ok := r.sessions[id.Raw()]; ok {
		return snap, nil
	}
	return "", &session.SessionReferenceError{Code: session.RefCodeSessionNotFound, Msg: "session " + id.Raw() + " not found"}
}

func (r *stubSessionResolver) ResolveFile(p string) (string, error) {
	return r.fs.ResolveFile(p)
}

func (r *stubSessionResolver) WorkspaceRoot() string { return r.fs.WorkspaceRoot() }

// newTestResolver creates a resolver rooted at a temporary workspace.
func newTestResolver(t *testing.T, sessions map[string]string) *stubSessionResolver {
	t.Helper()
	root := t.TempDir()
	return &stubSessionResolver{
		sessions: sessions,
		fs:       &session.WorkspaceFileResolver{Root: root},
	}
}

// TestSessionReferenceMentionParsing verifies mention syntax.
func TestSessionReferenceMentionParsing(t *testing.T) {
	text := `请对比 @session/abc-123 与 @session/xyz_9 的实现，并参考 #src/main.go 与 #docs/readme.md`
	ms := session.ParseMentions(text)
	if len(ms) != 4 {
		t.Fatalf("应解析 4 个 mention, 实际 %d: %+v", len(ms), ms)
	}
	// Mixed session and file mentions must retain source-text order.
	want := []session.Mention{
		{Kind: session.MentionSession, Value: "abc-123"},
		{Kind: session.MentionSession, Value: "xyz_9"},
		{Kind: session.MentionFile, Value: "src/main.go"},
		{Kind: session.MentionFile, Value: "docs/readme.md"},
	}
	for i, w := range want {
		if ms[i].Kind != w.Kind || ms[i].Value != w.Value {
			t.Errorf("mention[%d] = %+v, want %+v", i, ms[i], w)
		}
	}
}

// TestSessionReferencePrepareAggregates combines three sessions and a file correctly.
func TestSessionReferencePrepareAggregates(t *testing.T) {
	root := t.TempDir()
	mainGo := filepath.Join(root, "src", "main.go")
	if err := os.MkdirAll(filepath.Dir(mainGo), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mainGo, []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := &stubSessionResolver{
		sessions: map[string]string{
			"s1": "session-1 snapshot",
			"s2": "session-2 snapshot",
			"s3": "session-3 snapshot",
		},
		fs: &session.WorkspaceFileResolver{Root: root},
	}
	text := `请结合 @session/s1、@session/s2 与 @session/s3，再看 #src/main.go`
	prepared, err := session.Prepare(session.PrepareRequest{
		AgentSessionID: brand.NewSessionID("self"),
		Text:           text,
		Resolver:       res,
	})
	if err != nil {
		t.Fatalf("Prepare 失败: %v", err)
	}

	// Readable text remains after mentions are removed.
	if strings.Contains(prepared.Content, "@session/") {
		t.Errorf("Content 仍含会话 mention: %q", prepared.Content)
	}
	if strings.Contains(prepared.Content, "#src/main.go") {
		t.Errorf("Content 仍含文件 mention: %q", prepared.Content)
	}
	// The aggregate context includes every referenced snapshot.
	for _, want := range []string{"session-1 snapshot", "session-2 snapshot", "session-3 snapshot", "file \"src/main.go\""} {
		if !strings.Contains(prepared.AdditionalContext, want) {
			t.Errorf("additionalContext 缺少 %q\n上下文:\n%s", want, prepared.AdditionalContext)
		}
	}
	// All references must appear in the audit record.
	if len(prepared.References) != 4 {
		t.Fatalf("References 应有 4 条, 实际 %d", len(prepared.References))
	}
}

// TestSessionReferenceSelfReferenceRejected returns SELF_REFERENCE.
func TestSessionReferenceSelfReferenceRejected(t *testing.T) {
	res := newTestResolver(t, map[string]string{})
	_, err := session.Prepare(session.PrepareRequest{
		AgentSessionID: brand.NewSessionID("me"),
		Text:           `请看我 @session/me`,
		Resolver:       res,
	})
	if session.RefCodeFrom(err) != session.RefCodeSelfReference {
		t.Fatalf("应返回 SELF_REFERENCE, 实际: %v", err)
	}
}

// TestSessionReferenceClassificationErrors distinguishes missing sessions and files.
func TestSessionReferenceClassificationErrors(t *testing.T) {
	root := t.TempDir()
	res := newTestResolver(t, map[string]string{}) // No sessions registered.
	res.fs.Root = root

	// A missing session returns SESSION_NOT_FOUND.
	_, err := session.Prepare(session.PrepareRequest{
		Text:     `请读 @session/ghost`,
		Resolver: res,
	})
	if session.RefCodeFrom(err) != session.RefCodeSessionNotFound {
		t.Fatalf("应返回 SESSION_NOT_FOUND, 实际: %v", err)
	}

	// A missing file returns FILE_NOT_FOUND.
	_, err = session.Prepare(session.PrepareRequest{
		Text:     `见 #no/such/file.go`,
		Resolver: res,
	})
	if session.RefCodeFrom(err) != session.RefCodeFileNotFound {
		t.Fatalf("应返回 FILE_NOT_FOUND, 实际: %v", err)
	}

	// A ../ path outside the workspace returns FILE_OUT_OF_WORKSPACE.
	_, err = session.Prepare(session.PrepareRequest{
		Text:     `读 #../outside.txt`,
		Resolver: res,
	})
	if session.RefCodeFrom(err) != session.RefCodeFileOutOfWorkspace {
		t.Fatalf("应返回 FILE_OUT_OF_WORKSPACE, 实际: %v", err)
	}
}

// TestSessionReferenceTooManyRejected returns TOO_MANY above the reference limit.
func TestSessionReferenceTooManyRejected(t *testing.T) {
	res := newTestResolver(t, map[string]string{})
	var sb strings.Builder
	for i := 0; i < 20; i++ {
		sb.WriteString("@session/abc")
		sb.WriteString(string(rune('a' + i)))
		sb.WriteString(" ")
	}
	_, err := session.Prepare(session.PrepareRequest{
		Text:     sb.String(),
		Resolver: res,
	})
	if session.RefCodeFrom(err) != session.RefCodeTooMany {
		t.Fatalf("应返回 TOO_MANY, 实际: %v", err)
	}
}

// TestSessionReferenceBudgetExceeded returns BUDGET_EXCEEDED for oversized aggregate context.
func TestSessionReferenceBudgetExceeded(t *testing.T) {
	res := newTestResolver(t, map[string]string{"big": strings.Repeat("x", 1000)})
	_, err := session.Prepare(session.PrepareRequest{
		Text:            `看 @session/big`,
		Resolver:        res,
		MaxContextBytes: 100,
	})
	if session.RefCodeFrom(err) != session.RefCodeBudgetExceeded {
		t.Fatalf("应返回 BUDGET_EXCEEDED, 实际: %v", err)
	}
}

// TestSessionReferenceWriteAuditData maps prepared references into user/message source and refs
// fields.
func TestSessionReferenceWriteAuditData(t *testing.T) {
	res := newTestResolver(t, map[string]string{"s1": "snapshot-1"})
	prepared, err := session.Prepare(session.PrepareRequest{
		Text:     `结合 @session/s1`,
		Resolver: res,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Construct a reference-source user message and verify its UserMessageData audit fields.
	refs := make([]any, 0, len(prepared.References))
	for _, r := range prepared.References {
		refs = append(refs, r)
	}
	msg := session.UserMessageData{
		Content: prepared.Content,
		Source:  "reference",
		Refs:    refs,
	}
	// UserMessageData.Source identifies reference-origin messages for downstream auditing.
	if msg.Source != "reference" || msg.Content != prepared.Content {
		t.Fatalf("user/message 审计数据不完整: %+v", msg)
	}
}

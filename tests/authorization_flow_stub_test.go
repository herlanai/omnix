// S06 Authorization Service tests cover listing flows, beginning a token, completing the callback,
// resolving credentials through M39, canceling pending flows, rejecting repeated
// completion/cancellation, and querying state.
package tests

import (
	"context"
	"testing"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/credentials"
)

// TestAuthFlowBeginCompleteResolve verifies begin, callback completion, and credential resolution.
func TestAuthFlowBeginCompleteResolve(t *testing.T) {
	ctx := context.Background()
	store := credentials.NewMemoryStore()
	auth := credentials.NewAuthorizationService()
	svc := credentials.NewAuthService(auth, store)

	flowID := brand.NewCredentialRef("GITHUB_TOKEN")
	// Register a stub flow whose Begin returns a placeholder URL and whose Cancel is a no-op.
	if err := auth.Register(&credentials.Flow{
		ID:   flowID,
		Name: "github",
		Begin: func(ctx context.Context) (string, error) {
			return "https://example.com/authorize?state=stub", nil
		},
	}); err != nil {
		t.Fatal(err)
	}

	// The listed flows must include github.
	found := false
	for _, f := range auth.List() {
		if f.ID == flowID {
			found = true
		}
	}
	if !found {
		t.Fatal("list flows 应包含 github")
	}

	// begin → token。
	token, err := svc.Begin(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	if token == "" {
		t.Fatal("begin 应返回非空 token")
	}

	// Complete the callback and store the credential.
	if err := svc.Complete(ctx, token, "ghp_stub_secret"); err != nil {
		t.Fatal(err)
	}
	// The session state must be completed.
	if sess, ok := svc.Status(token); !ok || sess.Status != credentials.AuthCompleted {
		t.Fatalf("会话应 completed，实际 %+v ok=%v", sess, ok)
	}
	// M39 Resolve must return the stored credential.
	if v, ok := store.Resolve(ctx, flowID); !ok || v != "ghp_stub_secret" {
		t.Fatalf("Resolve 应取到回调凭证，实际 %q ok=%v", v, ok)
	}
}

// TestAuthCancel cancels a pending session.
func TestAuthCancel(t *testing.T) {
	ctx := context.Background()
	store := credentials.NewMemoryStore()
	auth := credentials.NewAuthorizationService()
	svc := credentials.NewAuthService(auth, store)

	flowID := brand.NewCredentialRef("SLACK_TOKEN")
	_ = auth.Register(&credentials.Flow{ID: flowID, Name: "slack",
		Begin: func(context.Context) (string, error) { return "u", nil }})

	token, _ := svc.Begin(ctx, flowID)
	if err := svc.Cancel(ctx, token); err != nil {
		t.Fatal(err)
	}
	sess, _ := svc.Status(token)
	if sess.Status != credentials.AuthCancelled {
		t.Fatalf("状态应 cancelled，实际 %s", sess.Status)
	}
	// Completion after cancellation must be rejected.
	if err := svc.Complete(ctx, token, "x"); err == nil {
		t.Fatal("cancelled 会话不应再完成")
	}
}

// TestAuthDoubleCompleteRejected rejects repeated completion.
func TestAuthDoubleCompleteRejected(t *testing.T) {
	ctx := context.Background()
	store := credentials.NewMemoryStore()
	auth := credentials.NewAuthorizationService()
	svc := credentials.NewAuthService(auth, store)

	flowID := brand.NewCredentialRef("A_KEY")
	_ = auth.Register(&credentials.Flow{ID: flowID, Name: "a",
		Begin: func(context.Context) (string, error) { return "u", nil }})

	token, _ := svc.Begin(ctx, flowID)
	if err := svc.Complete(ctx, token, "v1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Complete(ctx, token, "v2"); err == nil {
		t.Fatal("已 completed 会话二次完成应被拒绝")
	}
	// The first credential value must be retained.
	if v, ok := store.Resolve(ctx, flowID); !ok || v != "v1" {
		t.Fatalf("凭证应为首值 v1，实际 %q", v)
	}
}

// TestAuthMissingToken rejects operations on unknown tokens.
func TestAuthMissingToken(t *testing.T) {
	ctx := context.Background()
	store := credentials.NewMemoryStore()
	svc := credentials.NewAuthService(credentials.NewAuthorizationService(), store)
	if _, err := svc.Begin(ctx, brand.NewCredentialRef("NO_FLOW")); err == nil {
		t.Fatal("未注册 flow 的 begin 应报错")
	}
	if err := svc.Complete(ctx, "not-exist", "v"); err == nil {
		t.Fatal("未知 token 的 complete 应报错")
	}
}

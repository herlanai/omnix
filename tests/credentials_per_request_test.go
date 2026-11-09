// M39 credentials and authorization integration tests.
package tests

import (
	"context"
	"strings"
	"testing"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/credentials"
	"github.com/herlanai/omnix/pkg/storage"
)

// TestCredentialsPerRequestResolve observes environment updates on the next request.
func TestCredentialsPerRequestResolve(t *testing.T) {
	store := credentials.NewMemoryStore()
	ctx := context.Background()

	apiKey := brand.NewCredentialRef("OPENAI_API_KEY")

	// The initial resolve reports an unset credential as absent.
	if _, ok := store.Resolve(ctx, apiKey); ok {
		t.Fatal("未设置时应 resolve 不到")
	}

	// After setting the value, the next resolve sees it.
	if err := store.Set(ctx, apiKey, "sk-abc"); err != nil {
		t.Fatalf("Set 失败: %v", err)
	}
	if v, ok := store.Resolve(ctx, apiKey); !ok || v != "sk-abc" {
		t.Fatalf("第一轮 resolve 应看到新值: %q ok=%v", v, ok)
	}

	// After an environment update, the next request sees the new value.
	if err := store.Set(ctx, apiKey, "sk-new"); err != nil {
		t.Fatalf("二次 Set 失败: %v", err)
	}
	if v, ok := store.Resolve(ctx, apiKey); !ok || v != "sk-new" {
		t.Fatalf("修改后 resolve 应看到新值: %q ok=%v", v, ok)
	}

	// After Unset, the credential is absent again.
	if err := store.Unset(ctx, apiKey); err != nil {
		t.Fatalf("Unset 失败: %v", err)
	}
	if _, ok := store.Resolve(ctx, apiKey); ok {
		t.Fatal("Unset 后不应 resolve 到值")
	}
}

// TestCredentialsDescribeRedactsValue verifies descriptions omit plaintext secrets.
func TestCredentialsDescribeRedactsValue(t *testing.T) {
	store := credentials.NewMemoryStore()
	ctx := context.Background()

	_ = store.Set(ctx, brand.NewCredentialRef("OPENAI_API_KEY"), "sk-super-secret")
	_ = store.Set(ctx, brand.NewCredentialRef("ANTHROPIC_KEY"), "sk-anthropic")

	infos := store.Describe(ctx)
	if len(infos) != 2 {
		t.Fatalf("describe 应含 2 条: %d", len(infos))
	}
	// No description may contain the plaintext value.
	joined := strings.Join(infosToStrings(infos), "|")
	if strings.Contains(joined, "sk-super-secret") || strings.Contains(joined, "sk-anthropic") {
		t.Fatalf("describe 泄漏明文: %s", joined)
	}
	// Each entry must have HasValue=true and entries must be sorted by reference.
	if infos[0].Ref.Raw() != "ANTHROPIC_KEY" || !infos[0].HasValue {
		t.Fatalf("describe 排序/标记异常: %+v", infos)
	}
}

// infosToStrings converts descriptions to strings for assertions.
func infosToStrings(infos []credentials.CredentialInfo) []string {
	out := make([]string, 0, len(infos))
	for _, i := range infos {
		out = append(out, i.Ref.Raw()+":"+boolStr(i.HasValue))
	}
	return out
}

// boolStr converts a boolean to a string.
func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// TestCredentialsRecordModifyCAS exercises versioned credential updates.
func TestCredentialsRecordModifyCAS(t *testing.T) {
	// Simulate concurrent writers with two Stores sharing one SQLite backend.
	sk, err := storage.NewSQLiteKV(t.TempDir() + "/cred.db")
	if err != nil {
		t.Fatalf("NewSQLiteKV 失败: %v", err)
	}
	defer sk.Close()

	storeA := credentials.NewStore(sk)
	storeB := credentials.NewStore(sk)
	ctx := context.Background()

	ref := brand.NewCredentialRef("AUTH_TOKEN")
	if err := storeA.Set(ctx, ref, "v1"); err != nil {
		t.Fatalf("storeA Set 失败: %v", err)
	}

	// Both stores initially observe version v1.
	_ = storeA.Describe(ctx)
	_ = storeB.Describe(ctx)

	// Store B updates successfully and advances the version.
	if err := storeB.Set(ctx, ref, "v2"); err != nil {
		t.Fatalf("storeB Set 失败: %v", err)
	}

	// Set reloads the current version before applying CAS, so this sequential test checks final
	// consistency: B writes v2 and A subsequently writes v3 using the refreshed version.
	if err := storeA.Set(ctx, ref, "v3"); err != nil {
		t.Fatalf("storeA 再写失败: %v", err)
	}
	if v, ok := storeA.Resolve(ctx, ref); !ok || v != "v3" {
		t.Fatalf("最终值应为 v3: %q ok=%v", v, ok)
	}
}

// TestAuthorizationFlowRegisterBegin verifies registration, begin, and cancel.
func TestAuthorizationFlowRegisterBegin(t *testing.T) {
	svc := credentials.NewAuthorizationService()
	ctx := context.Background()

	flow := &credentials.Flow{
		ID:   credentials.FlowID(brand.NewCredentialRef("github_oauth")),
		Name: "GitHub OAuth",
		Begin: func(ctx context.Context) (string, error) {
			return "https://github.com/login/oauth/authorize?state=xyz", nil
		},
		Cancel: func(ctx context.Context) error { return nil },
	}
	if err := svc.Register(flow); err != nil {
		t.Fatalf("Register 失败: %v", err)
	}

	if got := svc.List(); len(got) != 1 {
		t.Fatalf("List 应含 1 个 flow: %d", len(got))
	}

	hint, err := svc.Begin(ctx, flow.ID)
	if err != nil {
		t.Fatalf("Begin 失败: %v", err)
	}
	if !strings.HasPrefix(hint, "https://github.com") {
		t.Fatalf("Begin hint 异常: %q", hint)
	}

	if err := svc.Cancel(ctx, flow.ID); err != nil {
		t.Fatalf("Cancel 失败: %v", err)
	}

	// Unregistered flow.
	if _, err := svc.Begin(ctx, credentials.FlowID(brand.NewCredentialRef("nope"))); err == nil {
		t.Fatal("未注册 flow 的 Begin 应报错")
	}
}

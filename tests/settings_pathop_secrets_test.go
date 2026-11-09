// M38 settings path operations, CAS, and secrets tests.
package tests

import (
	"strings"
	"testing"

	"github.com/herlanai/omnix/pkg/settings"
)

// TestSettingsPathOpSecrets redacts secret values while retaining operations in Describe.
func TestSettingsPathOpSecrets(t *testing.T) {
	ns := settings.NewNamespace("omnix")
	ns.MarkSecret("credentials.api_key")
	ns.MarkSecret("credentials.secret")

	s := settings.NewSettingsScope(ns)
	_ = s.ApplyHost("sandbox.mode", "read-only")
	_ = s.ApplyHost("credentials.api_key", "sk-secret-123")

	// Override sandbox.mode and set credentials.secret in the session layer.
	err := s.Update(0, map[settings.Path]settings.PathOp{
		"sandbox.mode":       settings.NewSetOp("workspace-write"),
		"credentials.secret": settings.NewSetOp("supersecret"),
		"unused.path":        settings.NewUnsetOp(),
	})
	if err != nil {
		t.Fatalf("Update 失败: %v", err)
	}

	// With redactSecrets=true, expose the operation but omit the secret value.
	desc := s.Describe(true)
	apiKey := findPath(desc, "credentials.api_key")
	secret := findPath(desc, "credentials.secret")
	if apiKey == nil || secret == nil {
		t.Fatalf("secret 路径应存在: %+v", desc)
	}
	if apiKey.Value != nil {
		t.Fatalf("secret api_key 应脱敏: %+v", apiKey)
	}
	if secret.Value != nil {
		t.Fatalf("secret secret 应脱敏: %+v", secret)
	}
	if !apiKey.Secret || !secret.Secret {
		t.Fatal("secret 标记应为 true")
	}

	// With redactSecrets=false, expose the plaintext value.
	descOpen := s.Describe(false)
	if p := findPath(descOpen, "credentials.secret"); p == nil || p.Value != "supersecret" {
		t.Fatalf("不脱敏时应可见明文: %+v", p)
	}
}

// findPath locates a path in Describe output.
func findPath(list []settings.DescribedSetting, p settings.Path) *settings.DescribedSetting {
	for i := range list {
		if list[i].Path == p {
			return &list[i]
		}
	}
	return nil
}

// TestSettingsCASConcurrent rejects updates with a stale expectedRevision.
func TestSettingsCASConcurrent(t *testing.T) {
	ns := settings.NewNamespace("omnix")
	s := settings.NewSettingsScope(ns)

	// The first replace advances version 0 to 1.
	if _, err := 0, func() error {
		return s.Replace(0, map[settings.Path]any{"a": 1})
	}(); err != nil {
		t.Fatalf("首次 replace 失败: %v", err)
	}
	rev1 := s.Revision()
	if rev1 != 1 {
		t.Fatalf("首次 replace 后 revision = %d, want 1", rev1)
	}

	// After observing revision 1, another successful replace advances it to 2.
	if err := s.Replace(rev1, map[settings.Path]any{"b": 2}); err != nil {
		t.Fatalf("第二次 replace 失败: %v", err)
	}
	if s.Revision() != 2 {
		t.Fatalf("第二次 replace 后 revision = %d, want 2", s.Revision())
	}

	// Writing with stale revision 1 must produce a CAS conflict.
	err := s.Replace(rev1, map[settings.Path]any{"c": 3})
	if !settings.IsRevisionMismatch(err) {
		t.Fatalf("过期修订号写入应触发 CAS 错误, 实际 %v", err)
	}
	var mismatch *settings.ErrRevisionMismatch
	if !asRevMismatch(err, &mismatch) {
		t.Fatalf("错误类型应为 *ErrRevisionMismatch: %T", err)
	}
	if mismatch.Expected != rev1 || mismatch.Actual != 2 {
		t.Fatalf("CAS 字段异常: %+v", mismatch)
	}
}

// asRevMismatch performs a convenient type assertion.
func asRevMismatch(err error, target **settings.ErrRevisionMismatch) bool {
	e, ok := err.(*settings.ErrRevisionMismatch)
	if ok {
		*target = e
	}
	return ok
}

// TestSettingsNearestWins verifies session overrides take precedence over host defaults.
func TestSettingsNearestWins(t *testing.T) {
	ns := settings.NewNamespace("omnix")
	s := settings.NewSettingsScope(ns)
	_ = s.ApplyHost("sandbox.mode", "read-only")

	// Read the host value before overriding it.
	if v, ok := s.Get("sandbox.mode"); !ok || !strings.Contains(string(v), "read-only") {
		t.Fatalf("未覆盖时应读到 host 默认: %q ok=%v", v, ok)
	}

	// Apply a session override.
	if err := s.Update(0, map[settings.Path]settings.PathOp{
		"sandbox.mode": settings.NewSetOp("danger-full-access"),
	}); err != nil {
		t.Fatalf("Update 失败: %v", err)
	}
	if v, ok := s.Get("sandbox.mode"); !ok || !strings.Contains(string(v), "danger-full-access") {
		t.Fatalf("session 覆盖后应读到新值: %q ok=%v", v, ok)
	}

	// Describe must report the session-layer set operation.
	desc := s.Describe(true)
	if p := findPath(desc, "sandbox.mode"); p == nil || p.Op != "set" {
		t.Fatalf("session 覆盖后 op 应为 set: %+v", p)
	}
}

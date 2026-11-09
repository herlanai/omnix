// M06 SessionHeader metadata tests.
package tests

import (
	"testing"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/session"
)

// TestSessionHeaderRoundTrip verifies consistent header serialization and deserialization.
func TestSessionHeaderRoundTrip(t *testing.T) {
	h := session.NewSessionHeader(brand.NewSessionID("s1"), "/workspace")
	h.AgentPreset = "coder"

	data, err := h.Marshal()
	if err != nil {
		t.Fatalf("Marshal 失败: %v", err)
	}

	restored, err := session.UnmarshalSessionHeader(data)
	if err != nil {
		t.Fatalf("Unmarshal 失败: %v", err)
	}

	if restored.ID.Raw() != "s1" || restored.Cwd != "/workspace" || restored.AgentPreset != "coder" {
		t.Fatalf("字段还原不一致: %+v", restored)
	}
	if restored.Version != session.SessionFormatVersion {
		t.Fatalf("版本 = %d, want %d", restored.Version, session.SessionFormatVersion)
	}
	if restored.Origin != session.OriginCreated {
		t.Fatalf("origin = %q, want created", restored.Origin)
	}
	if restored.DelegationDepth != 0 {
		t.Fatalf("初始委派深度应为 0: %d", restored.DelegationDepth)
	}
}

// TestSessionHeaderUnknownVersionRejected fails closed on unknown versions.
func TestSessionHeaderUnknownVersionRejected(t *testing.T) {
	// Use version 999 to simulate a future header format.
	data := []byte(`{"version":999,"id":"s_x","createdAt":"2026-08-31T00:00:00Z"}`)

	_, err := session.UnmarshalSessionHeader(data)
	if err == nil {
		t.Fatal("未知版本头部应被拒绝读取")
	}
	// Expect SessionFormatUnsupportedError.
	var unsupported *session.SessionFormatUnsupportedError
	if !asUnsupported(err, &unsupported) {
		t.Fatalf("应返回 SessionFormatUnsupportedError, 实际 %T", err)
	}
}

// asUnsupported performs a convenient type assertion.
func asUnsupported(err error, target **session.SessionFormatUnsupportedError) bool {
	e, ok := err.(*session.SessionFormatUnsupportedError)
	if ok {
		*target = e
	}
	return ok
}

// TestSessionHeaderForkLineage verifies ParentSession and DelegationDepth after a fork.
func TestSessionHeaderForkLineage(t *testing.T) {
	parent := session.NewSessionHeader(brand.NewSessionID("parent_s"), "/ws")

	// The parent replayed 42 seed events.
	parent.SeedLength = 42

	child := parent.Fork(brand.NewSessionID("child_s"))
	if child.ParentSession.Raw() != "parent_s" {
		t.Fatalf("child.ParentSession = %q, want parent_s", child.ParentSession.Raw())
	}
	if child.Origin != session.OriginFork {
		t.Fatalf("child.Origin = %q, want fork", child.Origin)
	}
	if child.DelegationDepth != 1 {
		t.Fatalf("child.DelegationDepth = %d, want 1", child.DelegationDepth)
	}
	if child.SeedLength != 0 {
		t.Fatalf("fork 子会话 SeedLength 应为 0: %d", child.SeedLength)
	}
	if child.Cwd != "/ws" {
		t.Fatalf("child.Cwd 应继承父会话: %q", child.Cwd)
	}

	// Set the child's SeedLength after replay to preserve cold-resume semantics.
	child.SeedLength = 7

	// Increment delegation depth again.
	grandchild := child.Fork(brand.NewSessionID("gc_s"))
	if grandchild.DelegationDepth != 2 {
		t.Fatalf("grandchild.DelegationDepth = %d, want 2", grandchild.DelegationDepth)
	}
	if grandchild.ParentSession.Raw() != "child_s" {
		t.Fatalf("grandchild.ParentSession = %q, want child_s", grandchild.ParentSession.Raw())
	}
}

// TestSessionHeaderValidate rejects headers missing required fields.
func TestSessionHeaderValidate(t *testing.T) {
	bad := &session.SessionHeader{Version: session.SessionFormatVersion}
	if err := bad.Validate(); err == nil {
		t.Fatal("缺 ID 的头部校验应失败")
	}

	good := session.NewSessionHeader(brand.NewSessionID("s1"), "/ws")
	if err := good.Validate(); err != nil {
		t.Fatalf("合法头部校验应通过: %v", err)
	}
}

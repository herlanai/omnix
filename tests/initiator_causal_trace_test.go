// M33 Agent initiator context and security attribution tests.
package tests

import (
	"context"
	"testing"

	"github.com/herlanai/omnix/pkg/agent"
	"github.com/herlanai/omnix/pkg/brand"
)

// TestInitiatorCausalTrace verifies identity propagation through context.
func TestInitiatorCausalTrace(t *testing.T) {
	ctx := agent.WithInitiator(context.Background(), brand.NewSessionID("agent_x"), "Run")

	// Identity remains available in a child call.
	weakCtx := context.WithValue(ctx, "somekey", "v") // Unrelated context values must not affect the initiator.
	ini, err := agent.RequireInitiator(weakCtx)
	if err != nil {
		t.Fatalf("RequireInitiator 失败: %v", err)
	}
	if ini.AgentID.Raw() != "agent_x" {
		t.Fatalf("AgentID = %q, want agent_x", ini.AgentID.Raw())
	}
	if ini.Op != "Run" {
		t.Fatalf("Op = %q, want Run", ini.Op)
	}
}

// TestInitiatorUnauthorized returns a structured error without WithInitiator.
func TestInitiatorUnauthorized(t *testing.T) {
	_, err := agent.RequireInitiator(context.Background())
	if err == nil {
		t.Fatal("无 initiator 调用应报错")
	}
	var unauthorized *agent.UnauthorizedInitiatorError
	if !asUnauthorizedInitiator(err, &unauthorized) {
		t.Fatalf("应为 UnauthorizedInitiatorError, 实际 %T", err)
	}
}

// TestInitiatorWithout removes identity so Require fails.
func TestInitiatorWithout(t *testing.T) {
	withIni := agent.WithInitiator(context.Background(), brand.NewSessionID("a"), "op")
	cleared := agent.WithoutInitiator(withIni)

	if _, err := agent.RequireInitiator(cleared); err == nil {
		t.Fatal("withoutInitiator 后 initiator 应被清除")
	}
}

// TestInitiatorMustPanic panics when no initiator is present.
func TestInitiatorMustPanic(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("MustInitiator 无 initiator 应 panic")
		}
	}()
	_ = agent.MustInitiator(context.Background())
}

// asUnauthorizedInitiator performs a convenient type assertion.
func asUnauthorizedInitiator(err error, target **agent.UnauthorizedInitiatorError) bool {
	e, ok := err.(*agent.UnauthorizedInitiatorError)
	if ok {
		*target = e
	}
	return ok
}

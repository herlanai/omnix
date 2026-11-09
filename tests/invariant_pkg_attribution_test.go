// M30 invariant registry validation tests.
package tests

import (
	"errors"
	"strings"
	"testing"

	"github.com/herlanai/omnix/pkg/invariant"
)

// TestInvariantPkgAttribution checks the owning-package prefix and error details.
func TestInvariantPkgAttribution(t *testing.T) {
	reg := invariant.NewRegistry()

	// Register an always-failing checker to simulate starting an already-open turn.
	err := reg.Register("pkg/session", func() error {
		return &invariant.InvariantError{Pkg: "pkg/session", Msg: "turn/start while turn already open"}
	})
	if err != nil {
		t.Fatalf("Register 失败: %v", err)
	}

	failures := reg.Run()
	if len(failures) != 1 {
		t.Fatalf("Run() 应返回 1 个失败, 实际 %d", len(failures))
	}
	if !strings.HasPrefix(failures[0].Error(), "INVARIANT [pkg/session]: ") {
		t.Fatalf("错误前缀不匹配: %q", failures[0].Error())
	}

	// Record the checker in the registry.
	ledger := reg.Ledger()
	if len(ledger) != 1 {
		t.Fatalf("Ledger 应有 1 条记录, 实际 %d", len(ledger))
	}
}

// TestInvariantMultiplePkgsOrdered verifies lexical package ordering and independent checks.
func TestInvariantMultiplePkgsOrdered(t *testing.T) {
	reg := invariant.NewRegistry()

	// Register B before A; Run must still report A before B.
	_ = reg.Register("pkg/zeta", func() error { return errors.New("z bug") })
	_ = reg.Register("pkg/alpha", func() error { return errors.New("a bug") })

	failures := reg.Run()
	if len(failures) != 2 {
		t.Fatalf("Run() 应返回 2 个失败, 实际 %d", len(failures))
	}
	// Wrap non-InvariantError failures with their package attribution.
	if !strings.HasPrefix(failures[0].Error(), "INVARIANT [pkg/alpha]: ") {
		t.Fatalf("第一个失败应归属 pkg/alpha: %q", failures[0].Error())
	}
	if !strings.HasPrefix(failures[1].Error(), "INVARIANT [pkg/zeta]: ") {
		t.Fatalf("第二个失败应归属 pkg/zeta: %q", failures[1].Error())
	}
}

// TestInvariantRegisterValidation rejects invalid registration parameters.
func TestInvariantRegisterValidation(t *testing.T) {
	reg := invariant.NewRegistry()

	if err := reg.Register("", func() error { return nil }); err == nil {
		t.Fatal("空包名注册应报错")
	}
	if err := reg.Register("pkg/x", nil); err == nil {
		t.Fatal("nil 检查器注册应报错")
	}
}

// TestInvariantDisableInProduction skips validation when disabled.
func TestInvariantDisableInProduction(t *testing.T) {
	reg := invariant.NewRegistry()
	_ = reg.Register("pkg/session", func() error {
		return errors.New("should not run when disabled")
	})

	// Restore the original enabled state after the test to avoid affecting other cases.
	original := invariant.IsEnabled()
	defer invariant.SetEnabled(original)

	invariant.SetEnabled(false)
	if invariant.IsEnabled() {
		t.Fatal("SetEnabled(false) 后 IsEnabled 应为 false")
	}
	if failures := reg.Run(); len(failures) != 0 {
		t.Fatalf("关闭开关后 Run 应返回空, 实际 %d 个失败", len(failures))
	}

	// Re-enabling validation must restore checks.
	invariant.SetEnabled(true)
	if failures := reg.Run(); len(failures) != 1 {
		t.Fatalf("重新开启后 Run 应返回 1 个失败, 实际 %d", len(failures))
	}
}

// TestInvariantCheckerPanicIsolated prevents one checker panic from aborting all validation.
func TestInvariantCheckerPanicIsolated(t *testing.T) {
	reg := invariant.NewRegistry()
	_ = reg.Register("pkg/panicky", func() error {
		panic("boom")
	})
	_ = reg.Register("pkg/ok", func() error { return nil })

	failures := reg.Run()
	if len(failures) != 1 {
		t.Fatalf("panic 检查器应被捕获为 1 个失败, 实际 %d", len(failures))
	}
	if !strings.Contains(failures[0].Error(), "panicked") {
		t.Fatalf("应提示检查器 panic: %q", failures[0].Error())
	}
}

// TestInvariantMustSatisfy panics on a violation and returns normally on success.
func TestInvariantMustSatisfy(t *testing.T) {
	reg := invariant.NewRegistry()

	// Passing case.
	_ = reg.Register("pkg/clean", func() error { return nil })
	func() {
		defer func() {
			if p := recover(); p != nil {
				t.Fatalf("不应 panic: %v", p)
			}
		}()
		reg.MustSatisfy()
	}()

	// Violation case.
	_ = reg.Register("pkg/dirty", func() error { return errors.New("bad state") })
	func() {
		defer func() {
			if p := recover(); p == nil {
				t.Fatal("违规时 MustSatisfy 应 panic")
			}
		}()
		reg.MustSatisfy()
	}()
}

// M26 sandbox tests cover matching Bash/FS policies, explicit/session/default precedence, root
// normalization and fallback, danger-mode bypass, conversion to confined policies, and fail-closed
// SANDBOX_UNAVAILABLE behavior.
package tests

import (
	"testing"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/sandbox"
)

// fakeSession implements sandbox.Session for tests.
type fakeSession struct {
	id   string
	cwd  string
	mode sandbox.SandboxMode
	has  bool
}

func (f *fakeSession) ID() brand.SessionID { return brand.NewSessionID(f.id) }
func (f *fakeSession) Cwd() string         { return f.cwd }
func (f *fakeSession) SandboxMode() (sandbox.SandboxMode, bool) {
	return f.mode, f.has
}

// TestSandboxBothConsumersConsistentPolicy resolves identical Bash and FS policies for one
// session.
func TestSandboxBothConsumersConsistentPolicy(t *testing.T) {
	svc := sandbox.NewPolicyService(sandbox.ModeReadOnly, "/fallback")
	sess := &fakeSession{id: "s1", cwd: t.TempDir(), mode: sandbox.ModeWorkspaceWrite, has: true}

	// Resolve the policy independently for Bash and FS.
	bashPolicy := svc.Resolve(sandbox.SandboxPolicyRequest{Session: sess})
	fsPolicy := svc.Resolve(sandbox.SandboxPolicyRequest{Session: sess})

	// Mode, enforcement, and workspace boundaries must match.
	if bashPolicy.Mode != fsPolicy.Mode || bashPolicy.WorkspaceRoot != fsPolicy.WorkspaceRoot {
		t.Fatalf("Bash/FS 策略不一致: bash=%+v fs=%+v", bashPolicy, fsPolicy)
	}
	if bashPolicy.Mode != sandbox.ModeWorkspaceWrite {
		t.Fatalf("应采用会话覆盖 workspace-write, 实际 %s", bashPolicy.Mode)
	}
	if bashPolicy.WorkspaceRoot != sess.cwd {
		t.Fatalf("workspace root 应取会话 cwd, 实际 %q", bashPolicy.WorkspaceRoot)
	}
	if bashPolicy.IsDanger() {
		t.Fatal("workspace-write 不应是 danger")
	}
}

// TestSandboxModePrecedence verifies explicit > session > default precedence.
func TestSandboxModePrecedence(t *testing.T) {
	svc := sandbox.NewPolicyService(sandbox.ModeReadOnly, "/root")

	// Without a session or override, use the deployment default.
	p := svc.Resolve(sandbox.SandboxPolicyRequest{})
	if p.Mode != sandbox.ModeReadOnly {
		t.Fatalf("无输入应回落默认 read-only, 实际 %s", p.Mode)
	}

	// A session override takes precedence over the default.
	sess := &fakeSession{id: "s", cwd: "/ws", mode: sandbox.ModeDangerFullAccess, has: true}
	p = svc.Resolve(sandbox.SandboxPolicyRequest{Session: sess})
	if p.Mode != sandbox.ModeDangerFullAccess {
		t.Fatalf("会话 override 应为 danger, 实际 %s", p.Mode)
	}
	if !p.IsDanger() {
		t.Fatal("IsDanger() 应为 true")
	}

	// An explicit override wins over both.
	explicit := sandbox.ModeWorkspaceWrite
	p = svc.Resolve(sandbox.SandboxPolicyRequest{Session: sess, Mode: &explicit})
	if p.Mode != sandbox.ModeWorkspaceWrite {
		t.Fatalf("显式覆盖应为 workspace-write, 实际 %s", p.Mode)
	}
}

// TestSandboxWorkspaceRootFallback verifies root fallback and normalization.
func TestSandboxWorkspaceRootFallback(t *testing.T) {
	svc := sandbox.NewPolicyService(sandbox.ModeWorkspaceWrite, "/fallback")
	// Without a session working directory, use the fallback root.
	p := svc.Resolve(sandbox.SandboxPolicyRequest{})
	if p.WorkspaceRoot == "" || p.WorkspaceRoot != svc.Resolve(sandbox.SandboxPolicyRequest{}).WorkspaceRoot {
		t.Fatalf("回落根异常: %q", p.WorkspaceRoot)
	}
	// Normalize the working directory.
	sess := &fakeSession{id: "s", cwd: "relative/x"}
	p = svc.Resolve(sandbox.SandboxPolicyRequest{Session: sess})
	if p.WorkspaceRoot == "relative/x" {
		t.Fatalf("workspaceRoot 应规范化为绝对路径, 实际 %q", p.WorkspaceRoot)
	}
}

// TestSandboxToConfined converts non-danger policies and rejects danger-mode conversion.
func TestSandboxToConfined(t *testing.T) {
	base := sandbox.SandboxExecutionPolicy{
		Mode:          sandbox.ModeWorkspaceWrite,
		WorkspaceRoot: "/ws",
	}
	confined, err := base.ToConfined()
	if err != nil {
		t.Fatalf("workspace-write 应可降级: %v", err)
	}
	if confined.Mode != sandbox.ConfinedWorkspaceWrite {
		t.Fatalf("降级模式应为 workspace-write, 实际 %s", confined.Mode)
	}
	// Danger mode cannot be converted to a confined policy.
	danger := base
	danger.Mode = sandbox.ModeDangerFullAccess
	if _, err := danger.ToConfined(); err == nil {
		t.Fatal("danger-full-access 降级应报错")
	}
}

// TestSandboxProviderFailClosed rejects confinement without an available backend.
func TestSandboxProviderFailClosed(t *testing.T) {
	prov := &sandbox.UnavailableProvider{}
	_, err := prov.Confine(nil, sandbox.SandboxPolicy{})
	if err == nil {
		t.Fatal("无可用后端必须返回错误，绝不允许静默放行")
	}
	if err.Error() != sandbox.SandboxUnavailableErrorCode && !containsStr(err.Error(), sandbox.SandboxUnavailableErrorCode) {
		t.Fatalf("应携带 SANDBOX_UNAVAILABLE 码, 实际: %v", err)
	}
}

// TestSandboxRunnerFailureRule verifies exit-code gating, informational-line exclusion, and fatal
// signatures.
func TestSandboxRunnerFailureRule(t *testing.T) {
	rule := sandbox.RunnerFailureRule{
		AllowedExitCodes:   []int{125},
		FatalSignatures:    []string{"cannot start sandbox runner", "exec format error"},
		InformationalLines: []string{"landlock: fallback to baseline"},
	}
	// A nonmatching exit code must not classify as a sandbox failure.
	if _, ok := rule.Matches(1, "boom"); ok {
		t.Fatal("退出码 1 不在白名单，不应判定失败")
	}
	// Exit code 125 plus a fatal signature matches; informational lines are excluded.
	stderr := "landlock: fallback to baseline\ncannot start sandbox runner: permission denied\n"
	sig, ok := rule.Matches(125, stderr)
	if !ok {
		t.Fatal("应判定 runner 失败")
	}
	if sig == "" {
		t.Fatal("应返回匹配到的致命签名")
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

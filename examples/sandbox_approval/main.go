// Sandbox and approval tutorial. Before an Agent operates on files or processes, approval decides
// allow/deny/ask and sandboxing bounds the permitted side effects. Approval policies are
// allow-all, deny-all, ask-dangerous, and ask-dangerous-tool-edit; resolution proceeds from preset
// to user to session, with the nearest scope winning. Approval grants only the current call.
// Sandbox modes are read-only, workspace-write, and danger-full-access, resolved from explicit
// override, session record, then deployment default. This example covers policy resolution,
// three-way decisions, sandbox modes, and the deny-by-default stub. Run: go run
// ./examples/sandbox_approval. See pkg/approval, pkg/sandbox, and pkg/userq.
package main

import (
	"fmt"

	"github.com/herlanai/omnix/pkg/approval"
	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/sandbox"
	"github.com/herlanai/omnix/pkg/userq"
)

func main() {
	// A. Approval: map the safe preset to deny-all and dev to ask-dangerous.
	presetPolice := func(preset string) (approval.Policy, bool) {
		switch preset {
		case "safe":
			return approval.PolicyDenyAll, true
		case "dev":
			return approval.PolicyAskDangerous, true
		default:
			return "", false
		}
	}
	// Use a question stub that rejects by default to demonstrate fail-closed behavior.
	uq := userq.New(userq.NewStub())
	svc := approval.New(presetPolice, uq)

	// 1. Resolve the dev preset to ask-dangerous with source=preset.
	eff := svc.Resolve("dev", "")
	fmt.Println("— A1. 策略解析（预设 dev）—")
	fmt.Printf("  生效策略=%s, 来源=%s\n", eff.Policy, eff.Source)

	// 2. A session-level deny-all override takes precedence.
	sessID := "sess-1"
	svc.SetSessionPolicy(sessID, approval.PolicyDenyAll)
	eff2 := svc.Resolve("dev", sessID)
	fmt.Println("— A2. 会话层 override（nearest-scope-wins）—")
	fmt.Printf("  生效策略=%s, 来源=%s\n", eff2.Policy, eff2.Source)

	// 3. deny-all rejects even read operations.
	d, err := svc.Evaluate(approval.Request{Tool: "fs_read", Preset: "dev", SessionID: sessID})
	fmt.Println("— A3. 决策三态（deny-all）—")
	fmt.Printf("  fs_read => %s (err=%v)\n", d, err)

	// 4. ask-dangerous prompts for dangerous tools; the stub rejects by default. Safe tools such as
	// fs_read are allowed without prompting.
	d2, _ := svc.Evaluate(approval.Request{Tool: "shell", Preset: "dev"}) // shell is classified as dangerous.
	d3, _ := svc.Evaluate(approval.Request{Tool: "fs_read", Preset: "dev"})
	fmt.Println("— A4. ask-dangerous（stub 默认拒绝 = fail-closed）—")
	fmt.Printf("  shell(危险) => %s\n  fs_read(非危险) => %s\n", d2, d3)
	fmt.Printf("  (累计 ask 次数=%d)\n", svc.AskCount())

	// B. Sandbox: create an in-memory sandbox.Session exposing its ID, working directory, and session
	// override.
	ms := &memSession{id: brand.NewSessionID("sbox-sess"), cwd: "/workspace/app", mode: sandbox.ModeWorkspaceWrite}
	ps := sandbox.NewPolicyService(sandbox.ModeReadOnly, "/fallback")

	// 1. Without an explicit override, use the session's workspace-write mode.
	p1 := ps.Resolve(sandbox.SandboxPolicyRequest{Session: ms})
	fmt.Println("— B1. 沙箱策略（会话覆盖 workspace-write）—")
	fmt.Printf("  mode=%s, root=%s, danger=%v\n", p1.Mode, p1.WorkspaceRoot, p1.IsDanger())

	// 2. An explicit danger-full-access override takes precedence over the session record.
	danger := sandbox.ModeDangerFullAccess
	p2 := ps.Resolve(sandbox.SandboxPolicyRequest{Session: ms, Mode: &danger})
	fmt.Println("— B2. 显式 override danger-full-access —")
	fmt.Printf("  mode=%s, danger=%v（绕过沙箱，直接原始 argv）\n", p2.Mode, p2.IsDanger())

	// 3. Without a session, use the deployment's read-only mode and fallback root.
	p3 := ps.Resolve(sandbox.SandboxPolicyRequest{})
	fmt.Println("— B3. agentless 回落 —")
	fmt.Printf("  mode=%s, root=%s, danger=%v\n", p3.Mode, p3.WorkspaceRoot, p3.IsDanger())

	fmt.Println("\n— 结论：审批定「能不能」、沙箱定「在哪儿」，组合成受控执行 —")
}

// memSession implements sandbox.Session in memory for this tutorial.
type memSession struct {
	id   brand.SessionID
	cwd  string
	mode sandbox.SandboxMode
}

func (m *memSession) ID() brand.SessionID { return m.id }
func (m *memSession) Cwd() string         { return m.cwd }
func (m *memSession) SandboxMode() (sandbox.SandboxMode, bool) {
	if m.mode == "" {
		return "", false
	}
	return m.mode, true
}

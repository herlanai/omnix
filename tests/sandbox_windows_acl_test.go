//go:build windows
// +build windows

// Windows ACL backend tests.
package tests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/herlanai/omnix/pkg/sandbox"
)

// TestWindowsACLProviderAvailable verifies availability on Windows.
func TestWindowsACLProviderAvailable(t *testing.T) {
	p := sandbox.NewWindowsACLProvider(t.TempDir())
	if !p.Available() {
		t.Fatal("Windows ACL 后端在 Windows 上应可用")
	}
}

// TestWindowsACLProviderConfineReadOnly exercises read-only confinement.
func TestWindowsACLProviderConfineReadOnly(t *testing.T) {
	p := sandbox.NewWindowsACLProvider(t.TempDir())
	policy := sandbox.SandboxPolicy{
		SandboxExecutionPolicy: sandbox.SandboxExecutionPolicy{
			Mode:          sandbox.ModeReadOnly,
			WorkspaceRoot: t.TempDir(),
		},
		Mode: sandbox.ConfinedReadOnly,
	}
	result, err := p.Confine([]string{"echo", "hello"}, policy)
	if err != nil {
		t.Fatalf("Confine 不应报错: %v", err)
	}
	// Enforcement may be full or partial when a non-administrator cannot create a restricted token.
	if result.Enforcement != sandbox.EnforcementFull && result.Enforcement != sandbox.EnforcementPartial {
		t.Fatalf("enforcement 应为 full 或 partial, 实际 %s", result.Enforcement)
	}
	// Preserve the original arguments.
	if len(result.Argv) != 2 || result.Argv[0] != "echo" || result.Argv[1] != "hello" {
		t.Fatalf("argv 应保持不变, 实际 %v", result.Argv)
	}
}

// TestWindowsACLProviderCreateRestrictedToken verifies restricted-token creation.
func TestWindowsACLProviderCreateRestrictedToken(t *testing.T) {
	p := sandbox.NewWindowsACLProvider(t.TempDir())
	token, err := p.CreateRestrictedToken()
	if err != nil {
		t.Fatalf("CreateRestrictedToken 不应报错: %v", err)
	}
	// The token may be zero after non-administrator fallback; repeated calls must return the same
	// result.
	token2, err := p.CreateRestrictedToken()
	if err != nil {
		t.Fatalf("第二次 CreateRestrictedToken 不应报错: %v", err)
	}
	if token != token2 {
		t.Fatal("应返回缓存的令牌")
	}
}

// TestWindowsACLProviderSetupTempDir creates a temporary directory.
func TestWindowsACLProviderSetupTempDir(t *testing.T) {
	p := sandbox.NewWindowsACLProvider(t.TempDir())
	tempDir, err := p.SetupTempDir()
	if err != nil {
		t.Fatalf("SetupTempDir 不应报错: %v", err)
	}
	if tempDir == "" {
		t.Fatal("临时目录不应为空")
	}
	// Verify the directory exists.
	if _, err := os.Stat(tempDir); err != nil {
		t.Fatalf("临时目录应存在: %v", err)
	}
	// A second call must return the cached directory.
	tempDir2, err := p.SetupTempDir()
	if err != nil {
		t.Fatalf("第二次 SetupTempDir 不应报错: %v", err)
	}
	if tempDir != tempDir2 {
		t.Fatal("应返回缓存的临时目录")
	}
}

// TestWindowsACLProviderGrantWorkspaceAccess verifies workspace ACL setup.
func TestWindowsACLProviderGrantWorkspaceAccess(t *testing.T) {
	dir := t.TempDir()
	p := sandbox.NewWindowsACLProvider(dir)
	err := p.GrantWorkspaceAccess(dir)
	if err != nil {
		t.Fatalf("GrantWorkspaceAccess 不应报错: %v", err)
	}
	// The directory must still exist.
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("目录应存在: %v", err)
	}
}

// TestWindowsACLProviderCleanup verifies cleanup.
func TestWindowsACLProviderCleanup(t *testing.T) {
	p := sandbox.NewWindowsACLProvider(t.TempDir())
	// Create the token and temporary directory first.
	if _, err := p.CreateRestrictedToken(); err != nil {
		t.Fatalf("CreateRestrictedToken: %v", err)
	}
	tempDir, err := p.SetupTempDir()
	if err != nil {
		t.Fatalf("SetupTempDir: %v", err)
	}
	// Clean up resources.
	if err := p.Cleanup(); err != nil {
		t.Fatalf("Cleanup 不应报错: %v", err)
	}
	// The temporary directory must be removed.
	if _, err := os.Stat(tempDir); !os.IsNotExist(err) {
		t.Fatalf("临时目录应已删除, 但仍然存在或有其他错误: %v", err)
	}
}

// TestWindowsACLProviderSpawnWithToken starts a process using a restricted token.
func TestWindowsACLProviderSpawnWithToken(t *testing.T) {
	p := sandbox.NewWindowsACLProvider(t.TempDir())
	workDir := t.TempDir()
	// Create an output file.
	outputFile := filepath.Join(workDir, "output.txt")
	// Use cmd /c echo for the test.
	proc, err := p.SpawnWithToken(
		[]string{"cmd", "/c", "echo", "hello", ">", outputFile},
		nil,
		workDir,
	)
	if err != nil {
		t.Skipf("SpawnWithToken 可能需要特殊权限，跳过: %v", err)
	}
	if proc == nil {
		t.Fatal("进程不应为 nil")
	}
	// Wait for process termination.
	state, err := proc.Wait()
	if err != nil {
		t.Fatalf("Wait 不应报错: %v", err)
	}
	if !state.Success() {
		t.Fatalf("进程应成功退出, 实际 %v", state)
	}
}

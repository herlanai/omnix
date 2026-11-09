// Path tests cover symlink resolution with missing-path fallback, no writable roots for read-only,
// normalized/deduplicated workspace and temporary roots for workspace-write, and shared root
// semantics with filesystem guards.
package tests

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/herlanai/omnix/pkg/sandbox"
)

// TestCanonicalPathExisting normalizes an existing path.
func TestCanonicalPathExisting(t *testing.T) {
	dir := t.TempDir()
	canonical := sandbox.CanonicalPath(dir)
	if canonical == "" {
		t.Fatal("canonicalPath 不应返回空")
	}
	// The normalized path must exist.
	if _, err := os.Stat(canonical); err != nil {
		t.Fatalf("规范化后路径应存在: %v", err)
	}
}

// TestCanonicalPathMissing conservatively returns the original missing path.
func TestCanonicalPathMissing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nonexistent", "path")
	result := sandbox.CanonicalPath(missing)
	if result != missing {
		t.Fatalf("路径不存在时应返回原路径, 期望 %q, 实际 %q", missing, result)
	}
}

// TestWritableRootsReadOnly returns no writable roots.
func TestWritableRootsReadOnly(t *testing.T) {
	policy := sandbox.SandboxExecutionPolicy{
		Mode:          sandbox.ModeReadOnly,
		WorkspaceRoot: "/workspace/app",
	}
	roots := sandbox.WritableRoots(policy)
	if len(roots) != 0 {
		t.Fatalf("read-only 模式应返回空列表, 实际 %v", roots)
	}
}

// TestWritableRootsDanger returns no roots because danger-full-access bypasses confinement.
func TestWritableRootsDanger(t *testing.T) {
	policy := sandbox.SandboxExecutionPolicy{
		Mode:          sandbox.ModeDangerFullAccess,
		WorkspaceRoot: "/workspace/app",
	}
	roots := sandbox.WritableRoots(policy)
	if len(roots) != 0 {
		t.Fatalf("danger 模式应返回空列表, 实际 %v", roots)
	}
}

// TestWritableRootsWorkspaceWrite includes the workspace and temporary directories.
func TestWritableRootsWorkspaceWrite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 上 /tmp 路径语义不同，跳过")
	}
	policy := sandbox.SandboxExecutionPolicy{
		Mode:          sandbox.ModeWorkspaceWrite,
		WorkspaceRoot: "/workspace/app",
	}
	roots := sandbox.WritableRoots(policy)
	if len(roots) < 2 {
		t.Fatalf("workspace-write 应至少返回 workspaceRoot 和 tmpdir, 实际 %v", roots)
	}
	// The roots must include workspaceRoot.
	foundWorkspace := false
	for _, r := range roots {
		if r == "/workspace/app" {
			foundWorkspace = true
		}
	}
	if !foundWorkspace {
		t.Fatalf("应包含 workspaceRoot /workspace/app, 实际 %v", roots)
	}
}

// TestWritableRootsDeduplication removes duplicate paths.
func TestWritableRootsDeduplication(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 上跳过")
	}
	// If workspaceRoot equals /tmp, include it only once.
	policy := sandbox.SandboxExecutionPolicy{
		Mode:          sandbox.ModeWorkspaceWrite,
		WorkspaceRoot: "/tmp",
	}
	roots := sandbox.WritableRoots(policy)
	seen := map[string]bool{}
	for _, r := range roots {
		if seen[r] {
			t.Fatalf("路径重复: %s, 全部 %v", r, roots)
		}
		seen[r] = true
	}
}

// TestWritableRootsCanonical returns normalized paths.
func TestWritableRootsCanonical(t *testing.T) {
	dir := t.TempDir()
	policy := sandbox.SandboxExecutionPolicy{
		Mode:          sandbox.ModeWorkspaceWrite,
		WorkspaceRoot: dir,
	}
	roots := sandbox.WritableRoots(policy)
	// Normalize workspaceRoot.
	found := false
	for _, r := range roots {
		if r == sandbox.CanonicalPath(dir) {
			found = true
		}
	}
	if !found {
		t.Fatalf("应包含规范化后的 workspaceRoot, 实际 %v", roots)
	}
}

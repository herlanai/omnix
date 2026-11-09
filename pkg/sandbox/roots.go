// Writable-root calculation follows upstream sandbox/roots.ts. CanonicalPath
// resolves symlinks, including macOS /tmp to /private/tmp, retaining the original
// path on failure rather than inventing a fallback grant. WritableRoots is
// shared by process confinement and in-process filesystem guards.
// Workspace-write permits deduplicated canonical workspace and temporary roots;
// read-only and danger-full-access need no writable-root list.
package sandbox

import (
	"os"
	"path/filepath"
)

// CanonicalPath resolves symlinks to match the paths used by Seatbelt and
// filesystem containment checks. On failure it returns the original path:
// a missing root grants nothing until it exists, while inventing a fallback
// could grant a path the caller never named.
func CanonicalPath(path string) string {
	if path == "" {
		return path
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		// Symlink resolution failed because the path or a prefix is missing or unreadable.
		return filepath.Clean(path)
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(abs)
}

// WritableRoots returns canonical, deduplicated write grants. Read-only
// returns none. Workspace-write includes the policy workspace, /tmp, and
// os.TempDir so platform temporary-file creation remains permitted.
// Danger-full-access bypasses confinement and needs no root list.
func WritableRoots(policy SandboxExecutionPolicy) []string {
	if policy.Mode != ModeWorkspaceWrite {
		return nil
	}
	roots := []string{
		policy.WorkspaceRoot,
		"/tmp",
		os.TempDir(),
	}
	seen := map[string]bool{}
	var result []string
	for _, r := range roots {
		if r == "" {
			continue
		}
		canonical := CanonicalPath(r)
		if seen[canonical] {
			continue
		}
		seen[canonical] = true
		result = append(result, canonical)
	}
	return result
}

// This file builds profiles for bwrap, landlock, and seatbelt, following
// upstream sandbox-local/profiles.ts. Bwrap mounts the root read-only and
// adds writable temporary/workspace mounts for workspace-write. Landlock
// allows root reads and /dev/null writes, adding temporary/workspace writes
// when requested. Seatbelt denies file writes except /dev/null and permitted
// workspace subpaths. Functions return profile arguments; LocalProvider
// assembles the complete command.
package sandbox

import (
	"fmt"
	"strings"
)

// BwrapProfileArgs returns arguments between the runner and command separator.
// All modes mount / read-only, provide /dev and /proc, isolate PIDs, and enable
// die-with-parent. Workspace-write also mounts a writable /tmp tmpfs and
// binds the workspace root read-write.
func BwrapProfileArgs(policy SandboxPolicy) []string {
	args := []string{
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--unshare-pid",
		"--proc", "/proc",
		"--die-with-parent",
	}
	if policy.Mode == ConfinedWorkspaceWrite {
		args = append(args, "--tmpfs", "/tmp")
		args = append(args, "--bind", policy.WorkspaceRoot, policy.WorkspaceRoot)
	}
	return args
}

// LandlockProfileArgs builds launcher readOnly/readWrite grants.
// Read-only permits reads under / and writes to /dev/null, required by many
// programs. Workspace-write additionally permits /tmp and the workspace root.
func LandlockProfileArgs(policy SandboxPolicy) []string {
	readWrite := []string{"/dev/null"}
	if policy.Mode == ConfinedWorkspaceWrite {
		readWrite = append(readWrite, "/tmp", policy.WorkspaceRoot)
	}
	args := []string{}
	// readOnly
	args = append(args, "--read-only", "/")
	// readWrite
	for _, rw := range readWrite {
		args = append(args, "--read-write", rw)
	}
	return args
}

// SeatbeltProfileArgs returns -p and an SBPL script. The script enables
// version 1, allows defaults, denies file-write operations, then permits
// /dev/null writes. Workspace-write also allows writes under the workspace root.
func SeatbeltProfileArgs(policy SandboxPolicy) []string {
	forms := []string{
		"(version 1)",
		"(allow default)",
		"(deny file-write*)",
		fmt.Sprintf("(allow file-write* (literal %s))", sbplString("/dev/null")),
	}
	if policy.Mode == ConfinedWorkspaceWrite {
		roots := WritableRoots(policy.SandboxExecutionPolicy)
		if len(roots) > 0 {
			var subpaths []string
			for _, root := range roots {
				subpaths = append(subpaths, fmt.Sprintf("(subpath %s)", sbplString(root)))
			}
			forms = append(forms, fmt.Sprintf("(allow file-write* %s)", strings.Join(subpaths, " ")))
		}
	}
	return []string{"-p", strings.Join(forms, " ")}
}

// sbplString escapes paths as SBPL string literals, matching upstream:
// backslashes and double quotes are escaped.
func sbplString(path string) string {
	escaped := strings.ReplaceAll(path, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	return `"` + escaped + `"`
}

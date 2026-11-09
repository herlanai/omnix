// Workspace path helpers match util/workspace-path: resolve relative paths
// against roots, abbreviate home as ~, and derive display titles from final segments.
package workspace

import (
	"path/filepath"
	"strings"
)

// ResolvePath joins relative paths to root; absolute paths and paths with an
// empty root are unchanged. POSIX, Windows drive, and UNC absolute forms are recognized.
func ResolvePath(root, path string) string {
	if isAbsolutePath(path) {
		return path
	}
	if root == "" {
		return path
	}
	return filepath.Join(root, path)
}

// isAbsolutePath recognizes absolute paths across platforms.
func isAbsolutePath(p string) bool {
	if filepath.IsAbs(p) {
		return true
	}
	// Recognize POSIX leading slashes even on Windows.
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) {
		return true
	}
	return false
}

// AbbreviateHome replaces home prefixes with ~; paths outside home are unchanged.
func AbbreviateHome(path, home string) string {
	if home == "" {
		return path
	}
	root := strings.TrimRight(home, `/\`)
	if root == "" {
		return path
	}
	if path == root {
		return "~"
	}
	// Recognize both POSIX and Windows separators.
	if strings.HasPrefix(path, root+"/") || strings.HasPrefix(path, root+`\`) {
		return "~" + strings.TrimPrefix(path, root)
	}
	return path
}

// TitleOf returns the final path segment for display.
func TitleOf(path string) string {
	return filepath.Base(strings.TrimRight(path, `/\`))
}

// Package instructions implements pure instruction-file discovery logic from
// context/agent-instructions: project roots, ancestor chains, and per-directory deduplication.
package instructions

import (
	"os"
	"path/filepath"
	"strings"
)

// FindProjectRoot walks upward from cwd to the first root marker such as .git;
// if none exists before the filesystem root, it returns cwd.
func FindProjectRoot(cwd string, markers []string) string {
	current, err := filepath.Abs(cwd)
	if err != nil {
		return cwd
	}
	for {
		for _, m := range markers {
			if _, err := os.Stat(filepath.Join(current, m)); err == nil {
				return current
			}
		}
		parent := filepath.Dir(current)
		if parent == current {
			abs, _ := filepath.Abs(cwd)
			return abs
		}
		current = parent
	}
}

// AncestorChain returns directories from root to cwd, inclusive and broadest first.
// If cwd is outside root, it returns only root.
func AncestorChain(root, cwd string) []string {
	r, _ := filepath.Abs(root)
	c, _ := filepath.Abs(cwd)
	var chain []string
	current := c
	for current != r {
		chain = append(chain, current)
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	chain = append(chain, r)
	// Reverse the chain.
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain
}

// LoadedFile is an instruction candidate whose content has been read.
type LoadedFile struct {
	DisplayPath string
	Content     string
}

// DedupByDirectory keeps the earliest candidate with identical trimmed content
// within each directory. Identical content in different directories is retained.
func DedupByDirectory(files []LoadedFile) []LoadedFile {
	seen := map[string]map[string]struct{}{}
	var kept []LoadedFile
	for _, f := range files {
		dir := filepath.Dir(f.DisplayPath)
		digest := strings.TrimSpace(f.Content)
		set, ok := seen[dir]
		if !ok {
			set = map[string]struct{}{}
			seen[dir] = set
		}
		if _, dup := set[digest]; dup {
			continue
		}
		set[digest] = struct{}{}
		kept = append(kept, f)
	}
	return kept
}

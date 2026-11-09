// Package spill implements large-result storage (M42), matching spill/spill,
// spill-local, and spill-policy. SaveText persists exact text and returns an opaque
// locator, exact byte count, and retrieval hint; storage failures return errors.
//
// The local backend writes <root>/session-<sha256(sessionId)>/<random>-<safeName>
// under a private 0700 root, using exclusive creation and 0600 permissions to
// avoid symlink redirection. Consumer policy spills oversized tool/Bash output
// and retains a head/tail preview. On save failure it keeps original inline output
// so a successful tool call does not become an error solely because spilling failed.
package spill

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/herlanai/omnix/pkg/brand"
)

// Types.

// SpillLocator is an opaque model-facing artifact handle. Local backends use
// paths; remote or database backends may use URIs or keys. Consumers display the
// retrieval hint rather than parsing the handle.
type SpillLocator string

// String returns the raw handle for formatting and logging.
func (l SpillLocator) String() string { return string(l) }

// SpillOwner selects the storage namespace by producing session.
type SpillOwner struct {
	// SessionID identifies the session producing the artifact.
	SessionID brand.SessionID `json:"sessionId"`
}

// SpillSource identifies the producing tool and call for naming and inspection, not access control.
type SpillSource struct {
	// ToolName identifies the producing tool.
	ToolName string `json:"toolName"`
	// CallID identifies the model call producing the result.
	CallID brand.ToolCallID `json:"callId"`
	// Label briefly describes the artifact.
	Label string `json:"label"`
}

// SaveTextSpill requests text persistence.
type SaveTextSpill struct {
	// Owner selects the storage namespace.
	Owner SpillOwner `json:"owner"`
	// Source describes origin for naming and inspection.
	Source SpillSource `json:"source"`
	// SuggestedName is a proposed basename, sanitized by the backend into one safe path segment.
	SuggestedName string `json:"suggestedName"`
	// Content is the complete UTF-8 text to persist unchanged.
	Content string `json:"content"`
}

// SpillRef describes a saved artifact's locator, byte count, and retrieval hint.
type SpillRef struct {
	// Locator is an opaque model-facing handle.
	Locator SpillLocator `json:"locator"`
	// Bytes is the exact encoded byte count.
	Bytes int `json:"bytes"`
	// RetrievalHint guides model retrieval, such as read <path> or grep ... <path>.
	RetrievalHint string `json:"retrievalHint"`
}

// SpillStore is the single-method ctx.spillStore interface.
type SpillStore interface {
	// SaveText persists all content unchanged and returns actual storage failures.
	SaveText(input SaveTextSpill) (SpillRef, error)
}

// Local file backend.

// FileSpillStore writes artifacts into private, session-scoped host directories.
type FileSpillStore struct {
	// Root is a private 0700 directory, created automatically by default.
	Root string
}

// NewFileStore creates a local file backend.
func NewFileStore(root string) *FileSpillStore {
	return &FileSpillStore{Root: root}
}

// SaveText implements SpillStore using a private session directory.
func (s *FileSpillStore) SaveText(input SaveTextSpill) (SpillRef, error) {
	if s.Root == "" {
		return SpillRef{}, fmt.Errorf("spill: root not configured")
	}
	// Derive the session directory from SHA-256 of the session ID.
	sessHash := sha256sum(input.Owner.SessionID.Raw())
	dir := filepath.Join(s.Root, "session-"+sessHash[:16])
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return SpillRef{}, fmt.Errorf("spill: create session dir: %w", err)
	}
	// Sanitize the suggested name into a single safe segment.
	safe := sanitizeName(input.SuggestedName)
	// Use a random prefix, exclusive creation, and owner-only 0600 permissions.
	name := randomSuffix() + "-" + safe
	path := filepath.Join(dir, name)
	// Propagate actual storage failures.
	if err := os.WriteFile(path, []byte(input.Content), 0o600); err != nil {
		return SpillRef{}, fmt.Errorf("spill: write artifact: %w", err)
	}
	return SpillRef{
		Locator:       SpillLocator(path),
		Bytes:         len([]byte(input.Content)),
		RetrievalHint: fmt.Sprintf("read or grep this artifact with the tool at path %q", path),
	}, nil
}

// ReadText reads by locator to recover original bytes for consumers or tests.
func (s *FileSpillStore) ReadText(locator SpillLocator) (string, error) {
	data, err := os.ReadFile(string(locator))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// sanitizeName retains only [a-zA-Z0-9._-] in a single path segment.
func sanitizeName(name string) string {
	var sb strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			sb.WriteRune(r)
		} else {
			sb.WriteByte('_')
		}
	}
	out := sb.String()
	if out == "" {
		out = "artifact"
	}
	// Trim leading and trailing dots to avoid hidden files and traversal.
	out = strings.Trim(out, ".")
	if out == "" {
		out = "artifact"
	}
	return out
}

// randomSuffix generates a hex prefix from eight random bytes.
func randomSuffix() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// sha256sum returns the SHA-256 hex digest of a string.
func sha256sum(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// ============================================================================
// Spill Policy（post-tool / Bash listener）
// ============================================================================

// Apply spills text exceeding maxInlineBytes and returns a head/tail preview
// and reference. Smaller text returns unchanged with a nil reference. Save failures
// retain the original text and return an error for the caller to classify.
func Apply(store SpillStore, input SaveTextSpill, text string, maxInlineBytes, previewBytes int) (inline string, ref *SpillRef, err error) {
	if len([]byte(text)) <= maxInlineBytes {
		return text, nil, nil
	}
	r, serr := store.SaveText(SaveTextSpill{
		Owner:         input.Owner,
		Source:        input.Source,
		SuggestedName: input.SuggestedName,
		Content:       text,
	})
	if serr != nil {
		// Best effort: preserve inline output instead of turning a successful call into an error.
		return text, nil, serr
	}
	if previewBytes <= 0 {
		previewBytes = 1024
	}
	p := preview(text, previewBytes)
	return p, &r, nil
}

// preview uses previewBytes head bytes, previewBytes/4 tail bytes, and an omission marker.
func preview(text string, head int) string {
	b := []byte(text)
	tail := head / 4
	if len(b) <= head+tail {
		return text
	}
	tailText := string(b[len(b)-tail:])
	headText := string(b[:head])
	return fmt.Sprintf("%s\n...\n[truncated %d bytes; see spill ref for the full %d bytes]\n%s",
		headText, len(b)-head-tail, len(b), tailText)
}

// Package workspace implements the workspace registry (S14), matching core/workspace.
// Records contain ID, root, session group, and resume-on-open session. storage.Domain
// persists them under workspace IDs with CAS writes. SHA-256 of the normalized root
// produces deterministic IDs, so repeated creation for the same root is idempotent.
// ResumeOnOpen selects the session opened by default next time.
package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/storage"
)

// Types.

// Workspace records workspace information.
type Workspace struct {
	ID           brand.WorkspaceID `json:"id"`
	Root         string            `json:"root"`                   // Normalized absolute path.
	SessionGroup string            `json:"sessionGroup,omitempty"` // Optional session-group identifier.
	ResumeOnOpen *brand.SessionID  `json:"resumeOnOpen,omitempty"` // Session to open by default next time; nil disables resumption.
}

// ============================================================================
// Registry
// ============================================================================

// Registry uses storage.Domain; its backend provides concurrency safety.
type Registry struct {
	domain *storage.Domain[Workspace]
}

// New creates a workspace registry over a KV backend.
func New(backend storage.Backend) *Registry {
	return &Registry{domain: storage.NewDomain[Workspace](backend, "workspace/")}
}

// normalize trims the root and converts it to a cleaned absolute path.
func normalize(root string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", fmt.Errorf("workspace: empty root path")
	}
	// Resolve against CWD and clean redundant separators.
	abs, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", fmt.Errorf("workspace: resolve root %q: %w", root, err)
	}
	return abs, nil
}

// idFromRoot deterministically maps normalized roots to workspace IDs.
func idFromRoot(normalizedRoot string) brand.WorkspaceID {
	sum := sha256.Sum256([]byte(normalizedRoot))
	return brand.NewWorkspaceID("ws_" + hex.EncodeToString(sum[:12]))
}

// Create registers or reuses a workspace, returning the same ID for the same
// root along with its CAS version and whether it was newly created.
func (r *Registry) Create(ctx context.Context, root string) (brand.WorkspaceID, uint64, error) {
	normalized, err := normalize(root)
	if err != nil {
		return brand.WorkspaceID{}, 0, err
	}
	id := idFromRoot(normalized)

	// Reuse existing records idempotently.
	if ws, ver, gerr := r.domain.Get(ctx, id.Raw()); gerr == nil {
		_ = ws
		return id, ver, nil
	} else if !storage.IsKeyNotFound(gerr) {
		return brand.WorkspaceID{}, 0, gerr
	}

	// Create missing records with CAS.
	rec := Workspace{ID: id, Root: normalized}
	ver, err := r.domain.Put(ctx, id.Raw(), rec, 0)
	if err != nil {
		return brand.WorkspaceID{}, 0, fmt.Errorf("workspace: create %q: %w", normalized, err)
	}
	return id, ver, nil
}

// Get reads by ID, wrapping ErrKeyNotFound when absent.
func (r *Registry) Get(ctx context.Context, id brand.WorkspaceID) (Workspace, uint64, error) {
	if id.IsZero() {
		return Workspace{}, 0, fmt.Errorf("workspace: missing id")
	}
	return r.domain.Get(ctx, id.Raw())
}

// List returns all workspaces in deterministic lexical ID order.
func (r *Registry) List(ctx context.Context) ([]Workspace, error) {
	keys, err := r.domain.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []Workspace
	for _, k := range keys {
		ws, _, err := r.domain.Get(ctx, k)
		if err != nil {
			return nil, err
		}
		out = append(out, ws)
	}
	return out, nil
}

// SetSessionGroup replaces the current session group.
func (r *Registry) SetSessionGroup(ctx context.Context, id brand.WorkspaceID, group string) (uint64, error) {
	return r.update(ctx, id, func(ws *Workspace) {
		ws.SessionGroup = group
	})
}

// SetResumeOnOpen records the session opened by default for this workspace.
func (r *Registry) SetResumeOnOpen(ctx context.Context, id brand.WorkspaceID, sid brand.SessionID) (uint64, error) {
	return r.update(ctx, id, func(ws *Workspace) {
		cp := sid
		ws.ResumeOnOpen = &cp
	})
}

// ClearResumeOnOpen clears the default resumed session.
func (r *Registry) ClearResumeOnOpen(ctx context.Context, id brand.WorkspaceID) (uint64, error) {
	return r.update(ctx, id, func(ws *Workspace) {
		ws.ResumeOnOpen = nil
	})
}

// update reads, modifies, and CAS-writes using the observed version; conflicts fail.
func (r *Registry) update(ctx context.Context, id brand.WorkspaceID, mutate func(*Workspace)) (uint64, error) {
	if id.IsZero() {
		return 0, fmt.Errorf("workspace: missing id")
	}
	ws, ver, err := r.domain.Get(ctx, id.Raw())
	if err != nil {
		return 0, err
	}
	mutate(&ws)
	return r.domain.Put(ctx, id.Raw(), ws, ver)
}

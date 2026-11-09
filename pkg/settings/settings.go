// Package settings implements ctx.settings with layered scopes, path operations,
// CAS revisions, and secret redaction, matching settings/settings and settings-file.
// Host defaults are overridden by nearer session values. Set/unset operations use
// expectedRevision for optimistic concurrency. Redacted descriptions reveal only
// set/unset status for secret paths, never plaintext values in model output or logs.
package settings

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/herlanai/omnix/pkg/scope"
)

// Path is a dot-separated setting path, such as sandbox.mode.
type Path string

// splitPath splits a dot-separated path into segments.
func splitPath(p Path) []string {
	if p == "" {
		return nil
	}
	return strings.Split(string(p), ".")
}

// ============================================================================
// SettingsNamespace
// ============================================================================

// SettingsNamespace defines a namespace and its secret paths.
type SettingsNamespace struct {
	// Name identifies the namespace.
	Name string
	// SecretPaths identifies paths redacted in descriptions.
	SecretPaths map[Path]struct{}
}

// NewNamespace creates a settings namespace.
func NewNamespace(name string) *SettingsNamespace {
	return &SettingsNamespace{Name: name, SecretPaths: map[Path]struct{}{}}
}

// MarkSecret marks a path as secret.
func (ns *SettingsNamespace) MarkSecret(p Path) {
	ns.SecretPaths[p] = struct{}{}
}

// IsSecret reports whether a path is secret.
func (ns *SettingsNamespace) IsSecret(p Path) bool {
	_, ok := ns.SecretPaths[p]
	return ok
}

// ============================================================================
// SettingsScope
// ============================================================================

// PathOp describes one setting-path operation.
type PathOp struct {
	// Set supplies a value when nonnil; nil means unset.
	Set *json.RawMessage `json:"set,omitempty"`
}

// NewSetOp constructs a set operation.
func NewSetOp(v any) PathOp {
	raw, _ := json.Marshal(v)
	rm := json.RawMessage(raw)
	return PathOp{Set: &rm}
}

// NewUnsetOp constructs an unset operation.
func NewUnsetOp() PathOp {
	return PathOp{}
}

// SettingsScope provides a host-and-session layered settings view.
type SettingsScope struct {
	mu        sync.Mutex
	ns        *SettingsNamespace
	revision  uint64
	hostLayer *scope.Layer[string]
	sessLayer *scope.Layer[string]
}

// NewSettingsScope creates settings with the host layer first.
func NewSettingsScope(ns *SettingsNamespace) *SettingsScope {
	return &SettingsScope{
		ns:        ns,
		hostLayer: scope.NewLayer[string](scope.Key("host")),
		sessLayer: scope.NewLayer[string](scope.Key("session")),
	}
}

// Revision returns the revision incremented by update or replace.
func (s *SettingsScope) Revision() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.revision
}

// ApplyHost sets host defaults; expectedRevision=0 means initialization or force.
func (s *SettingsScope) ApplyHost(path Path, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	s.hostLayer.Register(string(path), string(raw), 0)
	return nil
}

// Get resolves a path nearest-first and returns raw JSON bytes.
func (s *SettingsScope) Get(path Path) (json.RawMessage, bool) {
	scoped := scope.NewScopedLayers[string]().Push(s.hostLayer).Push(s.sessLayer)
	if v, ok := scoped.Get(string(path)); ok {
		return json.RawMessage(v), true
	}
	return nil, false
}

// Update applies operations to the session layer using CAS. expectedRevision
// must match Revision or ErrRevisionMismatch is returned. Success increments the
// revision and session values override host defaults.
func (s *SettingsScope) Update(expectedRevision uint64, ops map[Path]PathOp) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.revision != expectedRevision {
		return &ErrRevisionMismatch{Expected: expectedRevision, Actual: s.revision}
	}
	for path, op := range ops {
		if op.Set == nil {
			// Unset removes the session value, falling back to the host default.
			s.sessLayer.Unregister(string(path))
		} else {
			s.sessLayer.Register(string(path), string(*op.Set), 0)
		}
	}
	s.revision++
	return nil
}

// Replace replaces all session settings using CAS.
func (s *SettingsScope) Replace(expectedRevision uint64, values map[Path]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.revision != expectedRevision {
		return &ErrRevisionMismatch{Expected: expectedRevision, Actual: s.revision}
	}
	// Rebuild the session layer.
	ns := scope.NewLayer[string](scope.Key("session"))
	for path, v := range values {
		raw, err := json.Marshal(v)
		if err != nil {
			return err
		}
		ns.Register(string(path), string(raw), 0)
	}
	s.sessLayer = ns
	s.revision++
	return nil
}

// ErrRevisionMismatch indicates a CAS revision conflict.
type ErrRevisionMismatch struct {
	Expected uint64
	Actual   uint64
}

// Error implements error.
func (e *ErrRevisionMismatch) Error() string {
	return fmt.Sprintf("settings: revision mismatch: expected %d, actual %d", e.Expected, e.Actual)
}

// IsRevisionMismatch identifies revision-conflict errors.
func IsRevisionMismatch(err error) bool {
	_, ok := err.(*ErrRevisionMismatch)
	return ok
}

// DescribedSetting is one described setting.
type DescribedSetting struct {
	Path   Path   `json:"path"`
	Op     string `json:"op"` // "set" / "unset" / "default"
	Value  any    `json:"value,omitempty"`
	Secret bool   `json:"secret,omitempty"`
}

// Describe reports current settings; redacted secrets expose operation state only.
func (s *SettingsScope) Describe(redactSecrets bool) []DescribedSetting {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Merge host and session paths.
	scoped := scope.NewScopedLayers[string]().Push(s.hostLayer).Push(s.sessLayer)
	merged := scoped.Merge()
	pathSet := map[Path]struct{}{}
	for _, e := range merged {
		if e.Name != "" {
			pathSet[Path(e.Name)] = struct{}{}
		}
	}
	paths := make([]Path, 0, len(pathSet))
	for p := range pathSet {
		paths = append(paths, p)
	}
	sort.Slice(paths, func(i, j int) bool { return paths[i] < paths[j] })

	var out []DescribedSetting
	for _, p := range paths {
		secret := s.ns.IsSecret(p)
		// Determine the source layer, preferring session.
		sessSet := s.sessLayer.Has(string(p))
		hostSet := s.hostLayer.Has(string(p))
		op := "unset"
		if sessSet {
			op = "set"
		} else if hostSet {
			op = "default"
		}
		ds := DescribedSetting{Path: p, Op: op, Secret: secret}
		if redactSecrets && secret {
			// Redact the value to nil while retaining operation state.
		} else if v, ok := s.Get(p); ok {
			var anyV any
			_ = json.Unmarshal(v, &anyV)
			ds.Value = anyV
		}
		out = append(out, ds)
	}
	return out
}

// Package credentials implements credentials and authorization flows, matching
// credentials/credentials, credentials-local, and authorization upstream.
// Resolve reads a branded POSIX variable reference before each LLM request, so
// environment changes become visible on the next turn. Storage Domain persists
// records with CAS versions and rejects concurrent write conflicts. Describe
// exposes only presence and writability, never plaintext. Registered authorization
// flows support list/begin/cancel; S06 adds the complete stub lifecycle.
package credentials

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/storage"
)

// Credential storage.

// envRecord is the persisted credential record.
type envRecord struct {
	// Values maps credential references to values.
	Values map[string]string `json:"values"`
}

// Store combines cached credentials with Storage Domain CAS persistence.
type Store struct {
	mu       sync.Mutex
	domain   *storage.Domain[envRecord]
	writable map[string]bool // Track writable references.
}

// NewStore creates a store using backend for CAS persistence.
func NewStore(backend storage.Backend) *Store {
	return &Store{
		domain:   storage.NewDomain[envRecord](backend, "credentials"),
		writable: map[string]bool{},
	}
}

// NewMemoryStore creates an in-memory store for tests or temporary use.
func NewMemoryStore() *Store {
	return NewStore(storage.NewMemoryKV())
}

// load reads the current record, returning an empty record when absent.
func (s *Store) load() (envRecord, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, version, err := s.domain.Get(context.Background(), "env")
	if err != nil {
		if storage.IsKeyNotFound(err) {
			return envRecord{Values: map[string]string{}}, 0
		}
		return envRecord{Values: map[string]string{}}, 0
	}
	if rec.Values == nil {
		rec.Values = map[string]string{}
	}
	return rec, version
}

// Resolve reads the current credential value once before each LLM request.
func (s *Store) Resolve(ctx context.Context, ref brand.CredentialRef) (string, bool) {
	rec, _ := s.load()
	v, ok := rec.Values[ref.Raw()]
	return v, ok
}

// Set writes a credential using the last loaded CAS version; mismatches fail.
func (s *Store) Set(ctx context.Context, ref brand.CredentialRef, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, version, err := s.domain.Get(ctx, "env")
	if err != nil && !storage.IsKeyNotFound(err) {
		return err
	}
	if rec.Values == nil {
		rec.Values = map[string]string{}
	}
	rec.Values[ref.Raw()] = value
	if _, err := s.domain.Put(ctx, "env", rec, version); err != nil {
		return err
	}
	s.writable[ref.Raw()] = true
	return nil
}

// Unset deletes a credential using CAS.
func (s *Store) Unset(ctx context.Context, ref brand.CredentialRef) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, version, err := s.domain.Get(ctx, "env")
	if err != nil && !storage.IsKeyNotFound(err) {
		return err
	}
	if rec.Values == nil {
		return nil
	}
	delete(rec.Values, ref.Raw())
	delete(s.writable, ref.Raw())
	_, err = s.domain.Put(ctx, "env", rec, version)
	return err
}

// CredentialInfo describes a credential without plaintext.
type CredentialInfo struct {
	Ref      brand.CredentialRef `json:"ref"`
	Writable bool                `json:"writable"`
	HasValue bool                `json:"hasValue"`
}

// Describe exposes references, writability, and presence, never plaintext.
func (s *Store) Describe(ctx context.Context) []CredentialInfo {
	rec, _ := s.load()
	refs := make([]string, 0, len(rec.Values))
	for k := range rec.Values {
		refs = append(refs, k)
	}
	sort.Strings(refs)
	out := make([]CredentialInfo, 0, len(refs))
	for _, k := range refs {
		ref := brand.NewCredentialRef(k)
		_, has := rec.Values[k]
		out = append(out, CredentialInfo{Ref: ref, Writable: s.writable[k], HasValue: has})
	}
	return out
}

// ============================================================================
// AuthorizationFlow & AuthorizationService
// ============================================================================

// FlowID is a branded authorization-flow identifier.
type FlowID = brand.CredentialRef

// Flow describes an authorization flow, such as OAuth authorization code.
type Flow struct {
	// ID uniquely identifies the flow.
	ID FlowID
	// Name is human-readable.
	Name string
	// Begin returns a prompt or URL for completing authorization; stubs may use a placeholder.
	Begin func(ctx context.Context) (string, error)
	// Cancel cancels the authorization flow.
	Cancel func(ctx context.Context) error
}

// AuthorizationService registers authorization flows.
type AuthorizationService struct {
	mu    sync.RWMutex
	flows map[string]*Flow
}

// NewAuthorizationService creates an authorization service.
func NewAuthorizationService() *AuthorizationService {
	return &AuthorizationService{flows: map[string]*Flow{}}
}

// Register registers an authorization flow.
func (a *AuthorizationService) Register(f *Flow) error {
	if f == nil || f.ID.Raw() == "" {
		return fmt.Errorf("credentials: flow with empty id cannot register")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.flows[f.ID.Raw()] = f
	return nil
}

// List returns registered authorization flows.
func (a *AuthorizationService) List() []*Flow {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]*Flow, 0, len(a.flows))
	for _, f := range a.flows {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID.Raw() < out[j].ID.Raw() })
	return out
}

// Begin starts the specified authorization flow.
func (a *AuthorizationService) Begin(ctx context.Context, id FlowID) (string, error) {
	a.mu.RLock()
	f, ok := a.flows[id.Raw()]
	a.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("credentials: flow %q not found", id.Raw())
	}
	if f.Begin == nil {
		return "", fmt.Errorf("credentials: flow %q has no begin handler", id.Raw())
	}
	return f.Begin(ctx)
}

// Cancel cancels the specified authorization flow.
func (a *AuthorizationService) Cancel(ctx context.Context, id FlowID) error {
	a.mu.RLock()
	f, ok := a.flows[id.Raw()]
	a.mu.RUnlock()
	if !ok {
		return fmt.Errorf("credentials: flow %q not found", id.Raw())
	}
	if f.Cancel == nil {
		return nil
	}
	return f.Cancel(ctx)
}

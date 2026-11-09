// Authorization lifecycle stub (S06), matching credentials/authorization.
// M39 supplies flow registration and Begin/Cancel. AuthService adds one-time
// callback tokens: Begin creates a pending session; Complete stores credentials
// and marks it completed so Resolve can read them; Cancel cancels pending sessions
// and invokes flow.Cancel. A real browser/SDK integration can supply a callback
// endpoint while retaining this interface.
package credentials

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/herlanai/omnix/pkg/brand"
)

// Session state.

// AuthStatus identifies an authorization session state.
type AuthStatus string

// Authorization session states.
const (
	AuthPending   AuthStatus = "pending"   // Waiting for a callback.
	AuthCompleted AuthStatus = "completed" // Credentials have been stored.
	AuthCancelled AuthStatus = "cancelled" // Canceled.
)

// AuthSession records an authorization flow session.
type AuthSession struct {
	ID        string              `json:"id"`
	FlowID    FlowID              `json:"flowId"`
	Status    AuthStatus          `json:"status"`
	StartedAt time.Time           `json:"startedAt"`
	CredRef   brand.CredentialRef `json:"credRef,omitempty"` // Credential reference written by the callback.
	HasValue  bool                `json:"hasValue"`
}

// AuthService: OAuth flow stub.

// AuthService orchestrates begin, callback, and credential resolution.
type AuthService struct {
	auth     *AuthorizationService
	store    *Store
	mu       sync.Mutex
	sessions map[string]*AuthSession // Session ID to state.
	next     int
}

// NewAuthService binds the flow registry and credential store.
func NewAuthService(auth *AuthorizationService, store *Store) *AuthService {
	return &AuthService{
		auth:     auth,
		store:    store,
		sessions: map[string]*AuthSession{},
	}
}

// newSessionID generates an incrementing session ID.
func (s *AuthService) newSessionID() string {
	s.next++
	return fmt.Sprintf("auth-%04d", s.next)
}

// Begin starts a flow and returns a one-time callback token.
// Typical flow: list flows, begin, complete externally, then Complete(token, value).
func (s *AuthService) Begin(ctx context.Context, flowID FlowID) (token string, err error) {
	// Start the flow to obtain a prompt or URL; failure aborts the whole operation.
	hint, err := s.auth.Begin(ctx, flowID)
	if err != nil {
		return "", err
	}
	_ = hint

	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.newSessionID()
	sess := &AuthSession{ID: id, FlowID: flowID, Status: AuthPending, StartedAt: time.Now()}
	s.sessions[id] = sess
	return id, nil
}

// Complete simulates an OAuth callback: store value and mark the session completed.
// Resolve can then retrieve it using the flow's credential reference.
func (s *AuthService) Complete(ctx context.Context, token string, value string) error {
	s.mu.Lock()
	sess, ok := s.sessions[token]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("credentials: auth session %q not found", token)
	}
	if sess.Status != AuthPending {
		s.mu.Unlock()
		return fmt.Errorf("credentials: auth session %q already %s", token, sess.Status)
	}
	// By convention, the credential reference is the flow ID.
	ref := sess.FlowID
	s.mu.Unlock()

	// Store credentials using CAS, then mark completion on success.
	if err := s.store.Set(ctx, ref, value); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess.Status = AuthCompleted
	sess.CredRef = ref
	sess.HasValue = true
	return nil
}

// Cancel cancels a pending session and calls flow.Cancel.
func (s *AuthService) Cancel(ctx context.Context, token string) error {
	s.mu.Lock()
	sess, ok := s.sessions[token]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("credentials: auth session %q not found", token)
	}
	if sess.Status != AuthPending {
		s.mu.Unlock()
		return fmt.Errorf("credentials: auth session %q already %s", token, sess.Status)
	}
	flowID := sess.FlowID
	sess.Status = AuthCancelled
	s.mu.Unlock()

	// Invoke the underlying flow cancellation, which may be a no-op for stubs.
	return s.auth.Cancel(ctx, flowID)
}

// Status returns the state of a session.
func (s *AuthService) Status(token string) (AuthSession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[token]
	if !ok {
		return AuthSession{}, false
	}
	cp := *sess
	return cp, true
}

// List returns all sessions deterministically in lexical ID order.
func (s *AuthService) List() []AuthSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AuthSession, 0, len(s.sessions))
	for _, v := range s.sessions {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

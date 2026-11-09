// SessionHeader metadata follows upstream session (M06). Static fields include
// format version, ID, creation time, cwd, fork parent, seed length, origin,
// and agent preset. They define persistence identity, lineage, and subagent depth.
package session

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/herlanai/omnix/pkg/brand"
)

// SessionFormatVersion identifies the current on-disk format. Incompatible
// header/event changes must increment it and reject other versions.
const SessionFormatVersion = 1

// HeaderOrigin identifies how a session originated.
type HeaderOrigin string

// Session origins.
const (
	OriginCreated HeaderOrigin = "created" // Newly created session.
	OriginFork    HeaderOrigin = "fork"    // Forked from a parent session.
	OriginResume  HeaderOrigin = "resume"  // Resumed from persisted state.
)

// SessionHeader contains static session metadata.
type SessionHeader struct {
	// Version is written on serialization and checked fail-closed on loading.
	Version int `json:"version"`
	// ID uniquely identifies the session.
	ID brand.SessionID `json:"id"`
	// CreatedAt records session creation time.
	CreatedAt time.Time `json:"createdAt"`
	// Cwd is the working directory used by filesystem tools and relative paths.
	Cwd string `json:"cwd,omitempty"`
	// ParentSession records the fork source; empty means no parent.
	ParentSession brand.SessionID `json:"parentSession,omitempty"`
	// SeedLength counts stored events replayed during Resume/Fork and locates
	// the boundary between seed history and live work.
	SeedLength uint64 `json:"seedLength,omitempty"`
	// Origin is created, fork, or resume.
	Origin HeaderOrigin `json:"origin,omitempty"`
	// DelegationDepth counts recursive subagent levels, starting at zero.
	DelegationDepth int `json:"delegationDepth,omitempty"`
	// AgentPreset names the bound agent preset.
	AgentPreset string `json:"agentPreset,omitempty"`
}

// NewSessionHeader creates a current-version header with created origin and zero depth.
func NewSessionHeader(id brand.SessionID, cwd string) *SessionHeader {
	return &SessionHeader{
		Version:         SessionFormatVersion,
		ID:              id,
		CreatedAt:       time.Now().UTC(),
		Cwd:             cwd,
		Origin:          OriginCreated,
		DelegationDepth: 0,
	}
}

// Marshal serializes the header as JSON.
func (h *SessionHeader) Marshal() ([]byte, error) {
	return json.Marshal(h)
}

// UnmarshalSessionHeader decodes the header and rejects unsupported versions
// to avoid interpreting data with incompatible format rules.
func UnmarshalSessionHeader(data []byte) (*SessionHeader, error) {
	var h SessionHeader
	if err := json.Unmarshal(data, &h); err != nil {
		return nil, fmt.Errorf("session header: unmarshal: %w", err)
	}
	if h.Version != SessionFormatVersion {
		return nil, &SessionFormatUnsupportedError{
			Version: h.Version,
		}
	}
	return &h, nil
}

// Validate checks header completeness after loading.
func (h *SessionHeader) Validate() error {
	if h.Version != SessionFormatVersion {
		return &SessionFormatUnsupportedError{Version: h.Version}
	}
	if h.ID.IsZero() {
		return fmt.Errorf("session header: missing session id")
	}
	if h.CreatedAt.IsZero() {
		return fmt.Errorf("session header: missing created-at")
	}
	return nil
}

// Fork derives a child header with this ID as ParentSession, fork origin,
// and depth increased by one. The caller sets SeedLength after replaying seeds.
func (h *SessionHeader) Fork(childID brand.SessionID) *SessionHeader {
	return &SessionHeader{
		Version:         SessionFormatVersion,
		ID:              childID,
		CreatedAt:       time.Now().UTC(),
		Cwd:             h.Cwd,
		ParentSession:   h.ID,
		SeedLength:      0, // A forked child starts with no live work.
		Origin:          OriginFork,
		DelegationDepth: h.DelegationDepth + 1,
		AgentPreset:     h.AgentPreset,
	}
}

// SessionFormatUnsupportedError indicates a rejected session format version.
type SessionFormatUnsupportedError struct {
	Version int
}

// Error implements the error interface.
func (e *SessionFormatUnsupportedError) Error() string {
	return fmt.Sprintf("session header: unsupported format version %d (want %d)", e.Version, SessionFormatVersion)
}

// AsHeaderOrigin converts an origin string for tests and external construction.
func AsHeaderOrigin(s string) HeaderOrigin {
	return HeaderOrigin(s)
}

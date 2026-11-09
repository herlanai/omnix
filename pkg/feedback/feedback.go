// Package feedback implements message feedback using Storage Domain sidecars
// and stable failure classes, matching feedback/feedback (S09). Each session/message
// pair has a rating, note, CAS version, and timestamps. List, put, and delete use
// M45 storage.Domain; failures are VERSION_CONFLICT, SESSION_NOT_FOUND, or NOT_FOUND.
package feedback

import (
	"context"
	"fmt"
	"time"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/storage"
)

// Data model.

// Rating is a feedback score.
type Rating int

// Rating values.
const (
	RatingThumbsDown Rating = -1
	RatingNone       Rating = 0
	RatingThumbsUp   Rating = 1
)

// Feedback records feedback on a message.
type Feedback struct {
	// SessionID identifies the owning session.
	SessionID brand.SessionID `json:"sessionId"`
	// MessageID identifies the message.
	MessageID string `json:"messageId"`
	// Rating is the score (-1, 0, or 1).
	Rating Rating `json:"rating"`
	// Note contains feedback text.
	Note string `json:"note,omitempty"`
	// Version is the CAS version, incremented on writes.
	Version uint64 `json:"version"`
	// CreatedAt is the creation time.
	CreatedAt time.Time `json:"createdAt"`
	// UpdatedAt is the last update time.
	UpdatedAt time.Time `json:"updatedAt"`
}

// Stable failure classifications.

// FailCode is a stable failure code.
type FailCode string

const (
	CodeVersionConflict FailCode = "VERSION_CONFLICT"
	CodeSessionNotFound FailCode = "SESSION_NOT_FOUND"
	CodeNotFound        FailCode = "NOT_FOUND"
)

// FailError carries a stable feedback failure code.
type FailError struct {
	Code FailCode
	Msg  string
}

func (e *FailError) Error() string { return string(e.Code) + ": " + e.Msg }

func fail(code FailCode, format string, args ...any) error {
	return &FailError{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// CodeOf extracts the stable failure code.
func CodeOf(err error) FailCode {
	if err == nil {
		return ""
	}
	if fe, ok := err.(*FailError); ok {
		return fe.Code
	}
	return ""
}

// ============================================================================
// Store
// ============================================================================

// Store persists message feedback.
type Store struct {
	domain *storage.Domain[Feedback]
}

// New creates a feedback store over an M45 storage backend.
func New(backend storage.Backend) *Store {
	return &Store{domain: storage.NewDomain[Feedback](backend, "feedback")}
}

// keyOf constructs a storage key for a session/message pair.
func keyOf(session brand.SessionID, messageID string) string {
	if session.IsZero() {
		return "_unowned_" + messageID
	}
	return session.Raw() + "::" + messageID
}

// Put creates or updates feedback and returns the new version. When supplied,
// expectedVersion is checked with CAS; a mismatch returns VERSION_CONFLICT.
func (s *Store) Put(ctx context.Context, session brand.SessionID, messageID string, rating Rating, note string, expectedVersion *uint64) (uint64, error) {
	if err := ValidateRating(rating); err != nil {
		return 0, err
	}
	if err := ValidateNote(note); err != nil {
		return 0, err
	}
	if session.IsZero() {
		return 0, fail(CodeSessionNotFound, "missing session id")
	}
	key := keyOf(session, messageID)
	now := time.Now()

	// Read the existing record to distinguish creation from update.
	existing, ver, err := s.domain.Get(ctx, key)
	var expected uint64
	created := false
	if err != nil {
		if !isNotFound(err) {
			return 0, err
		}
		// Create the missing record.
		created = true
		expected = 0
	} else {
		expected = ver
		_ = existing
	}

	// Validate the caller's expected CAS version when supplied.
	if expectedVersion != nil {
		if created {
			if *expectedVersion != 0 {
				return 0, fail(CodeVersionConflict, "feedback for message %q does not exist (expected v%d)", messageID, *expectedVersion)
			}
		} else if *expectedVersion != expected {
			return 0, fail(CodeVersionConflict, "feedback for message %q version mismatch (expected %d, now %d)", messageID, *expectedVersion, expected)
		}
	}

	fb := Feedback{
		SessionID: session,
		MessageID: messageID,
		Rating:    rating,
		Note:      note,
		Version:   expected + 1,
		CreatedAt: existing.CreatedAt,
		UpdatedAt: now,
	}
	if created {
		fb.CreatedAt = now
	}
	newVer, err := s.domain.Put(ctx, key, fb, expected)
	if err != nil {
		// Classify Domain CAS failures as VERSION_CONFLICT too.
		return 0, fail(CodeVersionConflict, "cas put failed: %v", err)
	}
	return newVer, nil
}

// Get reads feedback or returns NOT_FOUND.
func (s *Store) Get(ctx context.Context, session brand.SessionID, messageID string) (*Feedback, error) {
	fb, _, err := s.domain.Get(ctx, keyOf(session, messageID))
	if err != nil {
		if isNotFound(err) {
			return nil, fail(CodeNotFound, "feedback for message %q not found", messageID)
		}
		return nil, err
	}
	return &fb, nil
}

// Delete removes a feedback record.
func (s *Store) Delete(ctx context.Context, session brand.SessionID, messageID string) error {
	return s.domain.Delete(ctx, keyOf(session, messageID))
}

// List returns session feedback ordered by key.
func (s *Store) List(ctx context.Context, session brand.SessionID) ([]Feedback, error) {
	keys, err := s.domain.List(ctx)
	if err != nil {
		return nil, err
	}
	prefix := session.Raw() + "::"
	var out []Feedback
	for _, k := range keys {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			fb, _, err := s.domain.Get(ctx, k)
			if err == nil {
				out = append(out, fb)
			}
		}
	}
	return out, nil
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	_, ok := err.(*storage.ErrKeyNotFound)
	return ok
}

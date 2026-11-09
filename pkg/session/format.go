// This file validates session formats and event types (M44), following
// upstream session versioning. LoadSession validates the header version before
// checking every event against the known-type set. Unknown versions or types
// fail closed to avoid interpreting data with incompatible parsing rules.
package session

import (
	"encoding/json"
	"fmt"
)

// KNOWN_SESSION_EVENT_TYPES lists supported types in lexical order for validation.
var KNOWN_SESSION_EVENT_TYPES = func() map[EventType]struct{} {
	m := map[EventType]struct{}{}
	for _, t := range AllEventTypes {
		m[t] = struct{}{}
	}
	return m
}()

// CorruptEventError indicates an unknown type caused by corrupt or incompatible data.
type CorruptEventError struct {
	Seq  uint64
	Type EventType
}

// Error implements the error interface.
func (e *CorruptEventError) Error() string {
	return fmt.Sprintf("session: corrupt event seq %d with unknown type %q", e.Seq, e.Type)
}

// IsCorruptEventError reports whether an error indicates a corrupt event.
func IsCorruptEventError(err error) bool {
	_, ok := err.(*CorruptEventError)
	return ok
}

// LoadSession validates the header against SessionFormatVersion, checks each
// event type against KNOWN_SESSION_EVENT_TYPES, and reconstructs events through
// full deserialization. Incompatible versions are rejected.
func LoadSession(headerBytes []byte, eventLines ...[]byte) (*SessionHeader, []SessionEvent, error) {
	header, err := UnmarshalSessionHeader(headerBytes)
	if err != nil {
		return nil, nil, err
	}
	if err := header.Validate(); err != nil {
		return nil, nil, err
	}

	events := make([]SessionEvent, 0, len(eventLines))
	for _, line := range eventLines {
		// Parse the sequence/type envelope first and validate against the allowlist.
		var probe struct {
			Seq  uint64    `json:"seq"`
			Type EventType `json:"type"`
		}
		if err := json.Unmarshal(line, &probe); err != nil {
			return nil, nil, err
		}
		if _, known := KNOWN_SESSION_EVENT_TYPES[probe.Type]; !known {
			return nil, nil, &CorruptEventError{Seq: probe.Seq, Type: probe.Type}
		}
		// Then deserialize the complete event.
		var ev SessionEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			return nil, nil, err
		}
		events = append(events, ev)
	}
	return header, events, nil
}

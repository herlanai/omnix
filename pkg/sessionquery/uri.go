// Session-reference URIs match context/session-reference: opaque session IDs
// round-trip through canonical omnix-session URIs and Markdown mentions.
package sessionquery

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

// Scheme is reserved for session snapshot references.
const Scheme = "omnix-session:"

var enc = base64.RawURLEncoding

var payloadShape = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// ErrInvalidURI indicates an invalid or noncanonical session-reference URI.
var ErrInvalidURI = errors.New("sessionquery: invalid session reference URI")

// EncodeURI losslessly encodes a session ID as a canonical URI.
func EncodeURI(sessionID string) string {
	raw, _ := json.Marshal(sessionID)
	return Scheme + enc.EncodeToString(raw)
}

// DecodeURI validates canonical form and decodes, or returns ErrInvalidURI.
func DecodeURI(uri string) (string, error) {
	if !strings.HasPrefix(uri, Scheme) {
		return "", ErrInvalidURI
	}
	payload := uri[len(Scheme):]
	if !payloadShape.MatchString(payload) {
		return "", ErrInvalidURI
	}
	raw, err := enc.DecodeString(payload)
	if err != nil {
		return "", ErrInvalidURI
	}
	var id string
	if err := json.Unmarshal(raw, &id); err != nil {
		return "", ErrInvalidURI
	}
	if EncodeURI(id) != uri {
		return "", ErrInvalidURI
	}
	return id, nil
}

var escapeLabel = regexp.MustCompile(`[\\\]]`)

// FormatMention renders a Markdown mention with a canonical URI; empty labels use the ID.
func FormatMention(sessionID, label string) string {
	if label == "" {
		label = sessionID
	}
	label = escapeLabel.ReplaceAllStringFunc(label, func(m string) string { return `\` + m })
	return "@[" + label + "](" + EncodeURI(sessionID) + ")"
}

// Session references follow upstream session-reference and file-reference
// modules (M17). User input selects sessions with @session/<id> and workspace
// files with #<path/file>. Mentions are only selection syntax: the model sees
// an aggregated untrusted snapshot in additionalContext, matching
// PreparedReferencedMessage. Raw refs are recorded on user/message for
// auditing. Failures use stable codes; host protocols must not parse prompt text.
package session

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/herlanai/omnix/pkg/brand"
)

// Stable session-reference error codes.

// SessionReferenceErrorCode identifies a reference failure category.
type SessionReferenceErrorCode string

// Codes match upstream, with M17 additions for missing files and sessions.
const (
	// RefCodeInvalidConfig indicates missing or invalid resolver/workspace configuration.
	RefCodeInvalidConfig SessionReferenceErrorCode = "SESSION_REFERENCE_INVALID_CONFIG"
	// RefCodeInvalidReference indicates invalid syntax or target.
	RefCodeInvalidReference SessionReferenceErrorCode = "SESSION_REFERENCE_INVALID_REFERENCE"
	// RefCodeSelfReference indicates a reference to the requesting agent's own session.
	RefCodeSelfReference SessionReferenceErrorCode = "SESSION_REFERENCE_SELF_REFERENCE"
	// RefCodeTooMany indicates the reference count exceeds the limit.
	RefCodeTooMany SessionReferenceErrorCode = "SESSION_REFERENCE_TOO_MANY"
	// RefCodeReadFailed indicates failure reading a source session.
	RefCodeReadFailed SessionReferenceErrorCode = "SESSION_REFERENCE_READ_FAILED"
	// RefCodeBudgetExceeded indicates aggregated context exceeds its budget.
	RefCodeBudgetExceeded SessionReferenceErrorCode = "SESSION_REFERENCE_BUDGET_EXCEEDED"
	// RefCodeCancelled indicates canceled reference preparation.
	RefCodeCancelled SessionReferenceErrorCode = "SESSION_REFERENCE_CANCELLED"
	// RefCodeSessionNotFound indicates the referenced session is missing (M17).
	RefCodeSessionNotFound SessionReferenceErrorCode = "SESSION_NOT_FOUND"
	// RefCodeFileNotFound indicates the referenced file is missing (M17).
	RefCodeFileNotFound SessionReferenceErrorCode = "FILE_NOT_FOUND"
	// RefCodeFileOutOfWorkspace rejects files outside the workspace root.
	RefCodeFileOutOfWorkspace SessionReferenceErrorCode = "FILE_OUT_OF_WORKSPACE"
)

// SessionReferenceError carries a stable reference error code.
type SessionReferenceError struct {
	Code SessionReferenceErrorCode
	Msg  string
}

func (e *SessionReferenceError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Msg)
}

// newRefError constructs a SessionReferenceError.
func newRefError(code SessionReferenceErrorCode, format string, args ...any) error {
	return &SessionReferenceError{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// RefCodeFrom extracts a stable reference code, returning empty for other errors.
func RefCodeFrom(err error) SessionReferenceErrorCode {
	if err == nil {
		return ""
	}
	if re, ok := err.(*SessionReferenceError); ok {
		return re.Code
	}
	return ""
}

// Mention parsing.

// MentionKind identifies the reference type.
type MentionKind string

const (
	// MentionSession selects a session with @session/<id>.
	MentionSession MentionKind = "session"
	// MentionFile selects a file with #<path/file>.
	MentionFile MentionKind = "file"
)

// Mention identifies one reference in user input.
type Mention struct {
	// Kind is session or file.
	Kind MentionKind
	// Raw is the complete source token, such as @session/abc or #src/main.go.
	Raw string
	// Value is the target session ID or file path.
	Value string
	// Start and End delimit Raw in source bytes for stripping mentions from readable text.
	Start int
	End   int
}

// Session mentions permit alphanumeric IDs and underscore, dash, or dot separators.
var sessionMentionRe = regexp.MustCompile(`@session/([A-Za-z0-9_.\-]+)`)

// File mentions consume a contiguous non-whitespace, non-# path token.
// Trailing punctuation may require interpretation by the resolver.
var fileMentionRe = regexp.MustCompile(`#([^\s#]+)`)

// maxReferencesPerMessage bounds reference count and produces RefCodeTooMany.
const maxReferencesPerMessage = 16

// ParseMentions returns @session/<id> and #<path/file> references in source
// order. With no references it returns an empty, non-nil slice.
func ParseMentions(text string) []Mention {
	var out []Mention
	// Find session mentions.
	for _, loc := range sessionMentionRe.FindAllStringSubmatchIndex(text, -1) {
		raw := text[loc[0]:loc[1]]
		val := text[loc[2]:loc[3]]
		out = append(out, Mention{
			Kind:  MentionSession,
			Raw:   raw,
			Value: val,
			Start: loc[0],
			End:   loc[1],
		})
	}
	// Find file mentions.
	for _, loc := range fileMentionRe.FindAllStringSubmatchIndex(text, -1) {
		// Avoid ambiguous splits around session IDs. Trailing punctuation in a file
		// value is left unchanged for the caller to interpret rather than applying
		// complex disambiguation here.
		raw := text[loc[0]:loc[1]]
		val := text[loc[2]:loc[3]]
		out = append(out, Mention{
			Kind:  MentionFile,
			Raw:   raw,
			Value: val,
			Start: loc[0],
			End:   loc[1],
		})
	}
	// Merge the separate regex scans into stable source-position order.
	sortMentionsByPos(out)
	return out
}

// sortMentionsByPos sorts mentions stably by Start.
func sortMentionsByPos(m []Mention) {
	for i := 1; i < len(m); i++ {
		for j := i; j > 0 && m[j-1].Start > m[j].Start; j-- {
			m[j-1], m[j] = m[j], m[j-1]
		}
	}
}

// stripMentions removes mention tokens, leaving readable message content.
func stripMentions(text string, mentions []Mention) string {
	if len(mentions) == 0 {
		return text
	}
	var sb strings.Builder
	last := 0
	for _, m := range mentions {
		sb.WriteString(text[last:m.Start])
		last = m.End
	}
	sb.WriteString(text[last:])
	return sb.String()
}

// ============================================================================
// PreparedReferencedMessage
// ============================================================================

// SessionReferenceInput is a host-provided structured source-session selection.
type SessionReferenceInput struct {
	// SessionID is the authoritative source-session identity.
	SessionID brand.SessionID
	// Label is an optional display label included in the snapshot.
	Label string
}

// PreparedReferencedMessage combines readable text after mention removal
// with an optional aggregated additionalContext snapshot.
type PreparedReferencedMessage struct {
	// Content is readable message text with mention tokens removed.
	Content string
	// AdditionalContext contains aggregated untrusted reference context, or empty if absent.
	AdditionalContext string
	// References lists successfully resolved and included references in source order.
	References []ReferenceResolution
}

// ReferenceResolution records one resolved reference for Refs auditing.
type ReferenceResolution struct {
	Kind  MentionKind `json:"kind"`
	Raw   string      `json:"raw,omitempty"`
	Value string      `json:"value"`
	Label string      `json:"label,omitempty"`
	Slice string      `json:"slice,omitempty"` // Text fragment aggregated into additionalContext.
}

// Resolver interfaces.

// SessionReferenceResolver converts mention targets into context fragments.
// Session reads depend on persistence (M43), file reads on Filesystem (M35).
// Hosts and tests may inject real or stub implementations; the workspace file
// resolver provides existence and containment checks without reading contents.
type SessionReferenceResolver interface {
	// ResolveSession returns readable snapshot text, RefCodeSessionNotFound
	// for a missing session, or RefCodeReadFailed for read errors.
	ResolveSession(id brand.SessionID) (string, error)
	// ResolveFile returns a summary, RefCodeFileNotFound for missing files,
	// or RefCodeFileOutOfWorkspace for targets outside the root.
	ResolveFile(path string) (string, error)
	// WorkspaceRoot returns the agent's workspace boundary for file containment checks.
	WorkspaceRoot() string
}

// WorkspaceFileResolver checks file existence and workspace containment only.
// Like upstream path-only discovery, it does not read contents; content access
// belongs to the M35 Filesystem interface.
type WorkspaceFileResolver struct {
	Root string
}

// WorkspaceRoot returns the configured workspace root.
func (r *WorkspaceFileResolver) WorkspaceRoot() string { return r.Root }

// ResolveSession reports an invalid-hook error because this resolver has no session capability.
func (r *WorkspaceFileResolver) ResolveSession(brand.SessionID) (string, error) {
	return "", newRefError(RefCodeInvalidConfig, "workspace file resolver has no session capability")
}

// ResolveFile checks existence and returns a path-only summary; M35 handles content reads.
func (r *WorkspaceFileResolver) ResolveFile(path string) (string, error) {
	root, err := filepath.Abs(r.Root)
	if err != nil {
		return "", newRefError(RefCodeInvalidConfig, "bad workspace root %q: %v", r.Root, err)
	}
	// Resolve absolute paths relative to the workspace root when necessary.
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, abs)
	}
	abs, err = filepath.Abs(abs)
	if err != nil {
		return "", newRefError(RefCodeInvalidReference, "bad file path %q: %v", path, err)
	}
	// Require the target to be inside the workspace, excluding the root itself.
	if abs == root || !within(abs, root) {
		return "", newRefError(RefCodeFileOutOfWorkspace, "file %q outside workspace root %q", path, root)
	}
	// Check existence.
	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", newRefError(RefCodeFileNotFound, "file %q not found", path)
		}
		return "", newRefError(RefCodeReadFailed, "stat %q: %v", path, err)
	}
	kind := "file"
	if info.IsDir() {
		kind = "directory"
	}
	// Return only path, type, and size: stable, hashable metadata without content (D3).
	return fmt.Sprintf("file %q (%s, %d bytes)", path, kind, info.Size()), nil
}

// within reports whether abs is within root, including descendants.
func within(abs, root string) bool {
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Reference preparation.

// PrepareRequest describes reference preparation inputs.
type PrepareRequest struct {
	// AgentSessionID enables self-reference rejection; empty skips this check.
	AgentSessionID brand.SessionID
	// Text contains the original user message, possibly with mentions.
	Text string
	// Resolver resolves session and file references.
	Resolver SessionReferenceResolver
	// MaxContextBytes optionally limits aggregated context; nonpositive means unlimited.
	MaxContextBytes int
}

// Prepare parses mentions, resolves each reference, and builds the prepared
// message. No references preserves Content and leaves AdditionalContext empty.
// Self references, resolution failures, excess reference counts, and exceeded
// context budgets return their stable error codes immediately.
func Prepare(req PrepareRequest) (*PreparedReferencedMessage, error) {
	if req.Resolver == nil {
		return nil, newRefError(RefCodeInvalidConfig, "missing reference resolver")
	}
	mentions := ParseMentions(req.Text)
	if len(mentions) > maxReferencesPerMessage {
		return nil, newRefError(RefCodeTooMany, "%d references exceed limit %d", len(mentions), maxReferencesPerMessage)
	}

	// Strip tokens to recover readable text. File tokens may include trailing
	// punctuation; resolution determines validity rather than this stripping step.
	content := stripMentions(req.Text, mentions)

	if len(mentions) == 0 {
		return &PreparedReferencedMessage{Content: content}, nil
	}

	// Resolve references individually and assemble the aggregated snapshot.
	var sb strings.Builder
	var res []ReferenceResolution
	var total int
	for _, m := range mentions {
		var slice string
		var err error
		switch m.Kind {
		case MentionSession:
			// Reject self references.
			if !req.AgentSessionID.IsZero() && m.Value == req.AgentSessionID.Raw() {
				return nil, newRefError(RefCodeSelfReference, "self reference to %q", m.Value)
			}
			slice, err = req.Resolver.ResolveSession(brand.NewSessionID(m.Value))
		case MentionFile:
			slice, err = req.Resolver.ResolveFile(m.Value)
		default:
			return nil, newRefError(RefCodeInvalidReference, "unknown mention kind %q", m.Kind)
		}
		if err != nil {
			return nil, err
		}
		total += len(slice)
		if req.MaxContextBytes > 0 && total > req.MaxContextBytes {
			return nil, newRefError(RefCodeBudgetExceeded, "aggregated context exceeds %d bytes", req.MaxContextBytes)
		}
		res = append(res, ReferenceResolution{
			Kind:  m.Kind,
			Raw:   m.Raw,
			Value: m.Value,
			Slice: slice,
		})
		sb.WriteString("### referenced: ")
		sb.WriteString(m.Value)
		sb.WriteString("\n")
		sb.WriteString(slice)
		sb.WriteString("\n\n")
	}

	return &PreparedReferencedMessage{
		Content:           content,
		AdditionalContext: strings.TrimRight(sb.String(), "\n"),
		References:        res,
	}, nil
}

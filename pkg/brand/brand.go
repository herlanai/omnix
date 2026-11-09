// Package brand provides branded types to prevent mixing raw strings or bytes
// across package contracts, matching util/id-brand and util/bytes upstream.
//
// Branded[T] uses empty tag types to distinguish SessionID, ToolCallID, and other
// identifiers at compile time. Concrete aliases inherit serialization, database,
// and comparison methods. Construct them with NewSessionID, ParseSessionID, and
// similar helpers; brand.New[brand.SessionID] would incorrectly nest wrappers.
// Cross-package contracts should use these concrete types instead of raw values.
package brand

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
)

// Brand tags are unexported empty structs carrying compile-time identity only.
// External callers use concrete branded types and their constructors.

type sessionIDTag struct{}
type toolCallIDTag struct{}
type approvalRequestIDTag struct{}
type jobIDTag struct{}
type skillIDTag struct{}
type attachmentIDTag struct{}
type credentialRefTag struct{}
type workspaceIDTag struct{}
type projectionIDTag struct{}

// Branded[T]: the generic branded-string representation.

// Branded carries a string with a compile-time tag T. Distinct tags produce
// incompatible ID types, preventing a SessionID from being used as a ToolCallID.
// The unexported value is initialized through constructors or UnmarshalJSON.
type Branded[T comparable] struct {
	value string
}

// newBranded constructs an ID from a raw string without validation.
func newBranded[T comparable](raw string) Branded[T] {
	return Branded[T]{value: raw}
}

// parseBranded accepts nonempty strings and rejects empty values.
func parseBranded[T comparable](raw string) (Branded[T], error) {
	if raw == "" {
		return Branded[T]{}, fmt.Errorf("brand: empty id for %T", *new(T))
	}
	return Branded[T]{value: raw}, nil
}

// IsZero reports whether the value is unset or empty.
func (b Branded[T]) IsZero() bool {
	return b.value == ""
}

// String returns the underlying string and implements fmt.Stringer.
func (b Branded[T]) String() string {
	return b.value
}

// Raw returns the underlying string for external boundaries such as logs and
// filesystem paths. Cross-package contracts should retain the branded type.
func (b Branded[T]) Raw() string {
	return b.value
}

// Equal compares IDs with the same brand; incompatible brands cannot be compared.
func (b Branded[T]) Equal(other Branded[T]) bool {
	return b.value == other.value
}

// MarshalJSON encodes the ID as a JSON string.
func (b Branded[T]) MarshalJSON() ([]byte, error) {
	return json.Marshal(b.value)
}

// UnmarshalJSON decodes a JSON string into the branded ID.
func (b *Branded[T]) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("brand: invalid json for %T: %w", *new(T), err)
	}
	b.value = s
	return nil
}

// Value implements driver.Valuer for persistence.
func (b Branded[T]) Value() (driver.Value, error) {
	return b.value, nil
}

// Scan implements sql.Scanner for string, []byte, and nil values.
func (b *Branded[T]) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		b.value = ""
	case string:
		b.value = v
	case []byte:
		b.value = string(v)
	default:
		return fmt.Errorf("brand: cannot scan %T into %T", src, b)
	}
	return nil
}

// Concrete branded types combine aliases with tags.

// SessionID uniquely identifies an event-sourced SessionLog.
type SessionID = Branded[sessionIDTag]

// ToolCallID pairs a tool/call event with its tool/result.
type ToolCallID = Branded[toolCallIDTag]

// ApprovalRequestID uniquely identifies an approval prompt.
type ApprovalRequestID = Branded[approvalRequestIDTag]

// JobID uniquely identifies a background runtime job.
type JobID = Branded[jobIDTag]

// SkillID identifies a skill in the six-layer registry.
type SkillID = Branded[skillIDTag]

// AttachmentID identifies an image or file in attachment storage.
type AttachmentID = Branded[attachmentIDTag]

// CredentialRef brands a POSIX variable name, such as OPENAI_API_KEY.
type CredentialRef = Branded[credentialRefTag]

// WorkspaceID identifies a directory in the workspace registry.
type WorkspaceID = Branded[workspaceIDTag]

// ProjectionID identifies a registered session projection.
type ProjectionID = Branded[projectionIDTag]

// Concrete constructors are the external construction entry points.

// NewSessionID constructs a session ID from a raw string.
func NewSessionID(raw string) SessionID { return newBranded[sessionIDTag](raw) }

// ParseSessionID validates a session ID, rejecting empty strings.
func ParseSessionID(raw string) (SessionID, error) { return parseBranded[sessionIDTag](raw) }

// NewToolCallID constructs a tool call ID from a raw string.
func NewToolCallID(raw string) ToolCallID { return newBranded[toolCallIDTag](raw) }

// ParseToolCallID parses and validates a tool call ID.
func ParseToolCallID(raw string) (ToolCallID, error) { return parseBranded[toolCallIDTag](raw) }

// NewApprovalRequestID constructs an approval request ID.
func NewApprovalRequestID(raw string) ApprovalRequestID { return newBranded[approvalRequestIDTag](raw) }

// ParseApprovalRequestID parses and validates an approval request ID.
func ParseApprovalRequestID(raw string) (ApprovalRequestID, error) {
	return parseBranded[approvalRequestIDTag](raw)
}

// NewJobID constructs a background job ID.
func NewJobID(raw string) JobID { return newBranded[jobIDTag](raw) }

// ParseJobID parses and validates a job ID.
func ParseJobID(raw string) (JobID, error) { return parseBranded[jobIDTag](raw) }

// NewSkillID constructs a skill ID.
func NewSkillID(raw string) SkillID { return newBranded[skillIDTag](raw) }

// ParseSkillID parses and validates a skill ID.
func ParseSkillID(raw string) (SkillID, error) { return parseBranded[skillIDTag](raw) }

// NewAttachmentID constructs an attachment ID.
func NewAttachmentID(raw string) AttachmentID { return newBranded[attachmentIDTag](raw) }

// ParseAttachmentID parses and validates an attachment ID.
func ParseAttachmentID(raw string) (AttachmentID, error) { return parseBranded[attachmentIDTag](raw) }

// NewCredentialRef constructs a credential reference.
func NewCredentialRef(raw string) CredentialRef { return newBranded[credentialRefTag](raw) }

// ParseCredentialRef parses and validates a credential reference.
func ParseCredentialRef(raw string) (CredentialRef, error) {
	return parseBranded[credentialRefTag](raw)
}

// NewWorkspaceID constructs a workspace ID.
func NewWorkspaceID(raw string) WorkspaceID { return newBranded[workspaceIDTag](raw) }

// ParseWorkspaceID parses and validates a workspace ID.
func ParseWorkspaceID(raw string) (WorkspaceID, error) { return parseBranded[workspaceIDTag](raw) }

// NewProjectionID constructs a projection ID.
func NewProjectionID(raw string) ProjectionID { return newBranded[projectionIDTag](raw) }

// ParseProjectionID parses and validates a projection ID.
func ParseProjectionID(raw string) (ProjectionID, error) { return parseBranded[projectionIDTag](raw) }

// Bytes[T]: branded byte slices.

// Bytes carries binary data such as attachments and large tool results.
// Empty tag types distinguish different byte semantics at compile time.
type Bytes[T comparable] struct {
	value []byte
}

// NewBytes wraps a byte slice; the caller must not mutate it afterward.
func NewBytes[T comparable](raw []byte) Bytes[T] {
	return Bytes[T]{value: raw}
}

// ZeroBytes returns zero-value branded bytes.
func ZeroBytes[T comparable]() Bytes[T] {
	return Bytes[T]{}
}

// IsZero reports an empty or unset slice.
func (b Bytes[T]) IsZero() bool {
	return len(b.value) == 0
}

// Bytes returns a view of the underlying byte slice.
func (b Bytes[T]) Bytes() []byte {
	return b.value
}

// String returns the UTF-8 string view, useful for text such as tool results.
func (b Bytes[T]) String() string {
	return string(b.value)
}

// Len returns the underlying byte length.
func (b Bytes[T]) Len() int {
	return len(b.value)
}

// MarshalJSON encodes branded bytes as a base64 JSON string.
func (b Bytes[T]) MarshalJSON() ([]byte, error) {
	return json.Marshal(b.value)
}

// UnmarshalJSON decodes a base64 JSON string into branded bytes.
func (b *Bytes[T]) UnmarshalJSON(data []byte) error {
	var raw []byte
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("brand: invalid bytes json for %T: %w", *new(T), err)
	}
	b.value = raw
	return nil
}

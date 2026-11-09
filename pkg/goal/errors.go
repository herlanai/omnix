// Goal failure classes and block reasons match upstream goal (review fix R06).
// Upstream types.ts defines active/paused/blocked/complete and GoalBlockReason;
// domain.ts defines nine stable routing codes. Callers route on ErrorCode, not
// message text. GoalError supports Go error chaining through Cause, and blocked
// goals must carry a nonempty GoalBlockReason.
package goal

import "fmt"

// ErrorCode is a stable goal-domain routing code matching GoalErrorCode upstream.
type ErrorCode string

// The nine upstream stable error codes.
const (
	ErrorAgentNotLive       ErrorCode = "GOAL_AGENT_NOT_LIVE"
	ErrorNotFound           ErrorCode = "GOAL_NOT_FOUND"
	ErrorAlreadyExists      ErrorCode = "GOAL_ALREADY_EXISTS"
	ErrorStaleRevision      ErrorCode = "GOAL_STALE_REVISION"
	ErrorInvalidObjective   ErrorCode = "GOAL_INVALID_OBJECTIVE"
	ErrorInvalidMaxRounds   ErrorCode = "GOAL_INVALID_MAX_ROUNDS"
	ErrorInvalidBlockReason ErrorCode = "GOAL_INVALID_BLOCK_REASON"
	ErrorInvalidEdit        ErrorCode = "GOAL_INVALID_EDIT"
	ErrorInvalidTransition  ErrorCode = "GOAL_INVALID_TRANSITION"
)

// GoalError carries a stable goal-domain code.
type GoalError struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
	Cause   error     `json:"-"`
}

// Error implements error.
func (e *GoalError) Error() string {
	return fmt.Sprintf("goal error [%s]: %s", e.Code, e.Message)
}

// Unwrap exposes Cause to errors.Is and errors.As.
func (e *GoalError) Unwrap() error { return e.Cause }

// NewGoalError constructs an error with a stable code.
func NewGoalError(code ErrorCode, message string, cause error) *GoalError {
	return &GoalError{Code: code, Message: message, Cause: cause}
}

// FromError preserves GoalError values and classifies other errors as unknown.
func FromError(err error) *GoalError {
	if err == nil {
		return nil
	}
	if ge, ok := err.(*GoalError); ok {
		return ge
	}
	return NewGoalError(ErrorCode("unknown"), err.Error(), err)
}

// GoalBlockReason is the stable reason a goal is blocked.
type GoalBlockReason struct {
	// Code is a stable lower-kebab-case category.
	Code string `json:"code"`
	// Message is a nonempty explanation for users and models.
	Message string `json:"message"`
}

// NewBlockReason constructs a reason and validates nonempty code and message.
func NewBlockReason(code, message string) (GoalBlockReason, bool) {
	if code == "" || message == "" {
		return GoalBlockReason{}, false
	}
	return GoalBlockReason{Code: code, Message: message}, true
}

// Agent configuration and runtime types follow upstream
// packages/core/agent/src/runtime-types.ts and types.ts.
package agent

import (
	"time"
)

// AgentOptions configures agent creation, matching upstream AgentOptions.
// Options are fixed at creation and immutable at runtime. Persona belongs to
// the system prompt and is not configured here.
type AgentOptions struct {
	// Provider names a registered adapter route; empty uses the adapter default.
	Provider string `json:"provider,omitempty"`

	// Model is interpreted by the selected provider; empty uses its default model.
	Model string `json:"model,omitempty"`

	// ReasoningEffort is provider/model-specific; empty uses adapter defaults.
	ReasoningEffort string `json:"reasoningEffort,omitempty"`

	// MaxTokens limits output tokens per model request; zero uses the adapter default.
	MaxTokens int `json:"maxTokens,omitempty"`

	// DefaultTimeout is the per-run timeout; zero disables the timeout.
	DefaultTimeout time.Duration `json:"defaultTimeout,omitempty"`
}

// AgentStatus describes agent lifecycle state, matching upstream AgentStatus.
// Transitions emit agent/status. Idle means no active driver. Running begins
// when wake-up input starts cancellable pre-step processing and lasts until
// the driver drains, closes, or checkpoints turns. Disposal removes the agent
// from its registry; it is not a third observable state.
type AgentStatus string

const (
	// StatusIdle indicates no active driver.
	StatusIdle AgentStatus = "idle"
	// StatusRunning indicates an active driver.
	StatusRunning AgentStatus = "running"
)

// CancelOptions configures cancellation, matching upstream CancelOptions.
type CancelOptions struct {
	// KeepInbox preserves queued and steering input instead of discarding it.
	// The active turn still aborts, but pending work remains for later turns
	// and no canceled inbox splice is recorded.
	KeepInbox bool `json:"keepInbox,omitempty"`
}

// PreStepDecision determines whether to enter a proposed step and which
// messages it receives, matching upstream PreStepDecision.
type PreStepDecision struct {
	// Kind is either reject or enter.
	Kind string `json:"kind"`
	// Messages contains admitted user input when Kind is enter.
	Messages []UserMessage `json:"messages,omitempty"`
	// StartsRequestSeries optionally starts a distinct model-message series
	// before the admitted input when Kind is enter.
	StartsRequestSeries bool `json:"startsRequestSeries,omitempty"`
}

// RejectPreStep creates a decision that does not enter the step.
func RejectPreStep() PreStepDecision {
	return PreStepDecision{Kind: "reject"}
}

// EnterPreStep creates a decision admitting the supplied messages.
func EnterPreStep(messages ...UserMessage) PreStepDecision {
	return PreStepDecision{Kind: "enter", Messages: messages}
}

// SessionStartSource identifies why a session lifecycle starts:
// startup creates a seed, resume loads persistence, clear restarts after
// clearing, and compact restarts after compaction.
type SessionStartSource string

const (
	// StartSourceStartup indicates seed creation.
	StartSourceStartup SessionStartSource = "startup"
	// StartSourceResume indicates loading persisted state.
	StartSourceResume SessionStartSource = "resume"
	// StartSourceClear indicates restarting after clearing.
	StartSourceClear SessionStartSource = "clear"
	// StartSourceCompact indicates restarting after compaction.
	StartSourceCompact SessionStartSource = "compact"
)

// UserMessage is a simplified representation of upstream UserMessage.
type UserMessage struct {
	// ID identifies the message.
	ID string `json:"id"`
	// Content contains the message text.
	Content string `json:"content"`
	// Source identifies the origin, such as run, followup, or inbox.
	Source string `json:"source,omitempty"`
	// CreatedAt records creation time.
	CreatedAt time.Time `json:"createdAt,omitempty"`
}

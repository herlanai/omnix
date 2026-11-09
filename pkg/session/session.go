// Package session provides event-sourced session logs, following upstream
// packages/core/session (M04). State derives entirely from an append-only log
// and can be replayed. EventData and concrete payload types form a typed union
// selected by the serialized type field. SessionLog.Append is the sole write
// path and rejects invalid turn/step pairing or tool call/result transitions.
package session

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/invariant"
)

// Event types grouped by capability.

// EventType identifies an event using a string.
type EventType string

// Group A: turn and step execution.
const (
	EventTurnStart         EventType = "turn/start"
	EventTurnEnd           EventType = "turn/end"
	EventTurnStopping      EventType = "turn/stopping"
	EventStepStart         EventType = "step/start"
	EventStepEnd           EventType = "step/end"
	EventAgentError        EventType = "agent/error"
	EventAgentPreStep      EventType = "agent/pre-step"
	EventAgentRequest      EventType = "agent/request"
	EventAgentStatus       EventType = "agent/status"
	EventAgentInboxSpliced EventType = "agent/inbox/spliced"
)

// Group B: messages.
const (
	EventUserMessage        EventType = "user/message"
	EventAssistantMessage   EventType = "assistant/message"
	EventAssistantChunk     EventType = "assistant/chunk"
	EventAssistantReasoning EventType = "assistant/reasoning"
	EventInjectionContext   EventType = "injection/context"
	EventSessionTitle       EventType = "session/title"
)

// Group C: tools.
const (
	EventToolCall         EventType = "tool/call"
	EventToolResult       EventType = "tool/result"
	EventToolError        EventType = "tool/error"
	EventToolObserved     EventType = "tool/observed"
	EventToolPresentation EventType = "tool/presentation"
)

// Group D: planning.
const (
	EventPlanMode     EventType = "plan/mode"
	EventPlanApproval EventType = "plan/approval"
	EventGoalChange   EventType = "goal/change"
	EventGoalRound    EventType = "goal/round"
	EventTodoWrite    EventType = "todo/write"
	EventEndSeed      EventType = "session/end-seed"
	EventPlanUpdate   EventType = "project/plan-update"
)

// Group E: requests and approvals.
const (
	EventRequestHeader     EventType = "request/header"
	EventRequestContext    EventType = "request/context"
	EventApprovalAsked     EventType = "approval/asked"
	EventApprovalDecided   EventType = "approval/decided"
	EventPresetChange      EventType = "permission/preset-change"
	EventAgentPresetChange EventType = "agent/preset-change"
	EventCommandRun        EventType = "command/run"
	EventCommandDone       EventType = "command/done"
)

// Group F: filesystem operations.
const (
	EventFSWriteIntent EventType = "fs/write-intent"
	EventFSEditIntent  EventType = "fs/edit-intent"
	EventFSObserved    EventType = "fs/observed"
	EventFSVersionBump EventType = "fs/version-bump"
	EventFSRead        EventType = "fs/read"
	EventFSDirList     EventType = "fs/dir-list"
)

// Group G: skills.
const (
	EventSkillsChange  EventType = "skills/change"
	EventSkillsCatalog EventType = "skills/catalog"
	EventSkillInject   EventType = "skill/inject"
)

// Group H: LLM operations.
const (
	EventLLMRequest EventType = "llm/request"
	EventLLMStream  EventType = "llm/stream"
	EventLLMRetry   EventType = "llm/retry"
	EventLLMError   EventType = "llm/error"
	EventLLMDone    EventType = "llm/done"
)

// Group I: surface replacement and miscellaneous events.
const (
	EventSurfaceReplace          EventType = "surface/replace"
	EventAttachmentAdded         EventType = "attachment/added"
	EventSessionProjectionUpdate EventType = "session/projection-updated"
	EventWorkspaceChange         EventType = "workspace/change"
	EventUserQuestion            EventType = "user/question"
	EventSandboxMode             EventType = "sandbox/mode"
)

// All event types for enumeration and serialization round-trip tests.
var AllEventTypes = []EventType{
	EventTurnStart, EventTurnEnd, EventTurnStopping, EventStepStart, EventStepEnd,
	EventAgentError, EventAgentPreStep, EventAgentRequest, EventAgentStatus, EventAgentInboxSpliced,
	EventUserMessage, EventAssistantMessage, EventAssistantChunk, EventAssistantReasoning,
	EventInjectionContext, EventSessionTitle,
	EventToolCall, EventToolResult, EventToolError, EventToolObserved, EventToolPresentation,
	EventPlanMode, EventPlanApproval, EventGoalChange, EventGoalRound, EventTodoWrite,
	EventEndSeed, EventPlanUpdate,
	EventRequestHeader, EventRequestContext, EventApprovalAsked, EventApprovalDecided,
	EventPresetChange, EventAgentPresetChange, EventCommandRun, EventCommandDone,
	EventFSWriteIntent, EventFSEditIntent, EventFSObserved, EventFSVersionBump, EventFSRead, EventFSDirList,
	EventSkillsChange, EventSkillsCatalog, EventSkillInject,
	EventLLMRequest, EventLLMStream, EventLLMRetry, EventLLMError, EventLLMDone,
	EventSurfaceReplace, EventAttachmentAdded, EventSessionProjectionUpdate, EventWorkspaceChange,
	EventUserQuestion, EventSandboxMode,
}

// EventData: typed payload union.

// EventData is implemented by every concrete event payload. EventType
// identifies the variant, forming a type-safe event union.
type EventData interface {
	EventType() EventType
}

// Group A: turns and steps.

// TurnStartData records turn/start. Turn numbers increase monotonically
// from zero, independently within each session.
type TurnStartData struct {
	Turn uint64 `json:"turn"`
}

func (TurnStartData) EventType() EventType { return EventTurnStart }

// TurnEndReason identifies why a turn ended.
type TurnEndReason string

// Turn endings match upstream TurnEndReasonMap: completed, aborted, blocked,
// error, max-tokens, and interrupted.
const (
	ReasonCompleted   TurnEndReason = "completed"
	ReasonFinished    TurnEndReason = "finished" // Legacy alias for completed.
	ReasonInterrupted TurnEndReason = "interrupted"
	ReasonAborted     TurnEndReason = "aborted"
	ReasonBlocked     TurnEndReason = "blocked"
	ReasonError       TurnEndReason = "error"
	ReasonMaxTokens   TurnEndReason = "max-tokens"
)

// TurnEndData records turn/end.
type TurnEndData struct {
	Turn   uint64        `json:"turn"`
	Reason TurnEndReason `json:"reason"`
}

func (TurnEndData) EventType() EventType { return EventTurnEnd }

// TurnStoppingData marks turn/stopping for listeners such as the goal-round driver.
type TurnStoppingData struct {
	Reason string `json:"reason,omitempty"`
}

func (TurnStoppingData) EventType() EventType { return EventTurnStopping }

// StepStartData records step/start. Step numbers start at one and increase
// within each turn, resetting when the next turn begins.
type StepStartData struct {
	Turn    uint64 `json:"turn"`
	Step    uint64 `json:"step"`
	StepSeq uint64 `json:"stepSeq,omitempty"` // Legacy field equivalent to Step.
}

func (StepStartData) EventType() EventType { return EventStepStart }

// StepEndData records step/end.
type StepEndData struct {
	Turn    uint64 `json:"turn"`
	Step    uint64 `json:"step"`
	StepSeq uint64 `json:"stepSeq,omitempty"` // Legacy field equivalent to Step.
}

func (StepEndData) EventType() EventType { return EventStepEnd }

// AgentErrorData records an internal agent/error.
type AgentErrorData struct {
	Message string `json:"message"`
	Pkg     string `json:"pkg,omitempty"`
}

func (AgentErrorData) EventType() EventType { return EventAgentError }

// AgentPreStepData marks preprocessing before entering a step.
type AgentPreStepData struct {
	Blocked bool `json:"blocked,omitempty"`
}

func (AgentPreStepData) EventType() EventType { return EventAgentPreStep }

// AgentRequestData records an assembled model request snapshot.
type AgentRequestData struct {
	Provider string `json:"provider"`
	Model    string `json:"model,omitempty"`
}

func (AgentRequestData) EventType() EventType { return EventAgentRequest }

// AgentStatusData records idle/running lifecycle transitions.
type AgentStatusData struct {
	Status string `json:"status"`
}

func (AgentStatusData) EventType() EventType { return EventAgentStatus }

// InboxTarget selects next-turn or next-step input.
type InboxTarget string

const (
	// InboxNextTurn holds input awaiting the next turn.
	InboxNextTurn InboxTarget = "next-turn"
	// InboxNextStep holds input awaiting the next step boundary.
	InboxNextStep InboxTarget = "next-step"
)

// InboxSplicedData records a normalized persisted pending-input list change.
type InboxSplicedData struct {
	Target       InboxTarget `json:"target"`
	Start        int         `json:"start"`
	RemovedCount int         `json:"removedCount,omitempty"`
	Inserted     []string    `json:"inserted"`
	Outcome      string      `json:"outcome,omitempty"`
}

func (InboxSplicedData) EventType() EventType { return EventAgentInboxSpliced }

// Group B: messages.

// UserMessageData records user/message.
type UserMessageData struct {
	Content string `json:"content"`
	Source  string `json:"source,omitempty"` // e.g. "user" / "reference" / "injected"
	Refs    []any  `json:"refs,omitempty"`   // Referenced sessions or files.
}

func (UserMessageData) EventType() EventType { return EventUserMessage }

// AssistantMessageData records a completed assistant message.
type AssistantMessageData struct {
	Reasoning   string   `json:"reasoning,omitempty"`
	Content     string   `json:"content"`
	ToolCallIDs []string `json:"toolCallIds,omitempty"`
}

func (AssistantMessageData) EventType() EventType { return EventAssistantMessage }

// AssistantChunkData records streamed assistant text.
type AssistantChunkData struct {
	Text string `json:"text,omitempty"`
}

func (AssistantChunkData) EventType() EventType { return EventAssistantChunk }

// AssistantReasoningData records streamed reasoning.
type AssistantReasoningData struct {
	Text string `json:"text,omitempty"`
}

func (AssistantReasoningData) EventType() EventType { return EventAssistantReasoning }

// InjectionContextData records injected dynamic context, such as a skill catalog.
type InjectionContextData struct {
	Name    string `json:"name"`
	Content string `json:"content"`
	Hash    string `json:"hash,omitempty"`
}

func (InjectionContextData) EventType() EventType { return EventInjectionContext }

// SessionTitleData records the latest-wins session title.
type SessionTitleData struct {
	Title string `json:"title"`
}

func (SessionTitleData) EventType() EventType { return EventSessionTitle }

// Group C: tools.

// ToolCallData records initiation of a tool call.
type ToolCallData struct {
	CallID brand.ToolCallID `json:"callId"`
	Tool   string           `json:"tool"`
	Input  json.RawMessage  `json:"input,omitempty"`
}

func (ToolCallData) EventType() EventType { return EventToolCall }

// ToolResultData records a tool result paired with its call by CallID.
type ToolResultData struct {
	CallID    brand.ToolCallID `json:"callId"`
	IsError   bool             `json:"isError,omitempty"`
	Output    string           `json:"output,omitempty"`
	Truncated bool             `json:"truncated,omitempty"`
	SpillPath string           `json:"spillPath,omitempty"`
}

func (ToolResultData) EventType() EventType { return EventToolResult }

// ToolErrorData records a tool execution failure.
type ToolErrorData struct {
	CallID brand.ToolCallID `json:"callId"`
	Error  string           `json:"error"`
}

func (ToolErrorData) EventType() EventType { return EventToolError }

// ToolObservedData records observable tool effects, including read-before-write observations.
type ToolObservedData struct {
	CallID brand.ToolCallID `json:"callId"`
	Kind   string           `json:"kind"`
}

func (ToolObservedData) EventType() EventType { return EventToolObserved }

// ToolPresentationData records a presentation card using neutral vocabulary.
type ToolPresentationData struct {
	CallID brand.ToolCallID `json:"callId"`
	Card   string           `json:"card"`
}

func (ToolPresentationData) EventType() EventType { return EventToolPresentation }

// Group D: planning.

// PlanModeData records plan-mode activation or deactivation.
type PlanModeData struct {
	Mode string `json:"mode"` // "on" / "off"
}

func (PlanModeData) EventType() EventType { return EventPlanMode }

// PlanApprovalData records a plan approval outcome.
type PlanApprovalData struct {
	Approved bool `json:"approved"`
}

func (PlanApprovalData) EventType() EventType { return EventPlanApproval }

// GoalPhase identifies the goal lifecycle phase.
type GoalPhase string

// GoalBlockReason records the stable code and message for a blocked goal (R06).
// The goal domain sets it and persists it with goal/change.
type GoalBlockReason struct {
	// Code is a stable lower-kebab-case category.
	Code string `json:"code"`
	// Message is a nonempty explanation for users and models.
	Message string `json:"message"`
}

// GoalChangeData records a goal change with a CAS revision.
type GoalChangeData struct {
	GoalID      string    `json:"goalId"`
	Phase       GoalPhase `json:"phase"`
	Description string    `json:"description,omitempty"`
	MaxRounds   int       `json:"maxRounds,omitempty"`
	Revision    uint64    `json:"revision"`
	// BlockReason is present only for blocked goals and is persisted with the event.
	BlockReason *GoalBlockReason `json:"blockedReason,omitempty"`
}

func (GoalChangeData) EventType() EventType { return EventGoalChange }

// GoalRoundData marks continuation into another goal round.
type GoalRoundData struct {
	Round int `json:"round"`
}

func (GoalRoundData) EventType() EventType { return EventGoalRound }

// TodoEntry is a todo item with one of three statuses, matching upstream TodoItem.
type TodoEntry struct {
	Content string `json:"content"`
	Status  string `json:"status"` // pending | in_progress | completed
}

// TodoWriteData replaces the complete todo list. Items is the content-only
// compatibility field; Entries retains three-state status.
type TodoWriteData struct {
	Items   []string    `json:"items,omitempty"`
	Entries []TodoEntry `json:"entries,omitempty"`
}

func (TodoWriteData) EventType() EventType { return EventTodoWrite }

// EndSeedData marks the first live boundary after Resume/Fork seed replay.
type EndSeedData struct {
	ParentSession brand.SessionID `json:"parentSession,omitempty"`
}

func (EndSeedData) EventType() EventType { return EventEndSeed }

// PlanUpdateData records updated plan content.
type PlanUpdateData struct {
	Content string `json:"content,omitempty"`
}

func (PlanUpdateData) EventType() EventType { return EventPlanUpdate }

// Group E: requests and approvals.

// RequestHeaderData snapshots configuration, system, and tool epochs.
type RequestHeaderData struct {
	ConfigEpoch uint64 `json:"configEpoch"`
	SystemHash  string `json:"systemHash,omitempty"`
	ToolCount   int    `json:"toolCount,omitempty"`
}

func (RequestHeaderData) EventType() EventType { return EventRequestHeader }

// RequestContextData records provider, model, and context-window routing.
type RequestContextData struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Window   int    `json:"contextWindow,omitempty"`
	Reason   string `json:"reason,omitempty"` // initial/resume/change/series
}

func (RequestContextData) EventType() EventType { return EventRequestContext }

// ApprovalAskedData records an approval request.
type ApprovalRequestIDData struct {
	RequestID brand.ApprovalRequestID `json:"requestId"`
	Tool      string                  `json:"tool,omitempty"`
	Summary   string                  `json:"summary,omitempty"`
}

func (ApprovalRequestIDData) EventType() EventType { return EventApprovalAsked }

// ApprovalDecidedData records an approval decision.
type ApprovalDecidedData struct {
	RequestID brand.ApprovalRequestID `json:"requestId"`
	Allowed   bool                    `json:"allowed"`
}

func (ApprovalDecidedData) EventType() EventType { return EventApprovalDecided }

// PresetChangeData records a permission-preset change.
type PresetChangeData struct {
	Preset string `json:"preset"`
}

func (PresetChangeData) EventType() EventType { return EventPresetChange }

// AgentPresetChangeData records an agent-preset change.
type AgentPresetChangeData struct {
	Preset string `json:"preset"`
}

func (AgentPresetChangeData) EventType() EventType { return EventAgentPresetChange }

// CommandRunData records a slash-command invocation.
type CommandRunData struct {
	Command string `json:"command"`
	Args    string `json:"args,omitempty"`
}

func (CommandRunData) EventType() EventType { return EventCommandRun }

// CommandDoneData records slash-command completion.
type CommandDoneData struct {
	Command string `json:"command"`
}

func (CommandDoneData) EventType() EventType { return EventCommandDone }

// Group F: filesystem operations.

// FsIntentData shares fields across write/edit/observed/version-bump/read/directory events.
type FsIntentData struct {
	Path     string `json:"path"`
	Kind     string `json:"kind,omitempty"`
	Version  uint64 `json:"version,omitempty"`
	Observed bool   `json:"observed,omitempty"`
}

func (FsIntentData) EventType() EventType { return "" }

// FsWriteIntentData records intent to write a file.
type FsWriteIntentData struct {
	Path     string `json:"path"`
	Version  uint64 `json:"version,omitempty"`
	Observed bool   `json:"observed,omitempty"`
}

func (FsWriteIntentData) EventType() EventType { return EventFSWriteIntent }

// FsEditIntentData records intent to edit a file.
type FsEditIntentData struct {
	Path    string `json:"path"`
	Version uint64 `json:"version,omitempty"`
}

func (FsEditIntentData) EventType() EventType { return EventFSEditIntent }

// FsObservedData records file observation for read-before-write policy.
type FsObservedData struct {
	Path string `json:"path"`
}

func (FsObservedData) EventType() EventType { return EventFSObserved }

// FsVersionBumpData records a file version increment.
type FsVersionBumpData struct {
	Path    string `json:"path"`
	Version uint64 `json:"version"`
}

func (FsVersionBumpData) EventType() EventType { return EventFSVersionBump }

// FsReadData records a file read.
type FsReadData struct {
	Path string `json:"path"`
}

func (FsReadData) EventType() EventType { return EventFSRead }

// FsDirListData records a directory listing.
type FsDirListData struct {
	Path string `json:"path"`
}

func (FsDirListData) EventType() EventType { return EventFSDirList }

// Group G: skills.

// SkillsChangeData records changes to the skill catalog.
type SkillsChangeData struct {
	Added   []string `json:"added,omitempty"`
	Removed []string `json:"removed,omitempty"`
	Changed []string `json:"changed,omitempty"`
}

func (SkillsChangeData) EventType() EventType { return EventSkillsChange }

// SkillsCatalogData records a complete skill catalog for change-only injection.
type SkillsCatalogData struct {
	Catalog string `json:"catalog"`
	Hash    string `json:"hash"`
}

func (SkillsCatalogData) EventType() EventType { return EventSkillsCatalog }

// SkillInjectData records skill content injected as a user message.
type SkillInjectData struct {
	SkillName string `json:"skillName"`
	Content   string `json:"content,omitempty"`
}

func (SkillInjectData) EventType() EventType { return EventSkillInject }

// Group H: LLM operations.

// LLMRequestData records a model request.
type LLMRequestData struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

func (LLMRequestData) EventType() EventType { return EventLLMRequest }

// LLMStreamData records a model stream chunk.
type LLMStreamData struct {
	Text string `json:"text,omitempty"`
}

func (LLMStreamData) EventType() EventType { return EventLLMStream }

// LLMRetryData records a model retry and its backoff information.
type LLMRetryData struct {
	Attempt   int    `json:"attempt"`
	BackoffMs int64  `json:"backoffMs"`
	Error     string `json:"error"`
}

func (LLMRetryData) EventType() EventType { return EventLLMRetry }

// LLMErrorData records a failed model request.
type LLMErrorData struct {
	Kind  string `json:"kind"` // overload/rate-limit/refusal/context-overflow
	Error string `json:"error"`
}

func (LLMErrorData) EventType() EventType { return EventLLMError }

// LLMDoneData records model completion and token usage.
type LLMDoneData struct {
	PromptTokens     int `json:"promptTokens"`
	CompletionTokens int `json:"completionTokens"`
}

func (LLMDoneData) EventType() EventType { return EventLLMDone }

// Group I: surface replacement and miscellaneous events.

// SurfaceReplaceData records compaction's read-time replacement of history.
type SurfaceReplaceData struct {
	Start uint64          `json:"start"`
	End   uint64          `json:"end"`
	Data  json.RawMessage `json:"data,omitempty"`
}

func (SurfaceReplaceData) EventType() EventType { return EventSurfaceReplace }

// AttachmentAddedData records a new attachment.
type AttachmentAddedData struct {
	AttachmentID brand.AttachmentID `json:"attachmentId"`
	Name         string             `json:"name,omitempty"`
}

func (AttachmentAddedData) EventType() EventType { return EventAttachmentAdded }

// ProjectionUpdateData records a projection-state update.
type ProjectionUpdateData struct {
	ProjectionID brand.ProjectionID `json:"projectionId"`
	Hash         string             `json:"hash,omitempty"`
}

func (ProjectionUpdateData) EventType() EventType { return EventSessionProjectionUpdate }

// WorkspaceChangeData records workspace metadata changes.
type WorkspaceChangeData struct {
	WorkspaceID brand.WorkspaceID `json:"workspaceId"`
	Root        string            `json:"root,omitempty"`
}

func (WorkspaceChangeData) EventType() EventType { return EventWorkspaceChange }

// UserQuestionData records a user-question interaction.
type UserQuestionData struct {
	Question string `json:"question"`
	Answer   string `json:"answer,omitempty"`
}

func (UserQuestionData) EventType() EventType { return EventUserQuestion }

// SandboxModeData records a log-only sandbox override excluded from the model
// transcript. The latest event wins, matching upstream session-mode semantics.
// A delegation source marks an override installed for a subagent.
type SandboxModeData struct {
	Mode   string `json:"mode"`
	Source string `json:"source,omitempty"`
}

func (SandboxModeData) EventType() EventType { return EventSandboxMode }

// Event-type factories for deserialization.

// newEventData creates an empty payload for the event type.
func newEventData(t EventType) (EventData, error) {
	switch t {
	case EventTurnStart:
		return TurnStartData{}, nil
	case EventTurnEnd:
		return TurnEndData{}, nil
	case EventTurnStopping:
		return TurnStoppingData{}, nil
	case EventStepStart:
		return StepStartData{}, nil
	case EventStepEnd:
		return StepEndData{}, nil
	case EventAgentError:
		return AgentErrorData{}, nil
	case EventAgentPreStep:
		return AgentPreStepData{}, nil
	case EventAgentRequest:
		return AgentRequestData{}, nil
	case EventAgentStatus:
		return AgentStatusData{}, nil
	case EventAgentInboxSpliced:
		return InboxSplicedData{}, nil
	case EventUserMessage:
		return UserMessageData{}, nil
	case EventAssistantMessage:
		return AssistantMessageData{}, nil
	case EventAssistantChunk:
		return AssistantChunkData{}, nil
	case EventAssistantReasoning:
		return AssistantReasoningData{}, nil
	case EventInjectionContext:
		return InjectionContextData{}, nil
	case EventSessionTitle:
		return SessionTitleData{}, nil
	case EventToolCall:
		return ToolCallData{}, nil
	case EventToolResult:
		return ToolResultData{}, nil
	case EventToolError:
		return ToolErrorData{}, nil
	case EventToolObserved:
		return ToolObservedData{}, nil
	case EventToolPresentation:
		return ToolPresentationData{}, nil
	case EventPlanMode:
		return PlanModeData{}, nil
	case EventPlanApproval:
		return PlanApprovalData{}, nil
	case EventGoalChange:
		return GoalChangeData{}, nil
	case EventGoalRound:
		return GoalRoundData{}, nil
	case EventTodoWrite:
		return TodoWriteData{}, nil
	case EventEndSeed:
		return EndSeedData{}, nil
	case EventPlanUpdate:
		return PlanUpdateData{}, nil
	case EventRequestHeader:
		return RequestHeaderData{}, nil
	case EventRequestContext:
		return RequestContextData{}, nil
	case EventApprovalAsked:
		return ApprovalRequestIDData{}, nil
	case EventApprovalDecided:
		return ApprovalDecidedData{}, nil
	case EventPresetChange:
		return PresetChangeData{}, nil
	case EventAgentPresetChange:
		return AgentPresetChangeData{}, nil
	case EventCommandRun:
		return CommandRunData{}, nil
	case EventCommandDone:
		return CommandDoneData{}, nil
	case EventFSWriteIntent:
		return FsWriteIntentData{}, nil
	case EventFSEditIntent:
		return FsEditIntentData{}, nil
	case EventFSObserved:
		return FsObservedData{}, nil
	case EventFSVersionBump:
		return FsVersionBumpData{}, nil
	case EventFSRead:
		return FsReadData{}, nil
	case EventFSDirList:
		return FsDirListData{}, nil
	case EventSkillsChange:
		return SkillsChangeData{}, nil
	case EventSkillsCatalog:
		return SkillsCatalogData{}, nil
	case EventSkillInject:
		return SkillInjectData{}, nil
	case EventLLMRequest:
		return LLMRequestData{}, nil
	case EventLLMStream:
		return LLMStreamData{}, nil
	case EventLLMRetry:
		return LLMRetryData{}, nil
	case EventLLMError:
		return LLMErrorData{}, nil
	case EventLLMDone:
		return LLMDoneData{}, nil
	case EventSurfaceReplace:
		return SurfaceReplaceData{}, nil
	case EventAttachmentAdded:
		return AttachmentAddedData{}, nil
	case EventSessionProjectionUpdate:
		return ProjectionUpdateData{}, nil
	case EventWorkspaceChange:
		return WorkspaceChangeData{}, nil
	case EventUserQuestion:
		return UserQuestionData{}, nil
	case EventSandboxMode:
		return SandboxModeData{}, nil
	default:
		return nil, fmt.Errorf("session: unknown event type %q", t)
	}
}

// EventTypeOf returns a payload's type for callers and tests.
func EventTypeOf(d EventData) EventType {
	return d.EventType()
}

// SessionEvent records sequence, time, type, and payload.

// SessionEvent represents one log entry.
type SessionEvent struct {
	// Seq increases contiguously from one.
	Seq uint64 `json:"seq"`
	// Time records monotonic event write time.
	Time time.Time `json:"time"`
	// Type identifies the event variant.
	Type EventType `json:"type"`
	// Data contains the typed payload union.
	Data EventData `json:"data"`
	// SurfaceOp describes read-time surface replacement, the supported compaction mechanism.
	SurfaceOp *SurfaceOp `json:"surfaceOp,omitempty"`
	// SourceEventSeqs records source sequences for compaction and derived messages.
	SourceEventSeqs []uint64 `json:"sourceEventSeqs,omitempty"`
}

// eventEnvelope is the JSON wire representation, avoiding recursive custom serialization.
type eventEnvelope struct {
	Seq             uint64          `json:"seq"`
	Time            time.Time       `json:"time"`
	Type            EventType       `json:"type"`
	Data            json.RawMessage `json:"data"`
	SurfaceOp       *SurfaceOp      `json:"surfaceOp,omitempty"`
	SourceEventSeqs []uint64        `json:"sourceEventSeqs,omitempty"`
}

// MarshalJSON writes seq, time, type, data, and optional surface/source fields.
func (e SessionEvent) MarshalJSON() ([]byte, error) {
	dataBytes, err := json.Marshal(e.Data)
	if err != nil {
		return nil, fmt.Errorf("session: marshal data for %q: %w", e.Type, err)
	}
	env := eventEnvelope{
		Seq:             e.Seq,
		Time:            e.Time,
		Type:            e.Type,
		Data:            dataBytes,
		SurfaceOp:       e.SurfaceOp,
		SourceEventSeqs: e.SourceEventSeqs,
	}
	return json.Marshal(env)
}

// UnmarshalJSON restores the event and dispatches its payload by type.
func (e *SessionEvent) UnmarshalJSON(data []byte) error {
	var env eventEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("session: unmarshal envelope: %w", err)
	}
	instance, err := newEventData(env.Type)
	if err != nil {
		return err
	}
	if len(env.Data) > 0 {
		// Allocate a pointer for decoding, then dereference to retain value semantics
		// so assertions such as ToolCallData continue to match in applyState.
		pv := reflect.New(reflect.TypeOf(instance))
		if err := json.Unmarshal(env.Data, pv.Interface()); err != nil {
			return fmt.Errorf("session: unmarshal data for %q: %w", env.Type, err)
		}
		instance = pv.Elem().Interface().(EventData)
	}
	e.Seq = env.Seq
	e.Time = env.Time
	e.Type = env.Type
	e.Data = instance
	e.SurfaceOp = env.SurfaceOp
	e.SourceEventSeqs = env.SourceEventSeqs
	return nil
}

// SessionLog and temporal invariants.

// sessionState tracks open structures for temporal invariant checks.
type sessionState struct {
	turnOpen  bool
	stepOpen  bool
	openTurn  uint64          // Current turn number, valid while turnOpen.
	openStep  uint64          // Current step number, valid while stepOpen.
	nextTurn  uint64          // Next turn number, beginning at zero.
	nextStep  uint64          // Next step number, reset to one at the start of each turn.
	toolCalls map[string]bool // Maps raw call IDs to result-pairing status.
	lastTime  time.Time
}

// SessionLog owns the sole append path for the event-sourced log.
type SessionLog struct {
	mu         sync.Mutex
	sessionID  brand.SessionID
	events     []SessionEvent
	seq        uint64
	state      sessionState
	invariants *invariant.Registry
	// folder enables H04 incremental projections; nil preserves legacy behavior.
	// Successful appends update it immediately, avoiding repeated full-history folds.
	folder *IncrementalFolder
}

// EnableIncrementalProjection establishes a baseline through FolderFromEvents
// if history already exists, then applies future appends incrementally.
// Without it, callers may continue using FoldAllFromLog.
func (sl *SessionLog) EnableIncrementalProjection() {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	if sl.folder != nil {
		return
	}
	if len(sl.events) == 0 {
		sl.folder = NewIncrementalFolder()
	} else {
		sl.folder = FolderFromEvents(sl.events)
	}
}

// DisableIncrementalProjection disables incremental state for performance comparisons.
func (sl *SessionLog) DisableIncrementalProjection() {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	sl.folder = nil
}

// IncrementalEnabled reports whether incremental projections are enabled.
func (sl *SessionLog) IncrementalEnabled() bool {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	return sl.folder != nil
}

// Projection returns current derived state. With incremental folding enabled,
// Snapshot reuses metadata and lazily rebuilds dirty messages; otherwise it
// falls back to a complete FoldAllFromLog.
func (sl *SessionLog) Projection() SessionProjection {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	if sl.folder != nil {
		return sl.folder.Snapshot(sl.events)
	}
	return FoldAll(sl.events)
}

// ProjectionMeta omits Messages to avoid large message-slice copies on hot
// paths. It returns header, sandbox, approval, preset, goal, todo, plan, and title.
func (sl *SessionLog) ProjectionMeta() SessionProjection {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	if sl.folder != nil {
		return sl.folder.SnapshotMeta(sl.events)
	}
	// Without incremental state, fold everything but clear Messages to honor the contract.
	p := FoldAll(sl.events)
	p.Messages = nil
	return p
}

// IncrementalStats returns counters, or zero FolderStats when disabled.
func (sl *SessionLog) IncrementalStats() FolderStats {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	if sl.folder == nil {
		return FolderStats{}
	}
	return sl.folder.StatsCopy()
}

// NewSessionLog creates an empty log and registers temporal invariant checks.
func NewSessionLog(id brand.SessionID) *SessionLog {
	sl := &SessionLog{
		sessionID:  id,
		state:      sessionState{toolCalls: map[string]bool{}},
		invariants: invariant.NewRegistry(),
	}
	// Register temporal checks with pkg/session attribution on violations.
	_ = sl.invariants.Register("pkg/session", sl.checkTurnPairing)
	_ = sl.invariants.Register("pkg/session", sl.checkStepPairing)
	_ = sl.invariants.Register("pkg/session", sl.checkToolCallResultPairing)
	return sl
}

// SessionID identifies the owning session.
func (sl *SessionLog) SessionID() brand.SessionID {
	return sl.sessionID
}

// SandboxMode returns the latest logged sandbox/mode override, or ok false
// if absent, matching upstream effectiveSandboxMode.
func (sl *SessionLog) SandboxMode() (SandboxMode, bool) {
	evs := sl.Events()
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].Type != EventSandboxMode {
			continue
		}
		data, ok := evs[i].Data.(SandboxModeData)
		if !ok {
			continue
		}
		return SandboxMode(data.Mode), true
	}
	return "", false
}

// Events returns a read-only snapshot of the event list.
func (sl *SessionLog) Events() []SessionEvent {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	out := make([]SessionEvent, len(sl.events))
	copy(out, sl.events)
	return out
}

// Len returns the number of events.
func (sl *SessionLog) Len() int {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	return len(sl.events)
}

// LastSeq returns the latest sequence number, or zero for an empty log.
func (sl *SessionLog) LastSeq() uint64 {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	return sl.seq
}

// NextTurn returns the next zero-based turn number so Agent can construct turn/start.
func (sl *SessionLog) NextTurn() uint64 {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	return sl.state.nextTurn
}

// NextStep returns the next step number, reset to one for each turn.
func (sl *SessionLog) NextStep() uint64 {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	return sl.state.nextStep
}

// OpenTurn returns the open turn number, or zero and false if none.
func (sl *SessionLog) OpenTurn() (uint64, bool) {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	return sl.state.openTurn, sl.state.turnOpen
}

// OpenStep returns the open step number, or zero and false if none.
func (sl *SessionLog) OpenStep() (uint64, bool) {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	return sl.state.openStep, sl.state.stepOpen
}

// Append is the sole write path. It assigns contiguous sequence numbers and
// monotonic timestamps, then rejects invalid turn, step, or tool pairing.
func (sl *SessionLog) Append(data EventData) (uint64, error) {
	sl.mu.Lock()
	defer sl.mu.Unlock()

	// Clamp optional external timestamps so they never precede the last event.
	now := time.Now()
	if !sl.state.lastTime.IsZero() && now.Before(sl.state.lastTime) {
		now = sl.state.lastTime
	}

	// Construct the event.
	sl.seq++
	ev := SessionEvent{
		Seq:  sl.seq,
		Time: now,
		Type: data.EventType(),
		Data: data,
	}

	// Apply the state transition and validate temporal ordering.
	if err := sl.applyState(ev); err != nil {
		sl.seq-- // Restore the sequence counter to preserve continuity.
		return 0, err
	}

	sl.events = append(sl.events, ev)
	sl.state.lastTime = now
	// Feed the event to the enabled incremental folder without scanning history.
	if sl.folder != nil {
		sl.folder.Append(ev)
	}
	return ev.Seq, nil
}

// Check validates whole-log turn, step, and tool pairing before persistence
// or flushing. Append already checks individual transitions; complete-log
// balance checks are unnecessary after each append because intermediate
// structures may legitimately remain open.
func (sl *SessionLog) Check() []error {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	return sl.invariants.Run()
}

// applyState updates open structures and validates pairing for the event type.
func (sl *SessionLog) applyState(ev SessionEvent) error {
	switch ev.Type {
	case EventTurnStart:
		if sl.state.turnOpen {
			return fmt.Errorf("session invariant: turn/start while turn already open")
		}
		data, ok := ev.Data.(TurnStartData)
		if !ok {
			return fmt.Errorf("session invariant: turn/start data type mismatch")
		}
		if data.Turn != sl.state.nextTurn {
			return fmt.Errorf("session invariant: turn/start expected turn %d, got %d", sl.state.nextTurn, data.Turn)
		}
		sl.state.turnOpen = true
		sl.state.openTurn = data.Turn
		sl.state.nextStep = 1 // Reset step numbering to one at each turn start.
	case EventTurnEnd:
		if !sl.state.turnOpen {
			return fmt.Errorf("session invariant: turn/end without open turn/start")
		}
		if sl.state.stepOpen {
			return fmt.Errorf("session invariant: turn/end while step still open")
		}
		data, ok := ev.Data.(TurnEndData)
		if !ok {
			return fmt.Errorf("session invariant: turn/end data type mismatch")
		}
		if data.Turn != sl.state.openTurn {
			return fmt.Errorf("session invariant: turn/end turn %d does not match open turn %d", data.Turn, sl.state.openTurn)
		}
		sl.state.turnOpen = false
		sl.state.openTurn = 0
		sl.state.nextTurn++
	case EventTurnStopping:
		// Allow stopping while a turn is open.
		if !sl.state.turnOpen {
			return fmt.Errorf("session invariant: turn/stopping without open turn")
		}
	case EventStepStart:
		if !sl.state.turnOpen {
			return fmt.Errorf("session invariant: step/start without open turn")
		}
		if sl.state.stepOpen {
			return fmt.Errorf("session invariant: step/start while step already open")
		}
		data, ok := ev.Data.(StepStartData)
		if !ok {
			return fmt.Errorf("session invariant: step/start data type mismatch")
		}
		if data.Turn != sl.state.openTurn {
			return fmt.Errorf("session invariant: step/start in turn %d but open turn is %d", data.Turn, sl.state.openTurn)
		}
		if data.Step != sl.state.nextStep {
			return fmt.Errorf("session invariant: step/start expected step %d, got %d", sl.state.nextStep, data.Step)
		}
		sl.state.stepOpen = true
		sl.state.openStep = data.Step
		sl.state.nextStep++
	case EventStepEnd:
		if !sl.state.stepOpen {
			return fmt.Errorf("session invariant: step/end without open step/start")
		}
		data, ok := ev.Data.(StepEndData)
		if !ok {
			return fmt.Errorf("session invariant: step/end data type mismatch")
		}
		if data.Turn != sl.state.openTurn {
			return fmt.Errorf("session invariant: step/end in turn %d but open turn is %d", data.Turn, sl.state.openTurn)
		}
		if data.Step != sl.state.openStep {
			return fmt.Errorf("session invariant: step/end step %d does not match open step %d", data.Step, sl.state.openStep)
		}
		sl.state.stepOpen = false
		sl.state.openStep = 0
	case EventToolCall:
		if !sl.state.stepOpen {
			return fmt.Errorf("session invariant: tool/call without open step")
		}
		td, ok := ev.Data.(ToolCallData)
		if !ok {
			return fmt.Errorf("session invariant: tool/call data type mismatch")
		}
		key := td.CallID.Raw()
		if _, exists := sl.state.toolCalls[key]; exists {
			return fmt.Errorf("session invariant: duplicate tool/call %q", key)
		}
		sl.state.toolCalls[key] = false
	case EventToolResult, EventToolError:
		td, ok := ev.Data.(ToolResultData)
		var callID brand.ToolCallID
		if ok {
			callID = td.CallID
		} else if ed, ok2 := ev.Data.(ToolErrorData); ok2 {
			callID = ed.CallID
		} else {
			return fmt.Errorf("session invariant: tool result data type mismatch")
		}
		key := callID.Raw()
		open, exists := sl.state.toolCalls[key]
		if !exists {
			return fmt.Errorf("session invariant: tool/result for unknown call %q", key)
		}
		if open {
			return fmt.Errorf("session invariant: duplicate tool/result for call %q", key)
		}
		sl.state.toolCalls[key] = true
	}
	return nil
}

// checkTurnPairing validates whole-log turn balance for the invariant registry.
func (sl *SessionLog) checkTurnPairing() error {
	open := false
	for _, ev := range sl.events {
		switch ev.Type {
		case EventTurnStart:
			open = true
		case EventTurnEnd:
			open = false
		}
	}
	if open {
		return fmt.Errorf("unclosed turn at end of log")
	}
	return nil
}

// checkStepPairing validates whole-log step balance.
func (sl *SessionLog) checkStepPairing() error {
	turnOpen, stepOpen := false, false
	for _, ev := range sl.events {
		switch ev.Type {
		case EventTurnStart:
			turnOpen = true
		case EventTurnEnd:
			turnOpen = false
		case EventStepStart:
			stepOpen = turnOpen
		case EventStepEnd:
			stepOpen = false
		}
	}
	if stepOpen {
		return fmt.Errorf("unclosed step at end of log")
	}
	return nil
}

// checkToolCallResultPairing validates whole-log tool call/result pairing.
func (sl *SessionLog) checkToolCallResultPairing() error {
	matched := map[string]bool{}
	for _, ev := range sl.events {
		switch ev.Type {
		case EventToolCall:
			if td, ok := ev.Data.(ToolCallData); ok {
				matched[td.CallID.Raw()] = false
			}
		case EventToolResult, EventToolError:
			var callID brand.ToolCallID
			if td, ok := ev.Data.(ToolResultData); ok {
				callID = td.CallID
			} else if ed, ok := ev.Data.(ToolErrorData); ok {
				callID = ed.CallID
			}
			if _, exists := matched[callID.Raw()]; !exists {
				return fmt.Errorf("tool/result for unknown call %q", callID.Raw())
			}
			matched[callID.Raw()] = true
		}
	}
	for k, done := range matched {
		if !done {
			return fmt.Errorf("unmatched tool/call %q", k)
		}
	}
	return nil
}

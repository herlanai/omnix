// Session projections (M05) mirror packages/core/session in the upstream project.
// Fold functions derive state from events without modifying them. Incremental
// updates and full replay must agree. Compaction applies surface replacements
// when reading, leaving the original append-only event log intact.
package session

import (
	"sort"
)

// Effective events after applying surface replacements at read time.

// EffectiveEvents returns events not hidden by surface replacements, preserving
// their original order. Replacement intervals are inclusive and clipped to the
// replacing event's sequence number. The source events are never modified.
func EffectiveEvents(events []SessionEvent) []SessionEvent {
	hidden := replacementIntervals(events)
	out := make([]SessionEvent, 0, len(events))
	for _, ev := range events {
		if hidden.contains(ev.Seq) {
			continue
		}
		out = append(out, ev)
	}
	return out
}

// Message derivation.

// Message is a derived conversation message used to assemble model context.
type Message struct {
	Role        string   `json:"role"` // "user" / "assistant"
	Content     string   `json:"content"`
	ToolCallIDs []string `json:"toolCallIds,omitempty"`
	Seq         uint64   `json:"seq"`
}

// deriveMessages builds messages from effective user/message and assistant/message
// events, including assistant tool-call IDs. Chunks and reasoning are aggregated
// by the caller; replaced historical events do not contribute messages.
func deriveMessages(events []SessionEvent) []Message {
	effective := EffectiveEvents(events)
	var msgs []Message
	for _, ev := range effective {
		switch ev.Type {
		case EventUserMessage:
			if d, ok := ev.Data.(UserMessageData); ok {
				msgs = append(msgs, Message{Role: "user", Content: d.Content, Seq: ev.Seq})
			}
		case EventAssistantMessage:
			if d, ok := ev.Data.(AssistantMessageData); ok {
				msgs = append(msgs, Message{Role: "assistant", Content: d.Content, ToolCallIDs: d.ToolCallIDs, Seq: ev.Seq})
			}
		}
	}
	return msgs
}

// DeriveMessages exposes message derivation to callers outside the package.
func DeriveMessages(events []SessionEvent) []Message {
	return deriveMessages(events)
}

// Projections for headers, permissions, goals, todos, plan mode, and titles.

// RequestHeaderFold is the latest request/header snapshot.
type RequestHeaderFold struct {
	Present     bool   `json:"present"`
	ConfigEpoch uint64 `json:"configEpoch,omitempty"`
	SystemHash  string `json:"systemHash,omitempty"`
	ToolCount   int    `json:"toolCount,omitempty"`
}

// foldRequestHeader derives the latest request header.
func foldRequestHeader(events []SessionEvent) RequestHeaderFold {
	var out RequestHeaderFold
	for _, ev := range events {
		if ev.Type != EventRequestHeader {
			continue
		}
		if d, ok := ev.Data.(RequestHeaderData); ok {
			out = RequestHeaderFold{Present: true, ConfigEpoch: d.ConfigEpoch, SystemHash: d.SystemHash, ToolCount: d.ToolCount}
		}
	}
	return out
}

// FoldRequestHeader exposes request-header derivation.
func FoldRequestHeader(events []SessionEvent) RequestHeaderFold {
	return foldRequestHeader(events)
}

// SandboxMode identifies the sandbox access mode.
type SandboxMode string

// Supported sandbox modes.
const (
	SandboxReadOnly         SandboxMode = "read-only"
	SandboxWorkspaceWrite   SandboxMode = "workspace-write"
	SandboxDangerFullAccess SandboxMode = "danger-full-access"
)

// EffectiveSandboxMode records the preset-derived mode, enforcement, and root.
type EffectiveSandboxMode struct {
	Present  bool        `json:"present"`
	Mode     SandboxMode `json:"mode,omitempty"`
	Enforced bool        `json:"enforced,omitempty"`
	Root     string      `json:"root,omitempty"`
}

// ApprovalPolicy identifies the approval strategy.
type ApprovalPolicy string

// Supported approval policies.
const (
	ApprovalAllowAll         ApprovalPolicy = "allow-all"
	ApprovalDenyAll          ApprovalPolicy = "deny-all"
	ApprovalAskDangerous     ApprovalPolicy = "ask-dangerous"
	ApprovalAskDangerousEdit ApprovalPolicy = "ask-dangerous-tool-edit"
)

// EffectiveApprovalPolicy is the effective approval-policy projection.
type EffectiveApprovalPolicy struct {
	Present bool           `json:"present"`
	Policy  ApprovalPolicy `json:"policy,omitempty"`
	Source  string         `json:"source,omitempty"` // preset / user / session
}

// PermissionPresetFold is the permission-preset projection.
type PermissionPresetFold struct {
	Present bool   `json:"present"`
	Preset  string `json:"preset,omitempty"`
}

// presetSandbox maps preset names to sandbox modes, matching upstream permission presets.
var presetSandbox = map[string]SandboxMode{
	"safe":    SandboxReadOnly,
	"danger":  SandboxDangerFullAccess,
	"review":  SandboxReadOnly,
	"default": SandboxWorkspaceWrite,
}

// presetApproval maps preset names to approval policies.
var presetApproval = map[string]ApprovalPolicy{
	"safe":    ApprovalAskDangerous,
	"danger":  ApprovalAllowAll,
	"review":  ApprovalAskDangerousEdit,
	"default": ApprovalAskDangerous,
}

// foldPermissionPreset derives the latest permission preset.
func foldPermissionPreset(events []SessionEvent) PermissionPresetFold {
	var out PermissionPresetFold
	for _, ev := range events {
		if ev.Type != EventPresetChange {
			continue
		}
		if d, ok := ev.Data.(PresetChangeData); ok {
			out = PermissionPresetFold{Present: true, Preset: d.Preset}
		}
	}
	return out
}

// FoldPermissionPreset exposes permission-preset derivation.
func FoldPermissionPreset(events []SessionEvent) PermissionPresetFold {
	return foldPermissionPreset(events)
}

// foldEffectiveSandboxMode derives the sandbox mode from the latest preset.
func foldEffectiveSandboxMode(events []SessionEvent) EffectiveSandboxMode {
	preset := foldPermissionPreset(events)
	if !preset.Present {
		return EffectiveSandboxMode{}
	}
	mode, ok := presetSandbox[preset.Preset]
	if !ok {
		mode = SandboxWorkspaceWrite
	}
	return EffectiveSandboxMode{Present: true, Mode: mode, Enforced: true}
}

// FoldEffectiveSandboxMode exposes effective sandbox-mode derivation.
func FoldEffectiveSandboxMode(events []SessionEvent) EffectiveSandboxMode {
	return foldEffectiveSandboxMode(events)
}

// foldEffectiveApprovalPolicy derives the approval policy from the latest preset.
func foldEffectiveApprovalPolicy(events []SessionEvent) EffectiveApprovalPolicy {
	preset := foldPermissionPreset(events)
	if !preset.Present {
		return EffectiveApprovalPolicy{}
	}
	policy, ok := presetApproval[preset.Preset]
	if !ok {
		policy = ApprovalAskDangerous
	}
	return EffectiveApprovalPolicy{Present: true, Policy: policy, Source: "preset"}
}

// FoldEffectiveApprovalPolicy exposes effective approval-policy derivation.
func FoldEffectiveApprovalPolicy(events []SessionEvent) EffectiveApprovalPolicy {
	return foldEffectiveApprovalPolicy(events)
}

// GoalFold is the goal-state projection; its CAS revision guards concurrent updates.
type GoalFold struct {
	Present     bool      `json:"present"`
	GoalID      string    `json:"goalId,omitempty"`
	Phase       GoalPhase `json:"phase,omitempty"`
	Description string    `json:"description,omitempty"`
	MaxRounds   int       `json:"maxRounds,omitempty"`
	Revision    uint64    `json:"revision,omitempty"`
	// BlockReason is the latest blocking reason persisted by goal/change (R06).
	BlockReason *GoalBlockReason `json:"blockedReason,omitempty"`
}

// foldGoalChange selects the latest goal state with a strictly increasing revision.
func foldGoalChange(events []SessionEvent) GoalFold {
	var out GoalFold
	for _, ev := range events {
		if ev.Type != EventGoalChange {
			continue
		}
		if d, ok := ev.Data.(GoalChangeData); ok {
			// Accept only strictly increasing revisions to preserve CAS semantics.
			if !out.Present || d.Revision > out.Revision {
				out = GoalFold{
					Present:     true,
					GoalID:      d.GoalID,
					Phase:       d.Phase,
					Description: d.Description,
					MaxRounds:   d.MaxRounds,
					Revision:    d.Revision,
					BlockReason: d.BlockReason,
				}
			}
		}
	}
	return out
}

// FoldGoalChange exposes goal-state derivation.
func FoldGoalChange(events []SessionEvent) GoalFold {
	return foldGoalChange(events)
}

// TodoFold is the todo-list projection, replaced in full by the latest write.
type TodoFold struct {
	Present bool        `json:"present"`
	Items   []string    `json:"items,omitempty"`
	Entries []TodoEntry `json:"entries,omitempty"`
}

// foldTodoWrite derives the latest list; each todo/write replaces it in full.
func foldTodoWrite(events []SessionEvent) TodoFold {
	var out TodoFold
	for _, ev := range events {
		if ev.Type != EventTodoWrite {
			continue
		}
		if d, ok := ev.Data.(TodoWriteData); ok {
			out = TodoFold{
				Present: true,
				Items:   append([]string(nil), d.Items...),
				Entries: append([]TodoEntry(nil), d.Entries...),
			}
		}
	}
	return out
}

// FoldTodoWrite exposes todo-list derivation.
func FoldTodoWrite(events []SessionEvent) TodoFold {
	return foldTodoWrite(events)
}

// PlanModeFold is the plan-mode projection.
type PlanModeFold struct {
	Present bool   `json:"present"`
	Mode    string `json:"mode,omitempty"` // "on" / "off"
}

// foldPlanMode derives the latest on/off plan mode.
func foldPlanMode(events []SessionEvent) PlanModeFold {
	var out PlanModeFold
	for _, ev := range events {
		if ev.Type != EventPlanMode {
			continue
		}
		if d, ok := ev.Data.(PlanModeData); ok {
			out = PlanModeFold{Present: true, Mode: d.Mode}
		}
	}
	return out
}

// FoldPlanMode exposes plan-mode derivation.
func FoldPlanMode(events []SessionEvent) PlanModeFold {
	return foldPlanMode(events)
}

// SessionTitleFold is the latest session-title projection.
type SessionTitleFold struct {
	Present bool   `json:"present"`
	Title   string `json:"title,omitempty"`
}

// foldSessionTitle derives the latest title.
func foldSessionTitle(events []SessionEvent) SessionTitleFold {
	var out SessionTitleFold
	for _, ev := range events {
		if ev.Type != EventSessionTitle {
			continue
		}
		if d, ok := ev.Data.(SessionTitleData); ok {
			out = SessionTitleFold{Present: true, Title: d.Title}
		}
	}
	return out
}

// FoldSessionTitle exposes session-title derivation.
func FoldSessionTitle(events []SessionEvent) SessionTitleFold {
	return foldSessionTitle(events)
}

// Aggregate projection replay.

// SessionProjection aggregates all derived projections.
type SessionProjection struct {
	Messages      []Message               `json:"messages"`
	RequestHeader RequestHeaderFold       `json:"requestHeader"`
	SandboxMode   EffectiveSandboxMode    `json:"sandboxMode"`
	Approval      EffectiveApprovalPolicy `json:"approvalPolicy"`
	Preset        PermissionPresetFold    `json:"preset"`
	Goal          GoalFold                `json:"goal"`
	Todo          TodoFold                `json:"todo"`
	PlanMode      PlanModeFold            `json:"planMode"`
	Title         SessionTitleFold        `json:"title"`
}

// FoldAll replays the event list to derive all projection states.
func FoldAll(events []SessionEvent) SessionProjection {
	effective := EffectiveEvents(events)
	return SessionProjection{
		Messages:      deriveMessages(effective),
		RequestHeader: foldRequestHeader(effective),
		SandboxMode:   foldEffectiveSandboxMode(effective),
		Approval:      foldEffectiveApprovalPolicy(effective),
		Preset:        foldPermissionPreset(effective),
		Goal:          foldGoalChange(effective),
		Todo:          foldTodoWrite(effective),
		PlanMode:      foldPlanMode(effective),
		Title:         foldSessionTitle(effective),
	}
}

// FoldAllFromLog derives projections directly from a SessionLog snapshot.
func FoldAllFromLog(sl *SessionLog) SessionProjection {
	return FoldAll(sl.Events())
}

// sortMessagesBySeq orders messages by sequence for assertions and context assembly.
func sortMessagesBySeq(msgs []Message) {
	sort.SliceStable(msgs, func(i, j int) bool { return msgs[i].Seq < msgs[j].Seq })
}

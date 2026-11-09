// This file implements incremental session projections (H04). Append applies
// only the new event rather than replaying history. Each projection has a pure
// Apply operation. Surface replacement marks messages dirty because prior
// contributions cannot be cheaply undone; the next message snapshot rebuilds
// that projection while metadata projections remain incremental.
// NewIncrementalFolder and FolderFromEvents initialize state; Append/AppendMany
// consume events; Snapshot lazily rebuilds messages; FullRebuild supports
// consistency checks against FoldAll.
package session

import (
	"slices"
	"sync"
)

// Projection equality supports registry updates and incremental change detection.

// Equal implements ProjectionState.
func (m Message) Equal(o ProjectionState) bool {
	other, ok := o.(Message)
	if !ok {
		return false
	}
	return m.Role == other.Role &&
		m.Content == other.Content &&
		m.Seq == other.Seq &&
		strSliceEq(m.ToolCallIDs, other.ToolCallIDs)
}

// messageSliceEq compares message slices by value.
func messageSliceEq(a, b []Message) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !a[i].Equal(b[i]) {
			return false
		}
	}
	return true
}

// strSliceEq compares string slices by value.
func strSliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Equal compares request-header projections.
func (r RequestHeaderFold) Equal(o ProjectionState) bool {
	other, ok := o.(RequestHeaderFold)
	if !ok {
		return false
	}
	return r.Present == other.Present &&
		r.ConfigEpoch == other.ConfigEpoch &&
		r.SystemHash == other.SystemHash &&
		r.ToolCount == other.ToolCount
}

// Equal compares effective sandbox modes.
func (s EffectiveSandboxMode) Equal(o ProjectionState) bool {
	other, ok := o.(EffectiveSandboxMode)
	if !ok {
		return false
	}
	return s.Present == other.Present && s.Mode == other.Mode &&
		s.Enforced == other.Enforced && s.Root == other.Root
}

// Equal compares effective approval policies.
func (a EffectiveApprovalPolicy) Equal(o ProjectionState) bool {
	other, ok := o.(EffectiveApprovalPolicy)
	if !ok {
		return false
	}
	return a.Present == other.Present && a.Policy == other.Policy && a.Source == other.Source
}

// Equal compares permission-preset projections.
func (p PermissionPresetFold) Equal(o ProjectionState) bool {
	other, ok := o.(PermissionPresetFold)
	if !ok {
		return false
	}
	return p.Present == other.Present && p.Preset == other.Preset
}

// Equal compares goal projections.
func (g GoalFold) Equal(o ProjectionState) bool {
	other, ok := o.(GoalFold)
	if !ok {
		return false
	}
	if g.Present != other.Present ||
		g.GoalID != other.GoalID ||
		g.Phase != other.Phase ||
		g.Description != other.Description ||
		g.MaxRounds != other.MaxRounds ||
		g.Revision != other.Revision {
		return false
	}
	// Compare optional BlockReason values (R06).
	if (g.BlockReason == nil) != (other.BlockReason == nil) {
		return false
	}
	if g.BlockReason != nil {
		if g.BlockReason.Code != other.BlockReason.Code ||
			g.BlockReason.Message != other.BlockReason.Message {
			return false
		}
	}
	return true
}

// Equal compares todo projections.
func (t TodoFold) Equal(o ProjectionState) bool {
	other, ok := o.(TodoFold)
	if !ok {
		return false
	}
	if t.Present != other.Present {
		return false
	}
	return strSliceEq(t.Items, other.Items) && slices.Equal(t.Entries, other.Entries)
}

// Equal compares plan-mode projections.
func (p PlanModeFold) Equal(o ProjectionState) bool {
	other, ok := o.(PlanModeFold)
	if !ok {
		return false
	}
	return p.Present == other.Present && p.Mode == other.Mode
}

// Equal compares session-title projections.
func (s SessionTitleFold) Equal(o ProjectionState) bool {
	other, ok := o.(SessionTitleFold)
	if !ok {
		return false
	}
	return s.Present == other.Present && s.Title == other.Title
}

// SessionProjectionEqual compares aggregate projections for consistency checks.
func SessionProjectionEqual(a, b SessionProjection) bool {
	return messageSliceEq(a.Messages, b.Messages) &&
		a.RequestHeader.Equal(b.RequestHeader) &&
		a.SandboxMode.Equal(b.SandboxMode) &&
		a.Approval.Equal(b.Approval) &&
		a.Preset.Equal(b.Preset) &&
		a.Goal.Equal(b.Goal) &&
		a.Todo.Equal(b.Todo) &&
		a.PlanMode.Equal(b.PlanMode) &&
		a.Title.Equal(b.Title)
}

// Incremental Apply operations for each projection.

// ApplyMessage appends a message event and reports whether SurfaceReplace
// occurred. It does not remove replaced history; callers must mark messages
// dirty and rebuild them when necessary.
func ApplyMessage(msgs []Message, ev SessionEvent) ([]Message, bool) {
	dirty := false
	if ev.SurfaceOp != nil && ev.SurfaceOp.Op == SurfaceReplace {
		dirty = true
	}
	switch ev.Type {
	case EventUserMessage:
		if d, ok := ev.Data.(UserMessageData); ok {
			msgs = append(msgs, Message{Role: "user", Content: d.Content, Seq: ev.Seq})
		}
	case EventAssistantMessage:
		if d, ok := ev.Data.(AssistantMessageData); ok {
			msgs = append(msgs, Message{
				Role:        "assistant",
				Content:     d.Content,
				ToolCallIDs: append([]string(nil), d.ToolCallIDs...),
				Seq:         ev.Seq,
			})
		}
	}
	return msgs, dirty
}

// ApplyRequestHeader keeps the latest request header.
func ApplyRequestHeader(s RequestHeaderFold, ev SessionEvent) RequestHeaderFold {
	if ev.Type != EventRequestHeader {
		return s
	}
	if d, ok := ev.Data.(RequestHeaderData); ok {
		return RequestHeaderFold{
			Present:     true,
			ConfigEpoch: d.ConfigEpoch,
			SystemHash:  d.SystemHash,
			ToolCount:   d.ToolCount,
		}
	}
	return s
}

// ApplyPermissionPreset keeps the latest permission preset.
func ApplyPermissionPreset(s PermissionPresetFold, ev SessionEvent) PermissionPresetFold {
	if ev.Type != EventPresetChange {
		return s
	}
	if d, ok := ev.Data.(PresetChangeData); ok {
		return PermissionPresetFold{Present: true, Preset: d.Preset}
	}
	return s
}

// ApplyGoalChange applies strictly increasing revisions with latest-wins CAS semantics.
func ApplyGoalChange(s GoalFold, ev SessionEvent) GoalFold {
	if ev.Type != EventGoalChange {
		return s
	}
	if d, ok := ev.Data.(GoalChangeData); ok {
		if !s.Present || d.Revision > s.Revision {
			return GoalFold{
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
	return s
}

// ApplyTodoWrite replaces the todo state with the latest complete value.
func ApplyTodoWrite(s TodoFold, ev SessionEvent) TodoFold {
	if ev.Type != EventTodoWrite {
		return s
	}
	if d, ok := ev.Data.(TodoWriteData); ok {
		return TodoFold{
			Present: true,
			Items:   append([]string(nil), d.Items...),
			Entries: append([]TodoEntry(nil), d.Entries...),
		}
	}
	return s
}

// ApplyPlanMode keeps the latest plan mode.
func ApplyPlanMode(s PlanModeFold, ev SessionEvent) PlanModeFold {
	if ev.Type != EventPlanMode {
		return s
	}
	if d, ok := ev.Data.(PlanModeData); ok {
		return PlanModeFold{Present: true, Mode: d.Mode}
	}
	return s
}

// ApplySessionTitle keeps the latest session title.
func ApplySessionTitle(s SessionTitleFold, ev SessionEvent) SessionTitleFold {
	if ev.Type != EventSessionTitle {
		return s
	}
	if d, ok := ev.Data.(SessionTitleData); ok {
		return SessionTitleFold{Present: true, Title: d.Title}
	}
	return s
}

// deriveSandboxAndApproval derives policy from the latest preset using constant-time lookups.
func deriveSandboxAndApproval(preset PermissionPresetFold) (EffectiveSandboxMode, EffectiveApprovalPolicy) {
	if !preset.Present {
		return EffectiveSandboxMode{}, EffectiveApprovalPolicy{}
	}
	mode, ok := presetSandbox[preset.Preset]
	if !ok {
		mode = SandboxWorkspaceWrite
	}
	policy, ok := presetApproval[preset.Preset]
	if !ok {
		policy = ApprovalAskDangerous
	}
	return EffectiveSandboxMode{Present: true, Mode: mode, Enforced: true},
		EffectiveApprovalPolicy{Present: true, Policy: policy, Source: "preset"}
}

// IncrementalFolder is the H04 entry point.

// IncrementalFolder maintains SessionProjection incrementally. Create a folder,
// Append events, then call Snapshot(events); history is read when messages
// are dirty. Projection state is not internally synchronized: callers must
// serialize access, as SessionLog.Append does under its mutex.
type IncrementalFolder struct {
	// Incremental projection state.
	messages  []Message
	reqHeader RequestHeaderFold
	preset    PermissionPresetFold
	sandbox   EffectiveSandboxMode
	approval  EffectiveApprovalPolicy
	goal      GoalFold
	todo      TodoFold
	planMode  PlanModeFold
	title     SessionTitleFold

	// Only message projections are invalidated by surface replacement.
	messagesDirty bool

	// Statistics support H04 monitoring and benchmarks.
	Stats FolderStats

	// statsMu protects statistics access.
	statsMu sync.Mutex
}

// FolderStats exposes H04 performance counters.
type FolderStats struct {
	// IncrementalAppends counts incremental appends.
	IncrementalAppends uint64 `json:"incrementalAppends"`
	// DirtyRebuilds counts message rebuilds caused by surface replacement.
	DirtyRebuilds uint64 `json:"dirtyRebuilds"`
	// EventsScanned counts one per incremental event and all events during a rebuild.
	EventsScanned uint64 `json:"eventsScanned"`
}

// NewIncrementalFolder creates empty projection state.
func NewIncrementalFolder() *IncrementalFolder {
	return &IncrementalFolder{}
}

// FolderFromEvents initializes a baseline with FoldAll, then supports
// incremental appends. Its initial snapshot matches FoldAll(events).
func FolderFromEvents(events []SessionEvent) *IncrementalFolder {
	f := NewIncrementalFolder()
	proj := FoldAll(events)
	f.messages = proj.Messages
	f.reqHeader = proj.RequestHeader
	f.preset = proj.Preset
	f.sandbox = proj.SandboxMode
	f.approval = proj.Approval
	f.goal = proj.Goal
	f.todo = proj.Todo
	f.planMode = proj.PlanMode
	f.title = proj.Title
	// Count events scanned to establish the baseline.
	f.statsMu.Lock()
	f.Stats.EventsScanned += uint64(len(events))
	f.statsMu.Unlock()
	return f
}

// Append applies one event incrementally; surface replacement only marks messages dirty.
func (f *IncrementalFolder) Append(ev SessionEvent) {
	var dirty bool
	f.messages, dirty = ApplyMessage(f.messages, ev)
	if dirty {
		f.messagesDirty = true
	}
	f.reqHeader = ApplyRequestHeader(f.reqHeader, ev)

	oldPreset := f.preset
	f.preset = ApplyPermissionPreset(f.preset, ev)
	// A changed preset updates sandbox and approval through constant-time lookups.
	if oldPreset != f.preset {
		f.sandbox, f.approval = deriveSandboxAndApproval(f.preset)
	}

	f.goal = ApplyGoalChange(f.goal, ev)
	f.todo = ApplyTodoWrite(f.todo, ev)
	f.planMode = ApplyPlanMode(f.planMode, ev)
	f.title = ApplySessionTitle(f.title, ev)

	f.statsMu.Lock()
	f.Stats.IncrementalAppends++
	f.Stats.EventsScanned++
	f.statsMu.Unlock()
}

// AppendMany applies a batch through successive Append calls.
func (f *IncrementalFolder) AppendMany(evs []SessionEvent) {
	for _, ev := range evs {
		f.Append(ev)
	}
}

// Snapshot returns the aggregate projection. Dirty messages are lazily rebuilt
// on reading, while metadata reuses incremental state. Without replacements
// no rebuild occurs. The message slice is copied, but nested slices and pointers
// remain shared and must be treated as read-only. Use SnapshotMeta on hot paths
// that only need metadata to avoid copying the message slice.
func (f *IncrementalFolder) Snapshot(events []SessionEvent) SessionProjection {
	return f.snapshot(events, true)
}

// SnapshotMeta returns metadata only, with Messages always nil. Use it for
// Goal, Todo, Preset, Sandbox, Approval, PlanMode, Title, and RequestHeader
// when message copying would add unnecessary hot-path work.
func (f *IncrementalFolder) SnapshotMeta(events []SessionEvent) SessionProjection {
	return f.snapshot(events, false)
}

// snapshot shares implementation; copyMessages requests defensive message copies.
func (f *IncrementalFolder) snapshot(events []SessionEvent, copyMessages bool) SessionProjection {
	messages := f.messages
	if f.messagesDirty {
		// Rebuild only messages through deriveMessages and EffectiveEvents.
		messages = deriveMessages(events)
		f.messagesDirty = false
		f.messages = messages
		f.statsMu.Lock()
		f.Stats.DirtyRebuilds++
		f.Stats.EventsScanned += uint64(len(events))
		f.statsMu.Unlock()
	}
	p := SessionProjection{
		RequestHeader: f.reqHeader,
		SandboxMode:   f.sandbox,
		Approval:      f.approval,
		Preset:        f.preset,
		Goal:          f.goal,
		Todo:          f.todo,
		PlanMode:      f.planMode,
		Title:         f.title,
	}
	if copyMessages {
		p.Messages = append([]Message(nil), messages...)
	}
	return p
}

// FullRebuild recomputes all projections for consistency testing, bypassing dirty shortcuts.
func (f *IncrementalFolder) FullRebuild(events []SessionEvent) SessionProjection {
	proj := FoldAll(events)
	f.messages = proj.Messages
	f.reqHeader = proj.RequestHeader
	f.preset = proj.Preset
	f.sandbox = proj.SandboxMode
	f.approval = proj.Approval
	f.goal = proj.Goal
	f.todo = proj.Todo
	f.planMode = proj.PlanMode
	f.title = proj.Title
	f.messagesDirty = false
	return proj
}

// StatsCopy returns a synchronized statistics snapshot.
func (f *IncrementalFolder) StatsCopy() FolderStats {
	f.statsMu.Lock()
	defer f.statsMu.Unlock()
	return f.Stats
}

// MarkDirty explicitly invalidates messages after known history compaction or for debugging.
func (f *IncrementalFolder) MarkDirty() { f.messagesDirty = true }

// SessionLog hooks update IncrementalFolder on append so callers can read
// projections without repeatedly folding the complete log.

// Incremental projections are optional. EnableIncrementalProjection initializes
// the folder; successful appends update it immediately. Callers that do not
// enable it retain the existing projection behavior.

// SessionLog owns the optional folder and initializes it in session.go.
// Existing logs establish their baseline through FolderFromEvents.

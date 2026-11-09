// H04 incremental fold tests compare every append with FoldAll for cold and warm starts, lazy
// message rebuilding after SurfaceReplace, and all latest-wins projections. Benchmarks compare
// 10000 append/read operations: the incremental metadata path targets one tenth of full-replay CPU
// time.
package tests

import (
	"testing"
	"time"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/session"
)

// newTestLogH04 creates a SessionLog with optional incremental projection.
func newTestLogH04(enableIncremental bool) *session.SessionLog {
	sl := session.NewSessionLog(brand.NewSessionID("h04-sess"))
	if enableIncremental {
		sl.EnableIncrementalProjection()
	}
	return sl
}

// fixedTimeH04 returns a fixed timestamp for consistent invariant checks.
func fixedTimeH04() time.Time { return time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC) }

// 1. Cold-start equivalence after each user, assistant, preset, todo, goal, plan, title, and
// header event.

// TestH04IncrementalEquivalentAfterEachAppend compares incremental snapshots with FoldAll after
// every append across all projection types.
func TestH04IncrementalEquivalentAfterEachAppend(t *testing.T) {
	sl := newTestLogH04(true)
	if !sl.IncrementalEnabled() {
		t.Fatal("EnableIncrementalProjection 后应为 true")
	}

	// Construct events covering every projection type.
	payloads := []session.EventData{
		session.UserMessageData{Content: "u1"},
		session.AssistantMessageData{Content: "a1"},
		session.RequestHeaderData{ConfigEpoch: 1, SystemHash: "h1", ToolCount: 3},
		session.PresetChangeData{Preset: "danger"},
		session.GoalChangeData{GoalID: "g1", Phase: session.GoalPhase("in-progress"), Description: "d1", MaxRounds: 5, Revision: 1},
		session.TodoWriteData{Items: []string{"t1", "t2"}},
		session.PlanModeData{Mode: "on"},
		session.SessionTitleData{Title: "会话标题"},
		session.UserMessageData{Content: "u2"},
		session.AssistantMessageData{Content: "a2", ToolCallIDs: []string{"call-1"}},
		session.PresetChangeData{Preset: "safe"},
		session.GoalChangeData{GoalID: "g1", Phase: session.GoalPhase("in-progress"), Description: "d2", MaxRounds: 5, Revision: 2},
		session.TodoWriteData{Items: []string{"t1-new"}},
		session.PlanModeData{Mode: "off"},
		session.RequestHeaderData{ConfigEpoch: 2, SystemHash: "h2", ToolCount: 7},
		session.SessionTitleData{Title: "标题V2"},
	}

	for i, p := range payloads {
		if _, err := sl.Append(p); err != nil {
			t.Fatalf("第 %d 条 Append 失败: %v", i, err)
		}
		got := sl.Projection()
		want := session.FoldAll(sl.Events())
		if !session.SessionProjectionEqual(got, want) {
			t.Fatalf("第 %d 条 Append 后增量 ≠ 全量\n got=%+v\nwant=%+v", i, got, want)
		}
	}

	stats := sl.IncrementalStats()
	if stats.IncrementalAppends != uint64(len(payloads)) {
		t.Fatalf("IncrementalAppends = %d, want %d", stats.IncrementalAppends, len(payloads))
	}
	// Without SurfaceReplace, DirtyRebuilds must stay zero.
	if stats.DirtyRebuilds != 0 {
		t.Fatalf("DirtyRebuilds = %d, want 0", stats.DirtyRebuilds)
	}
}

// 2. Warm-start equivalence: append N events, enable incremental projections, then append M more.

func TestH04WarmStartEquivalence(t *testing.T) {
	sl := newTestLogH04(false) // Start with incremental projections disabled.
	// Warm-start baseline events.
	base := []session.EventData{
		session.UserMessageData{Content: "old-u"},
		session.AssistantMessageData{Content: "old-a"},
		session.PresetChangeData{Preset: "review"},
		session.TodoWriteData{Items: []string{"old"}},
		session.GoalChangeData{GoalID: "g-old", Phase: session.GoalPhase("pending"), Description: "old", MaxRounds: 1, Revision: 1},
	}
	for _, d := range base {
		if _, err := sl.Append(d); err != nil {
			t.Fatal(err)
		}
	}
	// Compute the full-fold baseline.
	baseline := session.FoldAll(sl.Events())

	// Enable incremental projections.
	sl.EnableIncrementalProjection()
	got := sl.Projection()
	if !session.SessionProjectionEqual(got, baseline) {
		t.Fatalf("热启动启用瞬间 got ≠ baseline FoldAll")
	}
	stats1 := sl.IncrementalStats()
	// Initializing from N events must record N scanned events.
	if int(stats1.EventsScanned) < len(base) {
		t.Fatalf("热启动 EventsScanned = %d, expect >= %d", stats1.EventsScanned, len(base))
	}
	// Append M new events.
	extra := []session.EventData{
		session.UserMessageData{Content: "new-u"},
		session.AssistantMessageData{Content: "new-a"},
		session.TodoWriteData{Items: []string{"new1", "new2"}},
		session.PlanModeData{Mode: "on"},
		session.SessionTitleData{Title: "warm"},
	}
	for _, d := range extra {
		if _, err := sl.Append(d); err != nil {
			t.Fatal(err)
		}
	}
	got2 := sl.Projection()
	want2 := session.FoldAll(sl.Events())
	if !session.SessionProjectionEqual(got2, want2) {
		t.Fatalf("热启动后 Append M 条 got ≠ FoldAll()")
	}
	stats2 := sl.IncrementalStats()
	if stats2.IncrementalAppends != stats1.IncrementalAppends+uint64(len(extra)) {
		t.Fatalf("增量 Append 计数未正确累加: stats1=%+v stats2=%+v", stats1, stats2)
	}
}

// 3. SurfaceReplace marks messages dirty and lazy rebuilding preserves equivalence.

func TestH04SurfaceReplaceDirtyRebuild(t *testing.T) {
	sl := newTestLogH04(true)
	// Append three user messages with sequence numbers 1, 2, and 3.
	for _, c := range []string{"u1", "u2", "u3"} {
		if _, err := sl.Append(session.UserMessageData{Content: c}); err != nil {
			t.Fatal(err)
		}
	}
	// The initial message projection is [u1, u2, u3].
	before := sl.Projection()
	if len(before.Messages) != 3 {
		t.Fatalf("messages before = %d, want 3", len(before.Messages))
	}

	// Construct sequence 4 with SurfaceReplace(2,2) and content u2-replaced. SurfaceOp belongs to
	// SessionEvent rather than its typed Data, so attach it directly to the copied events. This models
	// the replacement events produced by compaction; sequence 2 becomes hidden.
	if _, err := sl.Append(session.UserMessageData{Content: "u2-replaced"}); err != nil {
		t.Fatal(err)
	}
	// Attach a replacement of sequence 2 to the newly appended sequence 4 event.
	evs := sl.Events()
	evs[3].SurfaceOp = &session.SurfaceOp{Op: session.SurfaceReplace, Start: 2, End: 2}
	// SessionLog.Events returns a copy, so mutating it cannot inject a replacement into the log. Test
	// IncrementalFolder directly with the modified events instead of accessing SessionLog's private
	// state.
	f := session.NewIncrementalFolder()
	// AppendMany exercises ApplyMessage's SurfaceReplace dirty flag.
	f.AppendMany(evs)
	snap := f.Snapshot(evs)
	expected := session.FoldAll(evs)
	if !session.SessionProjectionEqual(snap, expected) {
		t.Fatalf("SurfaceReplace 后增量 Snapshot ≠ FoldAll()\n got=%+v\nwant=%+v", snap, expected)
	}
	// Hide sequence 2 but retain the replacement at its own sequence 4. The resulting order is u1(1),
	// u3(3), u2-replaced(4).
	if len(snap.Messages) != 3 {
		t.Fatalf("dirty rebuild 后 messages 条数 = %d, want 3", len(snap.Messages))
	}
	if snap.Messages[0].Content != "u1" || snap.Messages[1].Content != "u3" || snap.Messages[2].Content != "u2-replaced" {
		t.Fatalf("dirty rebuild 后 messages = [%+v], 顺序应为 u1/u3/u2-replaced（EffectiveEvents 隐藏 seq=2，替换事件保留原 seq=4）", snap.Messages)
	}
	stats := f.StatsCopy()
	if stats.DirtyRebuilds != 1 {
		t.Fatalf("SurfaceReplace 后 DirtyRebuilds = %d, want 1", stats.DirtyRebuilds)
	}
}

// 4. Latest-wins projections: monotonic Goal revisions and preset-to-sandbox/approval mapping.

func TestH04GoalCASIncremental(t *testing.T) {
	f := session.NewIncrementalFolder()
	// Apply revision 1.
	f.Append(session.SessionEvent{Seq: 1, Type: session.EventGoalChange, Data: session.GoalChangeData{
		GoalID: "g", Revision: 1, Description: "r1", MaxRounds: 3,
	}})
	// Apply the newer revision 2.
	f.Append(session.SessionEvent{Seq: 2, Type: session.EventGoalChange, Data: session.GoalChangeData{
		GoalID: "g", Revision: 2, Description: "r2", MaxRounds: 3,
	}})
	// Reject stale revision 1; the description must remain r2.
	f.Append(session.SessionEvent{Seq: 3, Type: session.EventGoalChange, Data: session.GoalChangeData{
		GoalID: "g", Revision: 1, Description: "stale", MaxRounds: 3,
	}})
	snap := f.Snapshot(nil)
	if !snap.Goal.Present || snap.Goal.Revision != 2 || snap.Goal.Description != "r2" {
		t.Fatalf("Goal CAS 增量失败: Goal=%+v", snap.Goal)
	}
}

func TestH04PresetSandboxApprovalDerivation(t *testing.T) {
	f := session.NewIncrementalFolder()
	check := func(t *testing.T, wantMode session.SandboxMode, wantPolicy session.ApprovalPolicy) {
		t.Helper()
		snap := f.Snapshot(nil)
		if snap.SandboxMode.Mode != wantMode {
			t.Fatalf("sandbox mode = %v, want %v", snap.SandboxMode.Mode, wantMode)
		}
		if snap.Approval.Policy != wantPolicy {
			t.Fatalf("approval policy = %v, want %v", snap.Approval.Policy, wantPolicy)
		}
	}
	f.Append(session.SessionEvent{Seq: 1, Type: session.EventPresetChange, Data: session.PresetChangeData{Preset: "safe"}})
	check(t, session.SandboxReadOnly, session.ApprovalAskDangerous)

	f.Append(session.SessionEvent{Seq: 2, Type: session.EventPresetChange, Data: session.PresetChangeData{Preset: "danger"}})
	check(t, session.SandboxDangerFullAccess, session.ApprovalAllowAll)

	f.Append(session.SessionEvent{Seq: 3, Type: session.EventPresetChange, Data: session.PresetChangeData{Preset: "review"}})
	check(t, session.SandboxReadOnly, session.ApprovalAskDangerousEdit)
}

// 5. Benchmark 10000 append/metadata-read operations: full replay is O(N squared), incremental
// maintenance is O(N), targeting at least 10x speedup.

// benchEventsH04 constructs N mixed benchmark events.
func benchEventsH04(n int) []session.SessionEvent {
	evs := make([]session.SessionEvent, 0, n)
	for i := 0; i < n; i++ {
		seq := uint64(i + 1)
		switch i % 6 {
		case 0:
			evs = append(evs, session.SessionEvent{Seq: seq, Type: session.EventUserMessage, Data: session.UserMessageData{Content: "hello"}})
		case 1:
			evs = append(evs, session.SessionEvent{Seq: seq, Type: session.EventAssistantMessage, Data: session.AssistantMessageData{Content: "world"}})
		case 2:
			evs = append(evs, session.SessionEvent{Seq: seq, Type: session.EventRequestHeader, Data: session.RequestHeaderData{ConfigEpoch: uint64(i), SystemHash: "x"}})
		case 3:
			evs = append(evs, session.SessionEvent{Seq: seq, Type: session.EventTodoWrite, Data: session.TodoWriteData{Items: []string{"a", "b", "c"}}})
		case 4:
			evs = append(evs, session.SessionEvent{Seq: seq, Type: session.EventGoalChange, Data: session.GoalChangeData{GoalID: "g", Revision: seq, MaxRounds: 3}})
		case 5:
			evs = append(evs, session.SessionEvent{Seq: seq, Type: session.EventPlanMode, Data: session.PlanModeData{Mode: "on"}})
		}
	}
	return evs
}

// BenchmarkIncrementalH04FullBaseline folds all events after each append, scanning approximately N
// squared / 2 events, or 50 million for N=10000.
func BenchmarkIncrementalH04FullBaseline(b *testing.B) {
	evs := benchEventsH04(10000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		acc := make([]session.SessionEvent, 0, len(evs))
		var lastGoalRev uint64
		for _, ev := range evs {
			acc = append(acc, ev)
			p := session.FoldAll(acc)
			lastGoalRev = p.Goal.Revision
		}
		_ = lastGoalRev
	}
}

// BenchmarkIncrementalH04IncrementalPath uses Append and SnapshotMeta for O(N) total work, with no
// message copying.
func BenchmarkIncrementalH04IncrementalPath(b *testing.B) {
	evs := benchEventsH04(10000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f := session.NewIncrementalFolder()
		var lastGoalRev uint64
		for _, ev := range evs {
			f.Append(ev)
			p := f.SnapshotMeta(nil)
			lastGoalRev = p.Goal.Revision
		}
		_ = lastGoalRev
	}
}

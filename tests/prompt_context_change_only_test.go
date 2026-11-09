// N05/D4 tests verify context hashes change only with state, GoalRoundContext stops after
// completion, change-only injection appends user messages without altering the system prefix, and
// CompactPreserve retains the latest context snapshot.
package tests

import (
	"testing"

	"github.com/herlanai/omnix/pkg/sysprompt"
)

// TestN05ContextHashStableAndChanges verifies stable hashes until state changes.
func TestN05ContextHashStableAndChanges(t *testing.T) {
	reg := sysprompt.NewContextRegistry()
	reg.Register("plan", 1, "plan mode: on")
	text1, hash1 := reg.Compute()
	text1b, hash1b := reg.Compute()
	if text1 != text1b || hash1 != hash1b {
		t.Fatal("无变化时 hash 应稳定")
	}
	// Changing plan mode must change the hash.
	reg.Register("plan", 1, "plan mode: off")
	_, hash2 := reg.Compute()
	if hash2 == hash1 {
		t.Fatal("状态变化后 hash 应改变")
	}
	_ = text1
}

// TestN05ChangeOnlyInject injects once per change and skips unchanged state.
func TestN05ChangeOnlyInject(t *testing.T) {
	reg := sysprompt.NewContextRegistry()
	reg.Register("goal", 1, "goal: ship it")
	in := sysprompt.NewChangeOnlyInjector(reg, "")
	// Initial injection.
	if _, injected := in.MightInject(); !injected {
		t.Fatal("首次应注入")
	}
	// Unchanged state must not be injected again.
	for i := 0; i < 50; i++ {
		if _, injected := in.MightInject(); injected {
			t.Fatalf("第 %d 轮不变不应注入", i)
		}
	}
	if in.InjectCount() != 1 {
		t.Fatalf("应只注入 1 次, 实际 %d", in.InjectCount())
	}
	// Changed state triggers injection.
	reg.Register("goal", 1, "goal: ship it now")
	if _, injected := in.MightInject(); !injected {
		t.Fatal("状态变化后应注入")
	}
	if in.InjectCount() != 2 {
		t.Fatalf("应有两次注入, 实际 %d", in.InjectCount())
	}
}

// TestN05GoalRoundStopsOnComplete stops GoalRoundContext injection after completion.
func TestN05GoalRoundStopsOnComplete(t *testing.T) {
	active := sysprompt.GoalRoundContext{Active: true, Round: 3, GoalDesc: "build"}
	if active.Render() == "" {
		t.Fatal("active 时应渲染续轮提示")
	}
	complete := sysprompt.GoalRoundContext{Active: false, Round: 3, GoalDesc: "build"}
	if complete.Render() != "" {
		t.Fatal("complete 后应停止注入（空）")
	}
}

// TestN05CompactionPreserveSnapshot retains the latest snapshot after compaction.
func TestN05CompactionPreserveSnapshot(t *testing.T) {
	reg := sysprompt.NewContextRegistry()
	reg.Register("ctx1", 1, "snapshot-A")
	cp := &sysprompt.CompactPreserve{}

	// Record snapshot A, which will be compacted away.
	textA, hashA := reg.Compute()
	cp.Record(textA, hashA)
	// A state change produces snapshot B.
	reg.Register("ctx2", 2, "snapshot-B")
	textB, hashB := reg.Compute()
	cp.Record(textB, hashB)

	// Compaction discards A and retains the latest snapshot B.
	latest, hash, ok := cp.Latest()
	if !ok || hash != hashB || latest != textB {
		t.Fatalf("compaction 后应保留最新 snapshot B: ok=%v", ok)
	}
	if hash == hashA {
		t.Fatal("不应保留旧 snapshot A")
	}
}

// TestN05PersistedHashRecovery restores persisted hashes to avoid duplicate injection across
// turns.
func TestN05PersistedHashRecovery(t *testing.T) {
	reg := sysprompt.NewContextRegistry()
	reg.Register("p1", 1, "stable")
	reg.Register("p2", 2, "settings: x")
	// Inject and persist the hash in the first turn.
	in1 := sysprompt.NewChangeOnlyInjector(reg, "")
	_, _ = in1.MightInject()
	persisted := in1.LastHash()

	// Restore the hash in a new turn; unchanged state must not be reinjected.
	in2 := sysprompt.NewChangeOnlyInjector(reg, persisted)
	if _, injected := in2.MightInject(); injected {
		t.Fatal("持久化恢复后状态未变不应重复注入")
	}
	if in2.InjectCount() != 0 {
		t.Fatalf("应 0 次注入, 实际 %d", in2.InjectCount())
	}
}

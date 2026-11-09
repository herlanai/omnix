// M10 dynamic PromptContext registration and snapshots.
package tests

import (
	"testing"

	"github.com/herlanai/omnix/pkg/sysprompt"
)

// TestPromptContextDynamic applies additions and removals on the next assembly.
func TestPromptContextDynamic(t *testing.T) {
	reg := sysprompt.NewContextRegistry()

	// Register two contexts.
	reg.Register("runtime_ctx", 300, "cwd=/workspace")
	reg.Register("plan_policy", 500, "先计划再执行")

	text, hash := reg.Compute()
	if text == "" {
		t.Fatal("注册后应能 compute 出内容")
	}
	if hash == "" {
		t.Fatal("非空快照应有哈希")
	}
	// Repeated computation must produce the same hash.
	if _, h2 := reg.Compute(); h2 != hash {
		t.Fatal("hash 应稳定")
	}

	// Removing a context changes the next computed result.
	reg.Unregister("plan_policy")
	text2, hash2 := reg.Compute()
	if text2 == text || hash2 == hash {
		t.Fatal("移除后 compute 应变化")
	}
}

// TestPromptContextChangeDetection changes the hash when content changes.
func TestPromptContextChangeDetection(t *testing.T) {
	reg := sysprompt.NewContextRegistry()
	changed := reg.Register("runtime_ctx", 300, "v1")
	if !changed {
		t.Fatal("首次注册应视为变化")
	}
	if !reg.Register("runtime_ctx", 300, "v2") {
		t.Fatal("内容变化后 register 应返回 changed=true")
	}
	if reg.Register("runtime_ctx", 300, "v2") {
		t.Fatal("内容未变时 register 应返回 changed=false（change-only）")
	}
}

// TestGoalRoundContext injects active goals and stops after completion.
func TestGoalRoundContext(t *testing.T) {
	active := sysprompt.GoalRoundContext{Active: true, Round: 2, GoalDesc: "实现任务"}
	if active.Render() == "" {
		t.Fatal("active 时应渲染续轮提示")
	}

	// Stop injecting after goal completion.
	inactive := sysprompt.GoalRoundContext{Active: false, Round: 2, GoalDesc: "实现任务"}
	if inactive.Render() != "" {
		t.Fatal("goal.complete 后应停止注入")
	}
}

// TestPromptContextSnapshotPersist retains complete snapshots for reconstruction after compaction.
func TestPromptContextSnapshotPersist(t *testing.T) {
	reg := sysprompt.NewContextRegistry()
	reg.Register("runtime_ctx", 300, "snapshot-value")

	// Capture a snapshot to simulate retention across compaction.
	snap := reg.Snapshot()
	if snap["runtime_ctx"] != "snapshot-value" {
		t.Fatalf("快照内容异常: %v", snap)
	}
}

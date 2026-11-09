// M15/M41 slash command tests.
package tests

import (
	"context"
	"strings"
	"testing"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/commands"
	"github.com/herlanai/omnix/pkg/session"
)

// TestCommandsSlashDispatch makes /plan off append plan/mode(off), not user/message.
func TestCommandsSlashDispatch(t *testing.T) {
	reg := commands.NewRegistry()
	sl := session.NewSessionLog(brand.NewSessionID("cmd_1"))

	out, err := reg.Dispatch(context.Background(), "/plan off", sl)
	if err != nil {
		t.Fatalf("Dispatch 失败: %v", err)
	}
	if !strings.Contains(out, "off") {
		t.Fatalf("输出应含 off: %q", out)
	}

	// Both command/run and command/done must be recorded.
	hasRun, hasDone, hasPlanMode := false, false, false
	for _, ev := range sl.Events() {
		switch ev.Type {
		case session.EventCommandRun:
			hasRun = true
		case session.EventCommandDone:
			hasDone = true
		case session.EventPlanMode:
			hasPlanMode = true
		}
	}
	if !hasRun || !hasDone || !hasPlanMode {
		t.Fatalf("应写 command/run + command/done + plan/mode: run=%v done=%v plan=%v", hasRun, hasDone, hasPlanMode)
	}

	// A plan change must not generate a user/message event.
	for _, ev := range sl.Events() {
		if ev.Type == session.EventUserMessage {
			t.Fatal("/plan 不应产生 user/message 事件")
		}
	}
}

// TestCommandsUnknown returns a classified error for unknown commands.
func TestCommandsUnknown(t *testing.T) {
	reg := commands.NewRegistry()
	sl := session.NewSessionLog(brand.NewSessionID("cmd_2"))

	_, err := reg.Dispatch(context.Background(), "/nonexistent x", sl)
	if err == nil {
		t.Fatal("未知命令应报错")
	}
	if _, ok := err.(commands.ErrUnknownCommand); !ok {
		t.Fatalf("应为 ErrUnknownCommand, 实际 %T", err)
	}
}

// TestCommandsGoal verifies goal/change events with monotonically increasing revisions.
func TestCommandsGoal(t *testing.T) {
	reg := commands.NewRegistry()
	sl := session.NewSessionLog(brand.NewSessionID("cmd_3"))

	if _, err := reg.Dispatch(context.Background(), "/goal 完成任务", sl); err != nil {
		t.Fatalf("/goal 失败: %v", err)
	}
	if _, err := reg.Dispatch(context.Background(), "/goal 继续", sl); err != nil {
		t.Fatalf("/goal 二次失败: %v", err)
	}

	fold := session.FoldGoalChange(sl.Events())
	if !fold.Present {
		t.Fatal("应折叠到目标状态")
	}
	if fold.Revision != 2 {
		t.Fatalf("revision 应单调递增到 2: %d", fold.Revision)
	}
	if fold.Description != "继续" {
		t.Fatalf("description 应最新为 继续: %q", fold.Description)
	}
}

// TestCommandsListRegister verifies registration and listing.
func TestCommandsListRegister(t *testing.T) {
	reg := commands.NewRegistry()
	names := reg.List()
	if len(names) < 2 {
		t.Fatalf("内置命令应至少 plan/goal: %v", names)
	}
	// Lexical order.
	if names[0] != "goal" || names[1] != "plan" {
		t.Fatalf("命令应按字典序: %v", names)
	}
}

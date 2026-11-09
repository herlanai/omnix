// Sandbox-mode persistence tests cover event round trips, latest-event folding, absent-event
// results, SetSandboxMode appends, and source=delegation for child-agent handoffs.
package tests

import (
	"testing"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/sandbox"
	"github.com/herlanai/omnix/pkg/session"
)

// TestSandboxModeEventType verifies the sandbox/mode event type.
func TestSandboxModeEventType(t *testing.T) {
	if session.EventSandboxMode != "sandbox/mode" {
		t.Fatalf("事件类型应为 sandbox/mode, 实际 %s", session.EventSandboxMode)
	}
	data := session.SandboxModeData{Mode: string(sandbox.ModeWorkspaceWrite)}
	if data.EventType() != session.EventSandboxMode {
		t.Fatal("SandboxModeData.EventType() 应返回 EventSandboxMode")
	}
}

// TestSandboxModeRoundTrip appends and reads back sandbox/mode.
func TestSandboxModeRoundTrip(t *testing.T) {
	sl := session.NewSessionLog(brand.NewSessionID("sbox-test"))
	_, err := sl.Append(session.SandboxModeData{Mode: string(sandbox.ModeReadOnly)})
	if err != nil {
		t.Fatalf("append sandbox/mode 失败: %v", err)
	}
	evs := sl.Events()
	if len(evs) != 1 {
		t.Fatalf("应有 1 条事件, 实际 %d", len(evs))
	}
	if evs[0].Type != session.EventSandboxMode {
		t.Fatalf("事件类型应为 sandbox/mode, 实际 %s", evs[0].Type)
	}
	data, ok := evs[0].Data.(session.SandboxModeData)
	if !ok {
		t.Fatal("事件数据应为 SandboxModeData")
	}
	if data.Mode != string(sandbox.ModeReadOnly) {
		t.Fatalf("mode 应为 read-only, 实际 %s", data.Mode)
	}
}

// TestEffectiveSandboxModeFold selects the final mode event.
func TestEffectiveSandboxModeFold(t *testing.T) {
	sl := session.NewSessionLog(brand.NewSessionID("sbox-fold"))
	sl.Append(session.SandboxModeData{Mode: string(sandbox.ModeReadOnly)})
	sl.Append(session.UserMessageData{Content: "hello"})
	sl.Append(session.SandboxModeData{Mode: string(sandbox.ModeWorkspaceWrite)})
	sl.Append(session.AssistantMessageData{Content: "hi"})

	mode, ok := sandbox.EffectiveSandboxMode(sl.Events())
	if !ok {
		t.Fatal("应有 sandbox/mode 事件")
	}
	if mode != sandbox.ModeWorkspaceWrite {
		t.Fatalf("应取最后一个 workspace-write, 实际 %s", mode)
	}
}

// TestEffectiveSandboxModeNoEvent returns ok=false when no mode event exists.
func TestEffectiveSandboxModeNoEvent(t *testing.T) {
	sl := session.NewSessionLog(brand.NewSessionID("sbox-none"))
	sl.Append(session.UserMessageData{Content: "hello"})

	_, ok := sandbox.EffectiveSandboxMode(sl.Events())
	if ok {
		t.Fatal("无 sandbox/mode 事件时应返回 ok=false")
	}
}

// TestEffectiveSandboxModeEmpty handles an empty event list.
func TestEffectiveSandboxModeEmpty(t *testing.T) {
	_, ok := sandbox.EffectiveSandboxMode(nil)
	if ok {
		t.Fatal("空事件列表应返回 ok=false")
	}
}

// TestSetSandboxMode appends a mode event to the session.
func TestSetSandboxMode(t *testing.T) {
	sl := session.NewSessionLog(brand.NewSessionID("sbox-set"))
	err := sandbox.SetSandboxMode(sl, sandbox.ModeDangerFullAccess)
	if err != nil {
		t.Fatalf("SetSandboxMode 失败: %v", err)
	}
	evs := sl.Events()
	if len(evs) != 1 {
		t.Fatalf("应有 1 条事件, 实际 %d", len(evs))
	}
	if evs[0].Type != session.EventSandboxMode {
		t.Fatalf("事件类型应为 sandbox/mode, 实际 %s", evs[0].Type)
	}
	data := evs[0].Data.(session.SandboxModeData)
	if data.Mode != string(sandbox.ModeDangerFullAccess) {
		t.Fatalf("mode 应为 danger-full-access, 实际 %s", data.Mode)
	}
}

// TestSetSandboxModeDelegation preserves source=delegation.
func TestSetSandboxModeDelegation(t *testing.T) {
	sl := session.NewSessionLog(brand.NewSessionID("sbox-deleg"))
	err := sandbox.SetSandboxModeWithSource(sl, sandbox.ModeWorkspaceWrite, sandbox.ModeSourceDelegation)
	if err != nil {
		t.Fatalf("SetSandboxModeWithSource 失败: %v", err)
	}
	data := sl.Events()[0].Data.(session.SandboxModeData)
	if data.Source != string(sandbox.ModeSourceDelegation) {
		t.Fatalf("source 应为 delegation, 实际 %q", data.Source)
	}
}

// TestSandboxModeSessionInterface derives SessionLog.SandboxMode from events.
func TestSandboxModeSessionInterface(t *testing.T) {
	sl := session.NewSessionLog(brand.NewSessionID("sbox-iface"))
	sl.Append(session.SandboxModeData{Mode: string(sandbox.ModeReadOnly)})

	mode, ok := sl.SandboxMode()
	if !ok {
		t.Fatal("SessionLog.SandboxMode() 应从事件 fold 返回")
	}
	if string(mode) != string(sandbox.ModeReadOnly) {
		t.Fatalf("mode 应为 read-only, 实际 %s", mode)
	}
}

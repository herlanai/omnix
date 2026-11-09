// S10 cross-platform terminal tests cover spawn/read/wait with reason exited, exclusive active sessions per Agent, bounded scrollback, and send/read/close interaction. Generate scripts by runtime.GOOS for Windows and Unix.
package tests

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/herlanai/omnix/pkg/terminal"
)

// TestTerminalSpawnReadWait verifies spawn, output reads, and wait reason exited.
func TestTerminalSpawnReadWait(t *testing.T) {
	ctx := context.Background()
	name, args := terminal.ShellCommand(echoScript("TERM_MARKER_123"))
	backend := terminal.NewBackend(terminal.TerminalConfig{Command: name, Args: args})
	sess, err := backend.Spawn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	if !waitForOutput(t, sess, "TERM_MARKER_123", 3*time.Second) {
		t.Fatalf("未读到期望输出 TERM_MARKER_123，回滚=%q", sess.ReadString())
	}
	if reason := sess.Wait(); reason != terminal.WaitExited {
		t.Fatalf("wait reason 应为 exited，实际 %s", reason)
	}
}

// TestTerminalBoundedScrollback retains only the latest MaxLines lines.
func TestTerminalBoundedScrollback(t *testing.T) {
	ctx := context.Background()
	name, args := terminal.ShellCommand(lineLoopScript("line-", 20))
	backend := terminal.NewBackend(terminal.TerminalConfig{Command: name, Args: args, MaxLines: 5})
	sess, err := backend.Spawn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	_ = sess.Wait()

	if lines := sess.Read(); len(lines) > 5 {
		t.Fatalf("有界回滚应 ≤5 行，实际 %d 行", len(lines))
	}
	if !strings.Contains(strings.Join(sess.Read(), "\n"), "line-20") {
		t.Fatalf("应保留最新行 line-20，实际 %q", sess.ReadString())
	}
}

// TestTerminalExclusive rejects another spawn while the Agent has an active terminal.
func TestTerminalExclusive(t *testing.T) {
	ctx := context.Background()
	name, args := sleepScript()
	backend := terminal.NewBackend(terminal.TerminalConfig{Command: name, Args: args})
	sess, err := backend.Spawn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if _, err := backend.Spawn(ctx); err == nil {
		t.Fatal("活跃 session 存在时再次 spawn 应被拒绝（单 agent 独占）")
	}
}

// TestTerminalInteractiveSendRead exercises send/read/close and skips unavailable interactive
// shells.
func TestTerminalInteractiveSendRead(t *testing.T) {
	name, args := interactiveShell()
	if name == "" {
		t.Skip("当前平台无可用交互 shell")
	}
	ctx := context.Background()
	backend := terminal.NewBackend(terminal.TerminalConfig{Command: name, Args: args})
	sess, err := backend.Spawn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	if err := sess.SendLine("echo INTERACTIVE_OK"); err != nil {
		t.Fatal(err)
	}
	if !waitForOutput(t, sess, "INTERACTIVE_OK", 4*time.Second) {
		t.Fatalf("未读到交互输出 INTERACTIVE_OK，回滚=%q", sess.ReadString())
	}
	_ = sess.SendLine("exit")
}

// Cross-platform script helpers.

// echoScript prints the supplied string.
func echoScript(text string) string {
	esc := strings.ReplaceAll(text, `"`, `""`)
	if runtime.GOOS == "windows" {
		return "echo " + esc
	}
	return "echo \"" + esc + "\""
}

// lineLoopScript prints lines 1 through n using cmd for /L on Windows and sh while on Unix.
func lineLoopScript(prefix string, n int) string {
	if runtime.GOOS == "windows" {
		return "for /L %i in (1,1," + itoa(n) + ") do @echo " + prefix + "%i"
	}
	return "i=1; while [ $i -le " + itoa(n) + " ]; do echo " + prefix + "$i; i=$((i+1)); done"
}

// itoa converts an integer to decimal for tests.
func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

// sleepScript runs for approximately one second.
func sleepScript() (string, []string) {
	if runtime.GOOS == "windows" {
		return "powershell", []string{"-NoProfile", "-Command", "Start-Sleep -Seconds 1"}
	}
	return "sh", []string{"-c", "sleep 1"}
}

// interactiveShell returns a REPL command, or an empty string when unavailable.
func interactiveShell() (string, []string) {
	if runtime.GOOS == "windows" {
		// cmd.exe supports interactive commands through stdin.
		return "cmd", nil
	}
	return "sh", nil
}

// waitForOutput polls scrollback until a substring appears or the timeout expires.
func waitForOutput(t *testing.T, sess *terminal.Session, substr string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if sess.Contains(substr) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return sess.Contains(substr)
}

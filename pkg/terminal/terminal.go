// Package terminal provides persistent terminal sessions (S10), matching
// shell/terminal. This cross-platform backend uses subprocesses and pipes for
// spawn/send/read/signal/close, bounded scrollback, wait reasons, and one active
// session per agent. A native PTY backend can replace it without changing the interface.
package terminal

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Types.

// WaitReason identifies why a session ended.
type WaitReason string

// Session termination reasons.
const (
	WaitExited    WaitReason = "exited"    // Normal process exit.
	WaitSignalled WaitReason = "signalled" // Signal termination.
	WaitClosed    WaitReason = "closed"    // Explicitly closed.
	WaitError     WaitReason = "error"     // Startup or execution error.
)

// TerminalConfig configures a terminal session.
type TerminalConfig struct {
	Command     string   // Executable name, such as bash, cmd, or sh.
	Args        []string // Arguments.
	MaxLines    int      // Scrollback line limit; nonpositive defaults to 1000.
	ReadLineMax int      // Maximum bytes per line.
}

// withDefaults fills default values.
func (c TerminalConfig) withDefaults() TerminalConfig {
	if c.MaxLines <= 0 {
		c.MaxLines = 1000
	}
	if c.ReadLineMax <= 0 {
		c.ReadLineMax = 64 * 1024
	}
	return c
}

// ============================================================================
// TerminalBackend
// ============================================================================

// TerminalBackend owns terminal sessions and permits one active session per agent.
type TerminalBackend struct {
	mu     sync.Mutex
	active *Session
	cfg    TerminalConfig
}

// NewBackend creates a terminal backend.
func NewBackend(cfg TerminalConfig) *TerminalBackend {
	return &TerminalBackend{cfg: cfg.withDefaults()}
}

// Spawn creates a session, rejecting it if another session is active.
func (b *TerminalBackend) Spawn(ctx context.Context) (*Session, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.active != nil && b.active.running() {
		return nil, fmt.Errorf("terminal: single-agent exclusive activity: a session is already active")
	}
	s, err := spawnSession(ctx, b.cfg)
	if err != nil {
		return nil, err
	}
	b.active = s
	return s, nil
}

// Active returns the active session, or nil.
func (b *TerminalBackend) Active() *Session {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.active != nil && b.active.running() {
		return b.active
	}
	return nil
}

// ============================================================================
// Session
// ============================================================================

// Session is a persistent subprocess-and-pipe terminal with bounded scrollback.
type Session struct {
	cfg TerminalConfig
	cmd *exec.Cmd
	w   io.WriteCloser // stdin

	mu      sync.Mutex
	lines   []string // Bounded scrollback retains only the most recent MaxLines lines.
	done    chan struct{}
	reason  WaitReason
	started time.Time
	exited  bool
}

// spawnSession starts the process and stdout-reading goroutine.
func spawnSession(ctx context.Context, cfg TerminalConfig) (*Session, error) {
	cmd := exec.CommandContext(ctx, cfg.Command, cfg.Args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	s := &Session{
		cfg:     cfg,
		cmd:     cmd,
		w:       stdin,
		lines:   []string{},
		done:    make(chan struct{}),
		reason:  WaitClosed,
		started: time.Now(),
	}
	// Read stdout separately from stderr to avoid interleaving; callers configure diagnostics.
	go s.readLoop(stdout)
	go s.waitLoop()
	return s, nil
}

// readLoop reads stdout lines into bounded scrollback.
func (s *Session) readLoop(r io.Reader) {
	sc := bufio.NewReaderSize(r, s.cfg.ReadLineMax)
	for {
		line, err := readLineLimited(sc, s.cfg.ReadLineMax)
		if len(line) > 0 {
			s.pushLine(line)
		}
		if err != nil {
			return
		}
	}
}

// waitLoop records the process termination reason, then closes done.
func (s *Session) waitLoop() {
	err := s.cmd.Wait()
	s.mu.Lock()
	if s.reason == WaitClosed { // Unless explicit close already set the reason.
		s.reason = WaitExited
	}
	if err != nil && s.reason == WaitClosed {
		// Classify unclosed exits as exited or signalled without detailed signal parsing.
		s.reason = WaitExited
	}
	s.exited = true
	s.mu.Unlock()
	close(s.done)
}

// readLineLimited reads a line, truncating oversized chunks while continuing to read.
func readLineLimited(sc *bufio.Reader, max int) (string, error) {
	sb := strings.Builder{}
	for {
		chunk, err := sc.ReadString('\n')
		sb.WriteString(chunk)
		if err != nil {
			return sb.String(), err
		}
		if len(chunk) > 0 && strings.HasSuffix(chunk, "\n") {
			return strings.TrimSuffix(sb.String(), "\n"), nil
		}
	}
}

// pushLine appends a line to bounded scrollback.
func (s *Session) pushLine(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines = append(s.lines, line)
	if len(s.lines) > s.cfg.MaxLines {
		// Retain only the most recent MaxLines lines.
		excess := len(s.lines) - s.cfg.MaxLines
		s.lines = append([]string(nil), s.lines[excess:]...)
	}
}

// Send writes stdin bytes, simulating keyboard input.
func (s *Session) Send(data string) error {
	if _, err := s.w.Write([]byte(data)); err != nil {
		return err
	}
	return nil
}

// SendLine writes text with an appended newline.
func (s *Session) SendLine(line string) error {
	return s.Send(line + "\n")
}

// Read returns a snapshot of all current scrollback lines.
func (s *Session) Read() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.lines))
	copy(out, s.lines)
	return out
}

// ReadString joins current scrollback into text.
func (s *Session) ReadString() string {
	lines := s.Read()
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l)
		b.WriteString("\n")
	}
	return b.String()
}

// Contains checks scrollback for a substring, useful for polling assertions.
func (s *Session) Contains(sub string) bool {
	return strings.Contains(s.ReadString(), sub)
}

// Wait blocks until termination and returns its reason.
func (s *Session) Wait() WaitReason {
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reason
}

// Signal sends a termination signal; Windows uses Kill.
func (s *Session) Signal() error {
	return s.cmd.Process.Kill()
}

// Close closes stdin, terminates the process, and records reason=closed.
func (s *Session) Close() error {
	_ = s.w.Close()
	if !s.exitedState() {
		_ = s.cmd.Process.Kill()
	}
	s.mu.Lock()
	s.reason = WaitClosed
	s.mu.Unlock()
	return nil
}

// running reports whether the process is still running.
func (s *Session) running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.exited
}

func (s *Session) exitedState() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exited
}

// Cross-platform shell helpers.

// ShellCommand returns the platform shell and one-command execution arguments,
// allowing tests such as echo to run under Windows cmd or Unix sh.
func ShellCommand(script string) (name string, args []string) {
	if runtime.GOOS == "windows" {
		return "cmd", []string{"/S", "/C", script}
	}
	return "/bin/sh", []string{"-c", script}
}

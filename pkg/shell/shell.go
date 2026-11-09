// Package shell implements shell execution and tool-bash, matching shell/shell,
// bash-local, bash-sandbox, and tool-bash (M37). Requests resolve to explicit specs.
// Results keep exitCode, signal, timedOut, aborted, and timeoutMs independent.
// Foreground execution uses subprocess with a 64 KB inline limit and complete
// spill streams; background execution uses jobs.
package shell

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/herlanai/omnix/pkg/subprocess"
)

// Requests, specifications, and results.

// ExecRequest describes a shell execution request.
type ExecRequest struct {
	// Command is the command text to execute.
	Command string
	// Cwd is the working directory; empty inherits it.
	Cwd string
	// TimeoutMs is the timeout in milliseconds; nonpositive disables it.
	TimeoutMs int
	// Env adds environment variables.
	Env map[string]string
	// Background execution is selected by the caller through ShellTool.
}

// ExecSpec is the fully resolved execution specification.
type ExecSpec struct {
	// Argv wraps the command in bash -c, cmd /c, or PowerShell.
	Argv []string
	// Cwd is the working directory.
	Cwd string
	// TimeoutMs is the timeout.
	TimeoutMs int
	// Env adds environment variables.
	Env map[string]string
	// SandboxMode is the effective read-only, workspace-write, or danger mode.
	SandboxMode string
}

// RunResult records five independent execution outcome fields.
type RunResult struct {
	// ExitCode may be -1 for signal termination or abnormal exit.
	ExitCode int    `json:"exitCode"`
	Signal   string `json:"signal,omitempty"`
	// TimedOut indicates forced termination due to timeout.
	TimedOut bool `json:"timedOut"`
	// Aborted indicates termination due to cancellation.
	Aborted bool `json:"aborted"`
	// TimeoutMs is the configured timeout in milliseconds; zero means unset.
	TimeoutMs int `json:"timeoutMs"`
}

// BashResult is the model-facing tool-bash result.
type BashResult struct {
	// Output is inline output, potentially truncated to its tail.
	Output string `json:"output"`
	// Truncated indicates output truncation.
	Truncated bool `json:"truncated"`
	// SpillPath points to the complete stream when truncated.
	SpillPath string `json:"spillPath,omitempty"`
	// Result records execution facts.
	Result RunResult `json:"result"`
}

// Shell resolution and execution.

// Shell executes shell commands.
type Shell struct {
	rt      *subprocess.LocalRuntime
	sandbox string // Effective sandbox mode.
}

// New creates a shell with read-only, workspace-write, or danger mode; empty defaults to workspace-write.
func New(sandboxMode string) *Shell {
	mode := sandboxMode
	if mode == "" {
		mode = "workspace-write"
	}
	return &Shell{rt: subprocess.NewLocal(), sandbox: mode}
}

// Resolve converts a command into an execution specification.
func (s *Shell) Resolve(req ExecRequest) ExecSpec {
	argv := shellArgv(req.Command)
	timeout := req.TimeoutMs
	if timeout < 0 {
		timeout = 0
	}
	return ExecSpec{
		Argv:        argv,
		Cwd:         req.Cwd,
		TimeoutMs:   timeout,
		Env:         req.Env,
		SandboxMode: s.sandbox,
	}
}

// shellArgv wraps commands for the platform without nested shell interpretation.
func shellArgv(cmd string) []string {
	switch runtime.GOOS {
	case "windows":
		return []string{"cmd", "/c", cmd}
	default:
		return []string{"bash", "-c", cmd}
	}
}

// Run waits for foreground execution and returns outcome and collected output.
func (s *Shell) Run(req ExecRequest) (RunResult, BashResult, error) {
	spec := s.Resolve(req)
	spillRoot := tmpDir()

	memCap := 64 * 1024
	h := s.rt.Spawn(subprocess.SubprocessSpawnSpec{
		Argv:           spec.Argv,
		Cwd:            spec.Cwd,
		StdoutMaxBytes: memCap,
		SpillRoot:      spillRoot,
		GraceMs:        500,
		Env:            spec.Env,
	})

	// Wait for completion or timeout.
	var res RunResult
	res.TimeoutMs = spec.TimeoutMs
	timedCh := make(chan struct{})
	if spec.TimeoutMs > 0 {
		time.AfterFunc(time.Duration(spec.TimeoutMs)*time.Millisecond, func() { close(timedCh) })
	}

	var outcome subprocess.SubprocessOutcome
	select {
	case outcome = <-h.Done():
		res.ExitCode = outcome.ExitCode
		res.Signal = outcome.Signal
	case <-timedCh:
		// On timeout, terminate the process tree and mark TimedOut.
		h.Terminate()
		res.TimedOut = true
		res.ExitCode = -1
		<-h.Done()
	}

	co := h.Collected().Stdout().ReadOutput(0)
	br := BashResult{
		Output:    co.Text,
		Truncated: co.Truncated,
		SpillPath: co.SpillPath,
		Result:    res,
	}
	return res, br, nil
}

func tmpDir() string {
	dir, err := os.MkdirTemp("", "omnix-shell-spill-*")
	if err != nil {
		return os.TempDir()
	}
	return dir
}

// Model-facing tool-bash entry point.

// BashTool implements the tool using Shell.
type BashTool struct {
	shell *Shell
}

// NewBashTool creates a Bash tool.
func NewBashTool(sandboxMode string) *BashTool {
	return &BashTool{shell: New(sandboxMode)}
}

// Exec executes a command and returns a model-facing result.
func (b *BashTool) Exec(req ExecRequest) (*BashResult, error) {
	_, br, err := b.shell.Run(req)
	if err != nil {
		return nil, err
	}
	return &br, nil
}

// FormatOutput formats model-facing text with spill-reading guidance when truncated.
func FormatOutput(r *BashResult) string {
	if !r.Truncated {
		return r.Output
	}
	return fmt.Sprintf("%s\n...[truncated; full output at %s]", strings.TrimRight(r.Output, "\n"), r.SpillPath)
}

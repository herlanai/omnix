// Package subprocess implements the M36 subprocess interface, matching subprocess
// and subprocess-local. Specs explicitly describe argv, cwd, stdio, grace, and env.
// Spawn removes inherited OMNIX_* variables, then merges explicit environment
// entries; empty-value tombstones remove ordinary inherited variables too.
// CollectedOutput retains the tail beyond memory limits and may spill the full
// stream. Terminate acts on the process tree: taskkill /T on Windows, or SIGTERM,
// a grace period, then SIGKILL on Unix.
package subprocess

import (
	"bytes"
	"sync"
)

// Types.

// CollectedOutput contains captured text and recovery information.
type CollectedOutput struct {
	// Text contains the collected stream tail when truncated.
	Text string `json:"text"`
	// Truncated reports whether bytes were discarded from Text.
	Truncated bool `json:"truncated"`
	// SpillPath points to the full stream when truncation and spilling are available.
	SpillPath string `json:"spillPath,omitempty"`
}

// SubprocessCollect configures bounded in-memory collection and optional full-stream spilling.
type SubprocessCollect struct {
	// MaxBytes limits memory; overflow retains the tail.
	MaxBytes int
	// SpillMaxBytes limits full-stream storage; nonpositive disables spilling.
	SpillMaxBytes int
}

// SubprocessStdinMode selects ignored stdin or a piped stream.
type SubprocessStdinMode string

const (
	StdinIgnore SubprocessStdinMode = "ignore"
	StdinPipe   SubprocessStdinMode = "pipe"
	// StdinData writes bytes then closes stdin for batch execution.
	StdinData = "data"
)

// SubprocessSpawnSpec explicitly describes a spawn request.
type SubprocessSpawnSpec struct {
	// Argv contains executable argv[0] and arguments without shell interpretation.
	Argv []string
	// Cwd is the child working directory.
	Cwd string
	// StdoutMaxBytes and StderrMaxBytes default to 64 KB when nonpositive.
	StdoutMaxBytes int
	StderrMaxBytes int
	// SpillRoot is the complete-stream spill directory; empty disables spilling.
	SpillRoot string
	// GraceMs is the SIGTERM-to-SIGKILL grace period in milliseconds.
	GraceMs int
	// Env overlays explicit entries on the scrubbed parent environment.
	Env map[string]string
	// StdinData optionally supplies input and closes stdin when nonempty.
	StdinData string
}

// SubprocessOutcome records exit facts; callers classify their own timeout/cancellation signals.
type SubprocessOutcome struct {
	// ExitCode is -1 when a signal terminates the process.
	ExitCode int
	// Signal names the terminating signal, such as SIGTERM; empty on normal exit.
	Signal string
}

// SubprocessHandle represents a live process and its descendant tree.
type SubprocessHandle struct {
	// Pid identifies the process-tree root.
	Pid int
	// collected provides offset-based, nonconsuming output reads.
	collected *collectedOutputs
	// done reports exit facts; only spawn failures use the error channel.
	done        chan SubprocessOutcome
	doneErr     chan error
	termOnce    sync.Once
	terminateFn func()
}

// Collected returns output readers that remain usable after exit.
func (h *SubprocessHandle) Collected() *collectedOutputs { return h.collected }

// Done returns process exit facts when it closes.
func (h *SubprocessHandle) Done() <-chan SubprocessOutcome { return h.done }

// Terminate escalates SIGTERM through grace to SIGKILL for the tree; Windows forces
// termination immediately. It is idempotent and does nothing after the tree exits.
func (h *SubprocessHandle) Terminate() {
	h.termOnce.Do(h.terminateFn)
}

// Collected-output readers.

// collectedOutputs holds captured stdout and stderr.
type collectedOutputs struct {
	stdout *outputReader
	stderr *outputReader
}

// Stdout returns the stdout reader when collection was enabled.
func (c *collectedOutputs) Stdout() *outputReader { return c.stdout }

// Stderr returns the stderr reader.
func (c *collectedOutputs) Stderr() *outputReader { return c.stderr }

// outputReader uses caller-owned whole-stream byte offsets. Independent readers
// do not consume each other's data; readFrom(0) retrieves the finalized batch result.
type outputReader struct {
	mu        sync.Mutex
	trunc     bytes.Buffer // Retained tail, or the whole stream before truncation.
	fullSpill string       // Full-stream spill path.
	truncated bool         // Whether output exceeded the in-memory window.
	total     int          // Total whole-stream byte count.
}

func (r *outputReader) readFrom(fromByte int) (text string, nextOffset int, lossy bool, spillPath string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.truncated {
		// Offsets before the retained window are lossy; return the entire retained tail.
		lossy = true
		return r.trunc.String(), r.total, lossy, r.fullSpill
	}
	cur := r.trunc.Bytes()
	if fromByte >= len(cur) {
		return "", fromByte, false, r.fullSpill
	}
	delta := cur[fromByte:]
	return string(delta), fromByte + len(delta), false, r.fullSpill
}

// ReadOutput performs a convenient incremental read.
func (r *outputReader) ReadOutput(from int) CollectedOutput {
	text, _, lossy, spill := r.readFrom(from)
	return CollectedOutput{Text: text, Truncated: lossy, SpillPath: spill}
}

// append adds bytes, retaining the tail and optionally spilling when memory is exceeded.
func (r *outputReader) append(data []byte, memCap, spillCap int, spillDir string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(data) == 0 {
		return
	}
	r.total += len(data)
	switch {
	case r.truncated:
		// After truncation, append new bytes to spill storage and refresh the tail.
		if spillDir != "" {
			r.fullSpill = writeSpillFile(spillDir, r.fullSpill, data)
		}
		r.appendTail(data, memCap)
	default:
		if r.trunc.Len()+len(data) <= memCap {
			r.trunc.Write(data)
			return
		}
		// On overflow, spill the complete stream if enabled and refresh the tail.
		if spillDir != "" {
			r.fullSpill = writeSpillFile(spillDir, r.fullSpill, r.trunc.Bytes())
			r.trunc.Reset()
			r.truncated = true
			r.fullSpill = writeSpillFile(spillDir, r.fullSpill, data)
		} else {
			r.truncated = true
		}
		r.appendTail(data, memCap)
	}
}

// appendTail retains the last memCap bytes in truncated mode.
func (r *outputReader) appendTail(data []byte, memCap int) {
	r.trunc.Write(data)
	if r.trunc.Len() <= memCap {
		return
	}
	over := r.trunc.Len() - memCap
	buf := r.trunc.Bytes()
	r.trunc.Reset()
	r.trunc.Write(buf[over:])
}

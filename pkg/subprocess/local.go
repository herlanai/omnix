// Local subprocess implementation using os/exec, process-tree termination, environment scrubbing, and spills.
package subprocess

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// OmnixEnvPrefix identifies managed environment variables removed before explicit overrides.
const OmnixEnvPrefix = "OMNIX_"

// writeSpillFile appends data to an existing spill file or creates one within
// the limit, returning its path.
func writeSpillFile(dir, existing string, data []byte) string {
	if existing == "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return ""
		}
		f, err := os.CreateTemp(dir, "spill-*.txt")
		if err != nil {
			return ""
		}
		path := f.Name()
		if _, err := f.Write(data); err != nil {
			_ = f.Close()
			return path
		}
		_ = f.Close()
		return path
	}
	f, err := os.OpenFile(existing, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return existing
	}
	_, _ = f.Write(data)
	_ = f.Close()
	return existing
}

// ScrubParentEnv removes OMNIX_* variables while preserving other environment entries.
func ScrubParentEnv() []string {
	return scrubEnv(os.Environ())
}

func scrubEnv(applied []string) []string {
	out := make([]string, 0, len(applied))
	for _, kv := range applied {
		// Remove OMNIX_* entries using the first equals sign as the separator.
		if strings.HasPrefix(kv, OmnixEnvPrefix) {
			continue
		}
		// Discard malformed entries without an equals separator.
		if !strings.Contains(kv, "=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// mergeEnv overlays explicit entries on base; empty values are removal tombstones.
func mergeEnv(base []string, overrides map[string]string) []string {
	m := make(map[string]string, len(base))
	for _, kv := range base {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			m[kv[:i]] = kv[i+1:]
		}
	}
	for k, v := range overrides {
		if v == "" {
			delete(m, k)
		} else {
			m[k] = v
		}
	}
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}

// LocalRuntime executes local subprocesses through os/exec.
type LocalRuntime struct{}

// NewLocal creates a local runtime.
func NewLocal() *LocalRuntime { return &LocalRuntime{} }

// Spawn starts a managed subprocess and immediately returns a handle.
func (r *LocalRuntime) Spawn(spec SubprocessSpawnSpec) *SubprocessHandle {
	stdoutR := &outputReader{}
	stderrR := &outputReader{}
	col := &collectedOutputs{stdout: stdoutR, stderr: stderrR}

	cmd := exec.Command(spec.Argv[0], spec.Argv[1:]...)
	cmd.Dir = spec.Cwd
	if spec.GraceMs <= 0 {
		spec.GraceMs = 2000
	}
	// Scrub the inherited environment before merging explicit entries.
	cmd.Env = mergeEnv(ScrubParentEnv(), spec.Env)
	// Create a separate process group for tree termination.
	if runtime.GOOS != "windows" {
		// Set the process group ID for group-wide SIGTERM in terminate.
		platformSetSysProcAttr(cmd)
	}

	var stdin io.WriteCloser
	if spec.StdinData != "" {
		cmd.Stdin = strings.NewReader(spec.StdinData)
	} else {
		stdin, _ = cmd.StdinPipe()
	}

	// Let os/exec own output copying: Wait must drain both streams before completion.
	memOut, memErr := spec.StdoutMaxBytes, spec.StderrMaxBytes
	if memOut <= 0 {
		memOut = 64 * 1024
	}
	if memErr <= 0 {
		memErr = 64 * 1024
	}
	cmd.Stdout = &outputWriter{reader: stdoutR, memCap: memOut, spillRoot: spec.SpillRoot}
	cmd.Stderr = &outputWriter{reader: stderrR, memCap: memErr, spillRoot: spec.SpillRoot}

	startErr := cmd.Start()
	pid := -1
	if startErr == nil {
		pid = cmd.Process.Pid
	}

	done := make(chan SubprocessOutcome, 1)
	doneErr := make(chan error, 1)
	h := &SubprocessHandle{
		Pid:       pid,
		collected: col,
		done:      done,
		doneErr:   doneErr,
		terminateFn: func() {
			if cmd.Process == nil {
				return
			}
			killTree(cmd, spec.GraceMs)
		},
	}
	_ = stdin // The caller owns the stdin pipe; this simplified implementation does not expose it.

	// Record startup failure immediately in done/doneErr.
	if startErr != nil {
		doneErr <- fmt.Errorf("subprocess: spawn %q: %w", spec.Argv[0], startErr)
		close(doneErr)
		return h
	}

	// Wait for process exit.
	go func() {
		wErr := cmd.Wait()
		res := SubprocessOutcome{}
		if wErr == nil {
			res.ExitCode = 0
		} else if ee, ok := wErr.(*exec.ExitError); ok {
			res.ExitCode = ee.ExitCode()
			// Unix obtains signals through ExitError.Sys; Windows has no signal value.
			if sig := platformExitSignal(ee); sig != "" {
				res.Signal = sig
				res.ExitCode = -1
			}
		} else {
			doneErr <- wErr
			close(doneErr)
			return
		}
		done <- res
		close(done)
	}()
	return h
}

// pipeToReader copies a stream while enforcing memory limits and spill handling.
func pipeToReader(r io.Reader, rd *outputReader, memCap int, spillRoot string) {
	buf := make([]byte, 16*1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			rd.append(buf[:n], memCap, spillCapFor(spillRoot), spillRoot)
		}
		if err != nil {
			return
		}
	}
}

func spillCapFor(root string) int {
	if root == "" {
		return 0
	}
	return 8 << 20 // Eight-megabyte complete-stream limit.
}

// killTree escalates termination across a process tree.
func killTree(cmd *exec.Cmd, graceMs int) {
	pid := cmd.Process.Pid
	if pid <= 0 {
		return
	}
	if runtime.GOOS == "windows" {
		// Windows immediately forces tree termination with taskkill /T.
		_ = exec.Command("taskkill", "/T", "/F", "/PID", fmt.Sprint(pid)).Run()
		return
	}
	// Unix sends SIGTERM, waits through the grace period, then SIGKILL to the group, including descendants.
	_ = platformSignalGroup(-pid, syscallSIGTERM)
	grace := time.Duration(graceMs) * time.Millisecond
	time.Sleep(grace)
	_ = platformSignalGroup(-pid, syscallSIGKILL)
}

// Platform files implement process attributes, exit signals, and group signaling,
// including platform-specific SIGTERM and SIGKILL constants.
type outputWriter struct {
	reader    *outputReader
	memCap    int
	spillRoot string
}

func (w *outputWriter) Write(data []byte) (int, error) {
	w.reader.append(data, w.memCap, spillCapFor(w.spillRoot), w.spillRoot)
	return len(data), nil
}

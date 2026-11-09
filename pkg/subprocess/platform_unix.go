//go:build !windows

// Unix process groups, exit-signal collection, and group signaling.
package subprocess

import (
	"os/exec"
	"syscall"
)

const (
	syscallSIGTERM = int(syscall.SIGTERM)
	syscallSIGKILL = int(syscall.SIGKILL)
)

// platformSetSysProcAttr creates a separate process group for group-wide termination.
func platformSetSysProcAttr(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// platformExitSignal returns the terminating signal name, or empty for nonsignal exits.
func platformExitSignal(ee *exec.ExitError) string {
	if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return ws.Signal().String()
	}
	return ""
}

// platformSignalGroup signals the group using a negative PID.
func platformSignalGroup(pid, sig int) error {
	_ = sig
	return syscall.Kill(pid, syscall.Signal(sig))
}

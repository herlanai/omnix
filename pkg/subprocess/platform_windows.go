//go:build windows

// Windows uses new process groups and taskkill tree termination without signal names.
package subprocess

import (
	"os/exec"
	"syscall"
)

// Windows has no SIGTERM/SIGKILL equivalent here; taskkill /T terminates the tree.
const (
	syscallSIGTERM = 0
	syscallSIGKILL = 0
)

// platformSetSysProcAttr uses CREATE_NEW_PROCESS_GROUP so taskkill can terminate
// the process tree from its root.
func platformSetSysProcAttr(cmd *exec.Cmd) {
	if cmd.SysProcAttr != nil {
		cmd.SysProcAttr.CreationFlags |= syscall.CREATE_NEW_PROCESS_GROUP
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// platformExitSignal returns empty on Windows; exit codes determine outcomes.
func platformExitSignal(*exec.ExitError) string { return "" }

// platformSignalGroup is unused on Windows; killTree uses taskkill /T.
func platformSignalGroup(int, int) error { return nil }

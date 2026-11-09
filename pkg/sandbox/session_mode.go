// Session sandbox overrides are persisted through sandbox/mode events,
// following upstream sandbox-policy/session-mode.ts. These log-only events
// stay out of the model transcript; the latest mode overrides deployment
// defaults. Replay restores state without cross-session configuration.
// The delegation source marks overrides installed for subagents; policy
// resolution consumes the same persisted state.
package sandbox

import (
	"github.com/herlanai/omnix/pkg/session"
)

// ModeSource identifies the origin of a sandbox/mode event.
type ModeSource string

const (
	// ModeSourceDelegation marks an override installed during subagent delegation.
	ModeSourceDelegation ModeSource = "delegation"
)

// SandboxModes lists valid modes for option advertising and runtime validation.
var SandboxModes = []SandboxMode{
	ModeReadOnly,
	ModeWorkspaceWrite,
	ModeDangerFullAccess,
}

// EffectiveSandboxMode scans backward for the latest sandbox/mode event,
// returning ok false if absent. It is a pure fold, so resume requires only
// replaying the log rather than a separate catch-up mechanism.
func EffectiveSandboxMode(events []session.SessionEvent) (SandboxMode, bool) {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != session.EventSandboxMode {
			continue
		}
		data, ok := events[i].Data.(session.SandboxModeData)
		if !ok {
			continue
		}
		return SandboxMode(data.Mode), true
	}
	return "", false
}

// SessionAppender abstracts event appends and is implemented by SessionLog.
type SessionAppender interface {
	Append(data session.EventData) (uint64, error)
}

// SetSandboxMode is the sole write path for session overrides: append a
// sandbox/mode event without out-of-band state. Consumers fold the log for
// each Bash/filesystem call, so the change affects the next invocation.
func SetSandboxMode(sess SessionAppender, mode SandboxMode) error {
	_, err := sess.Append(session.SandboxModeData{Mode: string(mode)})
	return err
}

// SetSandboxModeWithSource also records provenance, such as delegation.
func SetSandboxModeWithSource(sess SessionAppender, mode SandboxMode, source ModeSource) error {
	_, err := sess.Append(session.SandboxModeData{Mode: string(mode), Source: string(source)})
	return err
}

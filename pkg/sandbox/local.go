// LocalProvider follows upstream sandbox-local. Backend probing tries bwrap
// then landlock on Linux, seatbelt on macOS, and windows-acl on Windows.
// It runs a read-only profile with a true command and caches probe results.
// Confine builds backend-specific arguments and denial signatures: EROFS for
// bwrap, EACCES for landlock, and EPERM for seatbelt. RunnerCommand overrides
// skip probing and use the configured runner directly.
package sandbox

import (
	"os/exec"
	"runtime"
	"sync"
)

// BackendType identifies a local sandbox backend.
type BackendType string

const (
	// BackendBwrap selects Linux Bubblewrap.
	BackendBwrap BackendType = "bwrap"
	// BackendLandlock selects Linux Landlock.
	BackendLandlock BackendType = "landlock"
	// BackendSeatbelt selects macOS Seatbelt.
	BackendSeatbelt BackendType = "seatbelt"
	// BackendWindowsACL selects Windows ACL confinement.
	BackendWindowsACL BackendType = "windows-acl"
	// BackendUnavailable indicates no usable local backend.
	BackendUnavailable BackendType = "unavailable"
)

// LocalConfig configures LocalProvider.
type LocalConfig struct {
	// RunnerCommand overrides detection, for example bwrap or sandbox-exec.
	// A nonempty command is used directly without probing.
	RunnerCommand string
	// BackendType selects the backend explicitly with RunnerCommand.
	BackendType BackendType
}

// LocalProvider implements SandboxProvider by probing supported backends
// for the current platform.
type LocalProvider struct {
	config  LocalConfig
	once    sync.Once
	backend BackendType
	runner  string
}

// NewLocalProvider creates a local provider; nil config enables automatic detection.
func NewLocalProvider(config *LocalConfig) *LocalProvider {
	p := &LocalProvider{}
	if config != nil {
		p.config = *config
	}
	return p
}

// detectBackend probes the backend chain and returns the backend and runner.
// Probe results are cached.
func (p *LocalProvider) detectBackend() (BackendType, string) {
	p.once.Do(func() {
		// Use an explicitly configured backend directly.
		if p.config.RunnerCommand != "" && p.config.BackendType != "" {
			p.backend = p.config.BackendType
			p.runner = p.config.RunnerCommand
			return
		}
		// Probe according to platform.
		switch runtime.GOOS {
		case "linux":
			// Linux probes bwrap before landlock.
			if p.tryBwrap() {
				p.backend = BackendBwrap
				p.runner = "bwrap"
				return
			}
			if p.tryLandlock() {
				p.backend = BackendLandlock
				p.runner = "landlock-launcher"
				return
			}
		case "darwin":
			// macOS probes seatbelt.
			if p.trySeatbelt() {
				p.backend = BackendSeatbelt
				p.runner = "sandbox-exec"
				return
			}
		case "windows":
			// Windows selects windows-acl with token-based execution.
			p.backend = BackendWindowsACL
			p.runner = "windows-acl"
			return
		}
		p.backend = BackendUnavailable
		p.runner = ""
	})
	return p.backend, p.runner
}

// tryBwrap probes a read-only profile by running true.
func (p *LocalProvider) tryBwrap() bool {
	_, err := exec.LookPath("bwrap")
	if err != nil {
		return false
	}
	// A successful read-only true invocation indicates availability.
	cmd := exec.Command("bwrap",
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--unshare-pid",
		"--proc", "/proc",
		"--die-with-parent",
		"--", "true",
	)
	return cmd.Run() == nil
}

// tryLandlock probes landlock-launcher availability.
func (p *LocalProvider) tryLandlock() bool {
	_, err := exec.LookPath("landlock-launcher")
	return err == nil
}

// trySeatbelt probes macOS sandbox-exec availability.
func (p *LocalProvider) trySeatbelt() bool {
	_, err := exec.LookPath("sandbox-exec")
	if err != nil {
		return false
	}
	// Run true with a basic SBPL profile.
	cmd := exec.Command("sandbox-exec", "-p",
		"(version 1)(allow default)(deny file-write*)(allow file-write* (literal \"/dev/null\"))",
		"true",
	)
	return cmd.Run() == nil
}

// Backend returns the detected backend type.
func (p *LocalProvider) Backend() BackendType {
	b, _ := p.detectBackend()
	return b
}

// Runner returns the detected runner command.
func (p *LocalProvider) Runner() string {
	_, r := p.detectBackend()
	return r
}

// Available reports whether a local sandbox backend is available.
func (p *LocalProvider) Available() bool {
	b, _ := p.detectBackend()
	return b != BackendUnavailable
}

// Confine implements SandboxProvider by building backend-specific command arguments.
func (p *LocalProvider) Confine(argv []string, policy SandboxPolicy) (ConfinedArgv, error) {
	backend, runner := p.detectBackend()
	if backend == BackendUnavailable {
		return ConfinedArgv{}, &SandboxUnavailableError{Msg: "no local sandbox backend available on this platform"}
	}
	// Windows ACL uses token-based execution rather than a runner command; see windows_acl.go.
	if backend == BackendWindowsACL {
		// Keep argv unchanged for Windows ACL; token creation belongs to the execution
		// layer. This path reports full enforcement.
		return ConfinedArgv{
			Argv:             argv,
			Enforcement:      EnforcementFull,
			DenialSignatures: p.DenialSignatures(),
		}, nil
	}
	// Build profile arguments.
	var profileArgs []string
	switch backend {
	case BackendBwrap:
		profileArgs = BwrapProfileArgs(policy)
	case BackendLandlock:
		profileArgs = LandlockProfileArgs(policy)
	case BackendSeatbelt:
		profileArgs = SeatbeltProfileArgs(policy)
	}
	// Assemble runner, profile arguments, separator, and original argv.
	fullArgv := []string{runner}
	fullArgv = append(fullArgv, profileArgs...)
	fullArgv = append(fullArgv, "--")
	fullArgv = append(fullArgv, argv...)
	return ConfinedArgv{
		Argv:             fullArgv,
		Enforcement:      EnforcementFull,
		DenialSignatures: p.DenialSignatures(),
	}, nil
}

// DenialSignatures returns backend denial strings for failure matching.
func (p *LocalProvider) DenialSignatures() []string {
	backend, _ := p.detectBackend()
	switch backend {
	case BackendBwrap:
		return []string{"Read-only file system", "EROFS"}
	case BackendLandlock:
		return []string{"Permission denied", "EACCES"}
	case BackendSeatbelt:
		return []string{"Operation not permitted", "EPERM"}
	case BackendWindowsACL:
		return []string{"Access is denied", "ERROR_ACCESS_DENIED"}
	default:
		return nil
	}
}

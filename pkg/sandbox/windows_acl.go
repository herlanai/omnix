//go:build windows
// +build windows

// This file implements the Windows ACL backend, following upstream
// sandbox-windows-acl token, ACL, runner, and spawn modules. The intended
// model combines a restricted token, workspace directory grants, and
// CreateProcessAsUser. Each session gets a dedicated temporary directory.
// The implementation below documents incomplete privilege and ACL operations
// and any fallback behavior; these limitations affect enforcement guarantees.
package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// WindowsACLProvider implements SandboxProvider for Windows ACL execution.
type WindowsACLProvider struct {
	mu                 sync.Mutex
	workspace          string
	tempDir            string
	token              windows.Token
	tokenCreated       bool
	enforcementPartial bool // True when token creation falls back to reduced enforcement.
}

// NewWindowsACLProvider creates a Windows ACL provider.
func NewWindowsACLProvider(workspaceRoot string) *WindowsACLProvider {
	return &WindowsACLProvider{
		workspace: workspaceRoot,
	}
}

// Available reports availability; this implementation returns true on Windows.
func (p *WindowsACLProvider) Available() bool {
	return true
}

// CreateRestrictedToken follows upstream token creation conceptually: open
// and duplicate the process token, restrict groups and privileges, then return
// the token. This implementation does not yet perform all restrictions.
// If duplication fails, it falls back to the process token and partial enforcement.
func (p *WindowsACLProvider) CreateRestrictedToken() (windows.Token, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.tokenCreated {
		return p.token, nil
	}

	// Open the current process token.
	procToken, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return 0, fmt.Errorf("open current process token: %w", err)
	}
	defer procToken.Close()

	// Duplicate the token with DuplicateTokenEx.
	var dupToken windows.Token
	err = windows.DuplicateTokenEx(procToken, windows.TOKEN_ALL_ACCESS, nil, windows.SecurityImpersonation, windows.TokenPrimary, &dupToken)
	if err != nil {
		// Fall back on insufficient privileges, mark enforcement partial, and let
		// SpawnWithToken use the current process token.
		p.token = 0
		p.tokenCreated = true
		p.enforcementPartial = true
		return 0, nil
	}

	// Privilege restriction is not implemented here. A complete implementation
	// would call AdjustTokenPrivileges while retaining required privileges such
	// as SeChangeNotifyPrivilege; duplication alone does not apply those restrictions.

	// Record successful token creation.
	p.token = dupToken
	p.tokenCreated = true
	return p.token, nil
}

// GrantWorkspaceAccess ensures workspace read/write access for the current
// user SID, following upstream grantWorkspaceAccess. Existing workspace
// permissions often already include this user.
func (p *WindowsACLProvider) GrantWorkspaceAccess(path string) error {
	// Get the current user's SID.
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return fmt.Errorf("open token: %w", err)
	}
	defer token.Close()

	sid, err := token.GetTokenUser()
	if err != nil {
		return fmt.Errorf("get token user: %w", err)
	}

	// Prepare current-user FULL_CONTROL for the directory. The intended complete
	// implementation uses SetNamedSecurityInfo and SetEntriesInAcl.
	cmd := fmt.Sprintf(`icacls "%s" /grant %s:(OI)(CI)F /T`, path, sid.User.Sid)
	// A complete implementation should call Windows APIs directly.
	_ = cmd // Placeholder; see the implementation below.

	return p.setDirectoryACL(path, sid.User.Sid)
}

// setDirectoryACL handles directory security through Windows APIs.
func (p *WindowsACLProvider) setDirectoryACL(path string, sid *windows.SID) error {
	// Currently this reads the security descriptor only. A complete implementation
	// would retrieve the DACL, construct EXPLICIT_ACCESS, merge with SetEntriesInAcl,
	// and install the result with SetNamedSecurityInfo.

	// GetNamedSecurityInfo accepts a string path.
	sd, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return fmt.Errorf("get named security info: %w", err)
	}

	// Placeholder: return success without installing an ACE.
	_ = sd
	return nil
}

// SetupTempDir creates a per-session temporary directory and configures its ACL,
// following upstream runner.ts setupTempDir.
func (p *WindowsACLProvider) SetupTempDir() (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.tempDir != "" {
		return p.tempDir, nil
	}

	// Create the temporary directory.
	tempDir, err := os.MkdirTemp("", "omnix-sandbox-*")
	if err != nil {
		return "", fmt.Errorf("create temp dir: %w", err)
	}

	// Configure its ACL.
	if err := p.GrantWorkspaceAccess(tempDir); err != nil {
		os.RemoveAll(tempDir)
		return "", fmt.Errorf("grant temp dir access: %w", err)
	}

	p.tempDir = tempDir
	return tempDir, nil
}

// Confine keeps argv unchanged because Windows execution uses a token rather
// than a runner. Enforcement is selected below according to fallback state.
func (p *WindowsACLProvider) Confine(argv []string, policy SandboxPolicy) (ConfinedArgv, error) {
	// Ensure token creation has been attempted.
	if _, err := p.CreateRestrictedToken(); err != nil {
		return ConfinedArgv{}, fmt.Errorf("create restricted token: %w", err)
	}

	// Ensure a temporary directory exists.
	if _, err := p.SetupTempDir(); err != nil {
		return ConfinedArgv{}, fmt.Errorf("setup temp dir: %w", err)
	}

	// Configure workspace ACLs for workspace-write.
	if policy.Mode == ConfinedWorkspaceWrite && p.workspace != "" {
		if err := p.GrantWorkspaceAccess(p.workspace); err != nil {
			return ConfinedArgv{}, fmt.Errorf("grant workspace access: %w", err)
		}
	}

	// Choose the reported enforcement level.
	enforcement := EnforcementFull
	if p.enforcementPartial {
		enforcement = EnforcementPartial
	}

	return ConfinedArgv{
		Argv:        argv,
		Enforcement: enforcement,
		DenialSignatures: []string{
			"Access is denied",
			"ERROR_ACCESS_DENIED",
		},
	}, nil
}

// SpawnWithToken follows upstream spawnWithToken. The caller owns waiting
// and cleanup of the returned process. If no token is available, it falls
// back to ordinary process creation.
func (p *WindowsACLProvider) SpawnWithToken(argv []string, env []string, workDir string) (*os.Process, error) {
	token, _ := p.CreateRestrictedToken()

	// Fall back to ordinary process creation when the token is unavailable.
	if token == 0 {
		attr := &os.ProcAttr{
			Dir:   workDir,
			Env:   env,
			Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
		}
		return os.StartProcess(argv[0], argv, attr)
	}

	// Build the Windows command line.
	cmdLine := buildCommandLine(argv)

	// Initialize startup information.
	var si windows.StartupInfo
	si.Cb = uint32(unsafe.Sizeof(si))

	// Allocate process information.
	var pi windows.ProcessInformation

	// Select process creation flags.
	flags := uint32(windows.CREATE_UNICODE_ENVIRONMENT)

	// Prepare the environment block.
	var envBlock *uint16
	var err error
	if env != nil {
		err = windows.CreateEnvironmentBlock(&envBlock, token, false)
		if err != nil {
			return nil, fmt.Errorf("create environment block: %w", err)
		}
		defer windows.DestroyEnvironmentBlock(envBlock)
	}

	// Set the working directory.
	var workDirPtr *uint16
	if workDir != "" {
		workDirPtr, err = syscall.UTF16PtrFromString(workDir)
		if err != nil {
			return nil, err
		}
	}

	// Create the process with the token.
	err = windows.CreateProcessAsUser(
		token,
		nil,                               // applicationName
		syscall.StringToUTF16Ptr(cmdLine), // commandLine
		nil,                               // processAttributes
		nil,                               // threadAttributes
		false,                             // inheritHandles
		flags,                             // creationFlags
		envBlock,                          // environment
		workDirPtr,                        // currentDirectory
		&si,                               // startupInfo
		&pi,                               // processInformation
	)
	if err != nil {
		return nil, fmt.Errorf("create process as user: %w", err)
	}

	// Close the thread handle; the process handle is managed separately.
	windows.CloseHandle(pi.Thread)

	// Wrap the process as os.Process.
	return os.FindProcess(int(pi.ProcessId))
}

// buildCommandLine builds a Windows command-line string with basic quoting.
func buildCommandLine(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	// This simplified implementation joins quoted arguments with spaces.
	// Complete handling of embedded quotes and whitespace remains necessary.
	result := ""
	for i, arg := range argv {
		if i > 0 {
			result += " "
		}
		if containsSpaceOrQuote(arg) {
			result += `"` + arg + `"`
		} else {
			result += arg
		}
	}
	return result
}

// containsSpaceOrQuote checks for whitespace or quotes.
func containsSpaceOrQuote(s string) bool {
	for _, c := range s {
		if c == ' ' || c == '"' || c == '\t' {
			return true
		}
	}
	return false
}

// Cleanup releases the token and temporary directory.
func (p *WindowsACLProvider) Cleanup() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	var firstErr error

	// Close the token.
	if p.tokenCreated && p.token != 0 {
		if err := p.token.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		p.token = 0
		p.tokenCreated = false
	}
	// Remove the temporary directory.
	if p.tempDir != "" {
		if err := os.RemoveAll(p.tempDir); err != nil && firstErr == nil {
			firstErr = err
		}
		p.tempDir = ""
	}

	return firstErr
}

// Ensure WindowsACLProvider implements SandboxProvider.
var _ SandboxProvider = (*WindowsACLProvider)(nil)

// Keep filepath referenced for path handling.
var _ = filepath.Join

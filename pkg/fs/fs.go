// Package fs implements the filesystem interface, matching fs/fs, fs-local, and
// fs-observation-policy (M35). It defines targets, versions, metadata, edits, write
// intents, LocalFS, and stable FS_* errors; observation rules are in policy.go.
// Resolve produces an opaque stable target identity and a display path. Versions
// derived from stat identity and freshness guard writes and edits. An omitted
// expected value allows unconditional overwrite; supplied intents enforce
// create-if-absent or version matching. All failures use stable FsErrorCode values.
package fs

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Types.

// FsTargetKey is an opaque lookup and stale-write guard key.
type FsTargetKey string

// FsVersion is an opaque file-version token for write/edit guards.
type FsVersion string

// FsTarget is the stable file identity resolved by a backend.
type FsTarget struct {
	// TargetKey is opaque; the local backend uses a canonical absolute path.
	TargetKey FsTargetKey
	// DisplayPath is the path shown to the model or UI.
	DisplayPath string
}

// FsInfo contains stat metadata without contents.
type FsInfo struct {
	// Version is the current freshness token.
	Version FsVersion `json:"version"`
	// Type identifies the file type.
	Type string `json:"type"` // file / directory / other
	// Size is the regular-file size in bytes.
	Size int64 `json:"size,omitempty"`
}

// FsEditRequest describes a literal text replacement.
type FsEditRequest struct {
	// OldString is the nonempty literal text to replace.
	OldString string
	// NewString replaces the match; empty deletes it.
	NewString string
	// ReplaceAll replaces all matches instead of requiring exactly one.
	ReplaceAll bool
}

// FsWriteIntent describes a guarded write.
type FsWriteIntent struct {
	// Kind is createIfAbsent or replaceIfVersion.
	Kind string
	// Version is the expected version for replaceIfVersion.
	Version FsVersion
}

// FsWriteOutcome reports a whole-file write result.
type FsWriteOutcome struct {
	Operation string // create / update
	Version   FsVersion
	Before    string
	After     string
}

// FsEditOutcome reports a literal edit result.
type FsEditOutcome struct {
	Version FsVersion
	Before  string
	After   string
}

// Stable error codes.

// FsErrorCode is a stable filesystem failure code.
type FsErrorCode string

// Error codes aligned with upstream FsErrorCode.
const (
	FSErrNotFound       FsErrorCode = "FS_NOT_FOUND"
	FSErrNotDirectory   FsErrorCode = "FS_NOT_DIRECTORY"
	FSErrNotText        FsErrorCode = "FS_NOT_TEXT"
	FSErrNotRegularFile FsErrorCode = "FS_NOT_REGULAR_FILE"
	FSErrTooLarge       FsErrorCode = "FS_TOO_LARGE"
	FSErrPermission     FsErrorCode = "FS_PERMISSION_DENIED"
	FSErrSandboxDenied  FsErrorCode = "FS_SANDBOX_DENIED"
	FSErrIO             FsErrorCode = "FS_IO_ERROR"
	FSErrStaleVersion   FsErrorCode = "FS_STALE_VERSION"
	FSErrNotObserved    FsErrorCode = "FS_NOT_OBSERVED"
	FSErrAmbiguousEdit  FsErrorCode = "FS_AMBIGUOUS_EDIT"
	FSErrEditNotFound   FsErrorCode = "FS_EDIT_NOT_FOUND"
)

// FsError carries a stable filesystem error code.
type FsError struct {
	Code FsErrorCode
	Msg  string
}

func (e *FsError) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Msg) }

func fsErr(code FsErrorCode, format string, args ...any) error {
	return &FsError{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// Code returns the error code.
func Code(err error) FsErrorCode {
	if err == nil {
		return ""
	}
	if fe, ok := err.(*FsError); ok {
		return fe.Code
	}
	return FSErrIO
}

// Local provider.

// LocalFS implements a local disk filesystem.
type LocalFS struct {
	// Cwd optionally anchors relative-path resolution.
	Cwd string
}

// NewLocal creates a local filesystem.
func NewLocal(cwd string) *LocalFS {
	return &LocalFS{Cwd: cwd}
}

// Resolve resolves relative paths against Cwd and follows symlinks to a canonical target.
func (l *LocalFS) Resolve(path string) (FsTarget, error) {
	abs := path
	if !filepath.IsAbs(abs) {
		if l.Cwd == "" {
			return FsTarget{}, fsErr(FSErrIO, "no cwd for relative path %q", path)
		}
		abs = filepath.Join(l.Cwd, path)
	}
	real, err := filepath.Abs(abs)
	if err != nil {
		return FsTarget{}, fsErr(FSErrIO, "resolve %q: %v", path, err)
	}
	// Follow symlinks with EvalSymlinks, falling back to Clean when resolution fails.
	if rl, err := filepath.EvalSymlinks(real); err == nil {
		real = filepath.Clean(rl)
	}
	return FsTarget{TargetKey: FsTargetKey(filepath.Clean(real)), DisplayPath: filepath.Clean(abs)}, nil
}

// ProcessPath returns a canonical absolute path accessible to subprocesses.
func (l *LocalFS) ProcessPath(t FsTarget) string { return string(t.TargetKey) }

// FileURL returns a file URI.
func (l *LocalFS) FileURL(t FsTarget) string {
	p := filepath.ToSlash(string(t.TargetKey))
	return "file://" + p
}

// Contains checks whether child equals parent or lies beneath its canonical path.
func (l *LocalFS) Contains(parent, child FsTarget) bool {
	p := string(parent.TargetKey)
	c := string(child.TargetKey)
	if c == p {
		return true
	}
	return strings.HasPrefix(c, p+string(filepath.Separator))
}

// versionOf derives a version token from stat identity and freshness.
func versionOf(path string) (FsVersion, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	h.Write([]byte(path))
	var buf [16]byte
	binary.LittleEndian.PutUint64(buf[0:8], uint64(info.Size()))
	binary.LittleEndian.PutUint64(buf[8:16], uint64(info.ModTime().UnixNano()))
	h.Write(buf[:])
	return FsVersion(fmt.Sprintf("%x", h.Sum(nil))[:16]), nil
}

// Stat returns metadata, or nil and FS_NOT_FOUND for a missing target.
func (l *LocalFS) Stat(t FsTarget) (*FsInfo, error) {
	path := string(t.TargetKey)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fsErr(FSErrNotFound, "no such target %q", t.DisplayPath)
		}
		return nil, fsErr(FSErrIO, "stat %q: %v", path, err)
	}
	v, _ := versionOf(path)
	typ := "other"
	switch {
	case info.IsDir():
		typ = "directory"
	case info.Mode().IsRegular():
		typ = "file"
	}
	return &FsInfo{Version: v, Type: typ, Size: info.Size()}, nil
}

// ReadText reads an entire file as UTF-8 text.
func (l *LocalFS) ReadText(t FsTarget) (string, error) {
	path := string(t.TargetKey)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fsErr(FSErrNotFound, "%q not found", t.DisplayPath)
		}
		return "", fsErr(FSErrIO, "read %q: %v", path, err)
	}
	// Detect invalid UTF-8 bytes with strings.ToValidUTF8.
	if strings.ContainsRune(string(data), '\uFFFD') && !utf8Valid(data) {
		return "", fsErr(FSErrNotText, "%q is not valid UTF-8 text", t.DisplayPath)
	}
	// Normalize line endings to LF for comparisons.
	return normalizeLF(string(data)), nil
}

// ReadBytes reads raw bytes, returning FS_TOO_LARGE above maxBytes.
func (l *LocalFS) ReadBytes(t FsTarget, maxBytes int) ([]byte, error) {
	data, err := os.ReadFile(string(t.TargetKey))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fsErr(FSErrNotFound, "%q not found", t.DisplayPath)
		}
		return nil, fsErr(FSErrIO, "%v", err)
	}
	if len(data) > maxBytes {
		return nil, fsErr(FSErrTooLarge, "%q exceeds %d bytes", t.DisplayPath, maxBytes)
	}
	return data, nil
}

// ListDir returns immediate children as metadata in stable name order.
func (l *LocalFS) ListDir(t FsTarget) ([]FsDirEntry, error) {
	entries, err := os.ReadDir(string(t.TargetKey))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fsErr(FSErrNotFound, "%q not found", t.DisplayPath)
		}
		return nil, fsErr(FSErrIO, "list %q: %v", t.DisplayPath, err)
	}
	out := make([]FsDirEntry, 0, len(entries))
	for _, e := range entries {
		typ := "file"
		if e.IsDir() {
			typ = "directory"
		}
		child, _ := l.Resolve(filepath.Join(string(t.TargetKey), e.Name()))
		out = append(out, FsDirEntry{Name: e.Name(), Type: typ, Target: child})
	}
	return out, nil
}

// FsDirEntry describes an immediate directory child.
type FsDirEntry struct {
	Name   string
	Type   string
	Target FsTarget
	Size   int64
}

// WriteText atomically creates or replaces a file with an optional expected guard.
func (l *LocalFS) WriteText(t FsTarget, content string, expected *FsWriteIntent) (FsWriteOutcome, error) {
	path := string(t.TargetKey)
	_, statErr := os.Stat(path)
	exists := statErr == nil
	before := ""
	if exists {
		b, _ := os.ReadFile(path)
		before = normalizeLF(string(b))
	}

	// Apply the guard.
	if expected != nil {
		switch expected.Kind {
		case "createIfAbsent":
			if exists {
				return FsWriteOutcome{}, fsErr(FSErrNotObserved, "%q already exists for createIfAbsent", t.DisplayPath)
			}
		case "replaceIfVersion":
			if !exists {
				return FsWriteOutcome{}, fsErr(FSErrStaleVersion, "%q absent, cannot replaceIfVersion", t.DisplayPath)
			}
			cur, _ := versionOf(path)
			if cur != expected.Version {
				return FsWriteOutcome{}, fsErr(FSErrStaleVersion, "%q version mismatch", t.DisplayPath)
			}
		}
	}

	after := normalizeLF(content)
	// Publish atomically by writing a temporary file and renaming it.
	if err := atomicWrite(path, after); err != nil {
		return FsWriteOutcome{}, fsErr(FSErrIO, "write %q: %v", t.DisplayPath, err)
	}
	v, _ := versionOf(path)
	op := "update"
	if !exists {
		op = "create"
	}
	return FsWriteOutcome{Operation: op, Version: v, Before: before, After: after}, nil
}

// EditText applies a literal replacement atomically, checking the expected version first.
func (l *LocalFS) EditText(t FsTarget, edit FsEditRequest, expected *FsVersion) (FsEditOutcome, error) {
	path := string(t.TargetKey)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return FsEditOutcome{}, fsErr(FSErrStaleVersion, "%q absent", t.DisplayPath)
		}
		return FsEditOutcome{}, fsErr(FSErrIO, "%v", err)
	}
	// Version validation precedes matching so stale writes return FS_STALE_VERSION first.
	cur, _ := versionOf(path)
	if expected != nil && cur != *expected {
		return FsEditOutcome{}, fsErr(FSErrStaleVersion, "%q version mismatch (observed %s, now %s)", t.DisplayPath, *expected, cur)
	}
	before, _ := os.ReadFile(path)
	bf := normalizeLF(string(before))

	var after string
	switch {
	case edit.ReplaceAll:
		after = strings.ReplaceAll(bf, edit.OldString, edit.NewString)
		if after == bf {
			return FsEditOutcome{}, fsErr(FSErrEditNotFound, "%q has no match for %q", t.DisplayPath, edit.OldString)
		}
	default:
		idx := strings.Index(bf, edit.OldString)
		if idx < 0 {
			return FsEditOutcome{}, fsErr(FSErrEditNotFound, "%q has no match for %q", t.DisplayPath, edit.OldString)
		}
		if !edit.ReplaceAll && countOccurrences(bf, edit.OldString) != 1 {
			return FsEditOutcome{}, fsErr(FSErrAmbiguousEdit, "%q has multiple matches for %q; set replaceAll", t.DisplayPath, edit.OldString)
		}
		after = strings.Replace(bf, edit.OldString, edit.NewString, 1)
	}

	// Publish atomically.
	if err := atomicWrite(path, after); err != nil {
		return FsEditOutcome{}, fsErr(FSErrIO, "edit %q: %v", t.DisplayPath, err)
	}
	v, _ := versionOf(path)
	return FsEditOutcome{Version: v, Before: bf, After: after}, nil
}

// ObservedVersion returns the current target version for observation tracking.
func (l *LocalFS) ObservedVersion(t FsTarget) (FsVersion, bool) {
	if _, err := os.Stat(string(t.TargetKey)); err != nil {
		return "", false
	}
	v, _ := versionOf(string(t.TargetKey))
	return v, true
}

// atomicWrite publishes a temporary file with rename.
func atomicWrite(path, content string) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".omnix-write-*")
	if err != nil {
		return err
	}
	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func countOccurrences(s, sub string) int {
	if sub == "" {
		return 0
	}
	return strings.Count(s, sub)
}

// normalizeLF normalizes line endings for comparisons.
func normalizeLF(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

func utf8Valid(b []byte) bool {
	return !strings.ContainsRune(string(b), '\uFFFD')
}

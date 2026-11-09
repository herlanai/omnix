// M35 filesystem tests cover observation-gated writes, version guards after reads,
// FS_STALE_VERSION for a second edit using a stale observation, atomic IO, listing, and missing or
// ambiguous edit matches.
package tests

import (
	"strings"
	"testing"

	"github.com/herlanai/omnix/pkg/fs"
)

// TestFsObsPolicyRejectsUnobservedWrite rejects overwriting an unobserved target.
func TestFsObsPolicyRejectsUnobservedWrite(t *testing.T) {
	root := t.TempDir()
	local := fs.NewLocal(root)
	obs := fs.NewObservationPolicy()
	svc := fs.NewPolicyFS(local, obs)

	target, _ := local.Resolve("a.txt")
	// An unobserved target receives createIfAbsent. Creating an absent file succeeds, but an existing
	// file must be rejected with FS_NOT_OBSERVED when this owner has not observed it.
	_ = target
	existing, _ := local.Resolve("existing.txt")
	if _, err := svc.Write("owner1", existing, "hello"); err != nil {
		t.Fatalf("首个写(createIfAbsent, 文件不存在)应成功: %v", err)
	}
	// A repeated write without observing the existing version still requests createIfAbsent and fails
	// with FS_NOT_OBSERVED.
	_, err := svc.Write("owner1", existing, "hello2")
	if fs.Code(err) != fs.FSErrNotObserved {
		t.Fatalf("未观察版本 + 文件已存在应 FS_NOT_OBSERVED, 实际 %v", err)
	}
}

// TestFsReadThenWriteGuarded verifies observation through reading enables replaceIfVersion.
func TestFsReadThenWriteGuarded(t *testing.T) {
	root := t.TempDir()
	local := fs.NewLocal(root)
	obs := fs.NewObservationPolicy()
	svc := fs.NewPolicyFS(local, obs)

	target, _ := local.Resolve("g.txt")
	// The initial createIfAbsent write succeeds.
	if _, err := svc.Write("o", target, "v1"); err != nil {
		t.Fatal(err)
	}
	// Read and record the observed version.
	svc.ReflectObservation("o", target)
	// Replace successfully using the observed version.
	res, err := svc.Write("o", target, "v2")
	if err != nil {
		t.Fatalf("观察后 replaceIfVersion 应成功: %v", err)
	}
	if res.Operation != "update" {
		t.Fatalf("应为 update, 实际 %s", res.Operation)
	}
	got, _ := local.ReadText(target)
	if got != "v2" {
		t.Fatalf("内容应为 v2, 实际 %q", got)
	}
}

// TestFsConcurrentEditStaleVersion rejects a second edit based on the same stale observation.
func TestFsConcurrentEditStaleVersion(t *testing.T) {
	root := t.TempDir()
	local := fs.NewLocal(root)
	obs := fs.NewObservationPolicy()
	svc := fs.NewPolicyFS(local, obs)

	target, _ := local.Resolve("c.txt")
	if _, err := svc.Write("o", target, "aaa\nbbb\n"); err != nil {
		t.Fatal(err)
	}
	svc.ReflectObservation("o", target)

	// The first successful edit advances the version.
	if _, err := svc.Edit("o", target, fs.FsEditRequest{OldString: "aaa", NewString: "AAA"}); err != nil {
		t.Fatalf("第一个 edit 应成功: %v", err)
	}
	// The second edit uses the stale version and returns FS_STALE_VERSION.
	edit := fs.FsEditRequest{OldString: "bbb", NewString: "BBB"}
	if _, err := svc.Edit("o", target, edit); err == nil {
		t.Fatal("陈旧版本编辑应报错")
	} else if fs.Code(err) != fs.FSErrStaleVersion {
		t.Fatalf("应 FS_STALE_VERSION, 实际 %v", err)
	}
}

// TestFsEditNotFoundAndAmbiguous verifies missing and multiple literal matches.
func TestFsEditNotFoundAndAmbiguous(t *testing.T) {
	local := fs.NewLocal(t.TempDir())
	target, _ := local.Resolve("e.txt")
	if _, err := local.WriteText(target, "x y x\n", nil); err != nil {
		t.Fatal(err)
	}
	// Multiple matches without replaceAll produce AmbiguousEdit.
	if _, err := local.EditText(target, fs.FsEditRequest{OldString: "x", NewString: "z"}, nil); fs.Code(err) != fs.FSErrAmbiguousEdit {
		t.Fatalf("多匹配应 AmbiguousEdit, 实际 %v", err)
	}
	// No match produces EditNotFound; replaceAll handles multiple matches successfully.
	if _, err := local.EditText(target, fs.FsEditRequest{OldString: "q", NewString: "z"}, nil); fs.Code(err) != fs.FSErrEditNotFound {
		t.Fatalf("无匹配应 EditNotFound, 实际 %v", err)
	}
	if _, err := local.EditText(target, fs.FsEditRequest{OldString: "x", NewString: "z", ReplaceAll: true}, nil); err != nil {
		t.Fatalf("replaceAll 应成功: %v", err)
	}
}

// TestFsReadBytesTooLarge returns FS_TOO_LARGE when the limit is exceeded.
func TestFsReadBytesTooLarge(t *testing.T) {
	local := fs.NewLocal(t.TempDir())
	target, _ := local.Resolve("big.txt")
	if _, err := local.WriteText(target, strings.Repeat("a", 4096), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := local.ReadBytes(target, 100); fs.Code(err) != fs.FSErrTooLarge {
		t.Fatalf("超上限应 FS_TOO_LARGE, 实际 %v", err)
	}
}

// TestFsVersionStability verifies stable versions for unchanged content and timestamps.
func TestFsVersionStability(t *testing.T) {
	local := fs.NewLocal(t.TempDir())
	target, _ := local.Resolve("v.txt")
	if _, err := local.WriteText(target, "stable", nil); err != nil {
		t.Fatal(err)
	}
	v1, _ := local.ObservedVersion(target)
	v2, _ := local.ObservedVersion(target)
	if v1 != v2 {
		t.Fatalf("同状态两次版本应一致: %s vs %s", v1, v2)
	}
}

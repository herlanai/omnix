// M29 attachment tests cover content-addressed opaque IDs, durable cross-session references with
// digest checks, byte-exact resolution after compaction retains only references, media admission,
// and tamper rejection.
package tests

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/herlanai/omnix/pkg/attachment"
	"github.com/herlanai/omnix/pkg/brand"
)

// pngBytes contains a minimal PNG header and trailing IEND for media-type admission tests.
func pngBytes() []byte {
	return []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D}
}

// TestAttachmentSaveAndResolve verifies byte-exact save/reference/resolve round trips.
func TestAttachmentSaveAndResolve(t *testing.T) {
	store := attachment.NewStore("")
	data := pngBytes()
	ref, err := store.SaveImage(attachment.SaveImageAttachment{Data: data, MediaType: attachment.MediaPNG, Name: "a.png"})
	if err != nil {
		t.Fatalf("SaveImage 失败: %v", err)
	}
	if ref.AttachmentID == (attachment.AttachmentID{}) {
		t.Fatal("应返回 content-addressed AttachmentID")
	}
	if !strings.HasPrefix(ref.AttachmentID.Raw(), "sha256:") {
		t.Fatalf("AttachmentID 应为 sha256:<digest>, 实际 %q", ref.AttachmentID.Raw())
	}
	if ref.Bytes != len(data) {
		t.Fatalf("Bytes 应记录长度 %d, 实际 %d", len(data), ref.Bytes)
	}
	stored, err := attachment.ResolveReference(store, ref)
	if err != nil {
		t.Fatalf("ResolveReference 失败: %v", err)
	}
	if !bytes.Equal(stored.Data, data) {
		t.Fatal("解析字节与保存字节不一致")
	}
}

// TestAttachmentDurableAcrossCompactionAndSession verifies references survive session changes and
// compaction.
func TestAttachmentDurableAcrossCompactionAndSession(t *testing.T) {
	// Shared content-addressed storage is retention-neutral across sessions.
	dir := t.TempDir()
	store := attachment.NewStore(dir)
	data := pngBytes()

	// Session A saves the image.
	refA, err := store.SaveImage(attachment.SaveImageAttachment{Data: data, MediaType: attachment.MediaPNG})
	if err != nil {
		t.Fatal(err)
	}

	// Session B receives only refA to simulate a cross-session reference.
	refB := attachment.ImageAttachmentRef{AttachmentID: refA.AttachmentID, MediaType: refA.MediaType, Bytes: refA.Bytes}
	stored, err := attachment.ResolveReference(store, refB)
	if err != nil {
		t.Fatalf("跨会话 durable 引用解析失败: %v", err)
	}
	if !bytes.Equal(stored.Data, data) {
		t.Fatal("跨会话解析字节不一致")
	}

	// Simulate compaction by discarding the store and resolving solely through the durable host-file
	// path.
	refOnly := refA
	fileBacked, err := attachment.ResolveFileBacked(dir, refOnly)
	if err != nil {
		t.Fatalf("宿主文件 durable 路径解析失败: %v", err)
	}
	if !bytes.Equal(fileBacked.Data, data) {
		t.Fatal("宿主文件解析字节不一致")
	}
}

// TestAttachmentMediaTypeAdmission validates media-type admission.
func TestAttachmentMediaTypeAdmission(t *testing.T) {
	store := attachment.NewStore("")
	// Reject bytes declared as PNG that are not PNG data.
	if _, err := store.SaveImage(attachment.SaveImageAttachment{Data: []byte("not an image"), MediaType: attachment.MediaPNG}); err == nil {
		t.Fatal("字节与声明媒体类型不符应被拒绝")
	}
	// Accept valid PNG data.
	if _, err := store.SaveImage(attachment.SaveImageAttachment{Data: pngBytes(), MediaType: attachment.MediaPNG}); err != nil {
		t.Fatalf("合法 PNG 应被接受: %v", err)
	}
}

// TestAttachmentReadUnknownID rejects unknown or forged references at authoritative read time.
func TestAttachmentReadUnknownID(t *testing.T) {
	store := attachment.NewStore("")
	_, err := store.ReadImage(attachment.ImageAttachmentRef{
		AttachmentID: brand.NewAttachmentID("sha256:deadbeef"),
		MediaType:    attachment.MediaPNG,
	})
	if err == nil {
		t.Fatal("未知对象应报错")
	}
}

// TestAttachmentHostFileDigestCatch rejects a host file whose digest changed after tampering.
func TestAttachmentHostFileDigestCatch(t *testing.T) {
	dir := t.TempDir()
	store := attachment.NewStore(dir)
	data := pngBytes()
	ref, err := store.SaveImage(attachment.SaveImageAttachment{Data: data, MediaType: attachment.MediaPNG})
	if err != nil {
		t.Fatal(err)
	}
	// Replace the host-file contents so their digest no longer matches the reference.
	if err := os.WriteFile(filepath.Join(dir, ref.AttachmentID.Raw()), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := attachment.ResolveFileBacked(dir, ref); err == nil {
		t.Fatal("篡改后的宿主文件应因 digest 不匹配被拒绝")
	}
}

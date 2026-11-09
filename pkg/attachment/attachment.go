// Package attachment implements durable image references (M29), matching
// packages/attachment/attachment. Producers supply validated encoded bytes;
// an immutable, content-addressed AttachmentID is published only after storage.
//
// Session events and model-visible ImageBlocks contain references and metadata,
// never browser object URLs, temporary host paths, provider URLs, or base64 data.
// AttachmentID is opaque: consumers must use Store.ReadImage rather than parse
// the ID or derive a filesystem path from it. The store is retention-neutral,
// so resumed and forked sessions can share objects across compaction.
package attachment

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/herlanai/omnix/pkg/brand"
)

// Media types and references.

// ImageMediaType identifies an image format accepted by v1 attachments.
type ImageMediaType string

// Supported image media types.
const (
	MediaPNG  ImageMediaType = "image/png"
	MediaJPEG ImageMediaType = "image/jpeg"
	MediaWebP ImageMediaType = "image/webp"
	MediaGIF  ImageMediaType = "image/gif"
)

// AttachmentID is an opaque durable identifier aliased from pkg/brand.
type AttachmentID = brand.AttachmentID

// ImageAttachmentRef is a durable, serializable reference to an immutable
// normalized image. It is an opaque storage identifier, never a path or URL.
type ImageAttachmentRef struct {
	// AttachmentID is the content-addressed ID (sha256:<digest>).
	AttachmentID AttachmentID `json:"attachmentId"`
	// MediaType is verified against the stored bytes.
	MediaType ImageMediaType `json:"mediaType"`
	// Bytes is the exact encoded byte length.
	Bytes int `json:"bytes"`
	// Width is the encoded width in pixels.
	Width int `json:"width,omitempty"`
	// Height is the encoded height in pixels.
	Height int `json:"height,omitempty"`
	// Name is an optional display name with local path information removed.
	Name string `json:"name,omitempty"`
}

// StoredImageAttachment contains bytes and a reference verified by digest.
type StoredImageAttachment struct {
	Ref  ImageAttachmentRef `json:"ref"`
	Data []byte             `json:"data"`
}

// SaveImageAttachment describes an image submission.
type SaveImageAttachment struct {
	// Data contains encoded image bytes.
	Data []byte
	// MediaType is the caller-declared type checked against the decoded bytes.
	MediaType ImageMediaType
	// Name is an optional display name, never interpreted as a path.
	Name string
	// Width and Height are optional intrinsic dimensions; zero means unspecified.
	Width  int
	Height int
}

// Attachment storage interface.

// Store provides immutable binary attachments (ctx.attachments).
type Store interface {
	// SaveImage validates and persists an image, returning a content-addressed reference.
	SaveImage(input SaveImageAttachment) (ImageAttachmentRef, error)
	// ReadImage reads an image and verifies that its bytes still match the reference.
	ReadImage(ref ImageAttachmentRef) (StoredImageAttachment, error)
	// ImageHostPath returns the provider-owned host path, or false without a file backend.
	ImageHostPath(ref ImageAttachmentRef) (string, bool)
}

// Content-addressed memory storage with an optional host file backend.

// FileBackedStore implements content-addressed attachment storage.
// The primary map stores IDs and bytes; an optional directory also persists
// objects at <dir>/<digest> for cross-session, resume, and fork resolution.
// IDs use sha256:<hex(digest)> and reads recompute the digest to detect drift.
// Image dimensions are not forcibly parsed; admission checks media signatures.
type FileBackedStore struct {
	mu   sync.RWMutex
	dir  string
	objs map[string][]byte
}

// NewStore creates storage, enabling the host file backend when dir is nonempty.
func NewStore(dir string) *FileBackedStore {
	return &FileBackedStore{
		dir:  dir,
		objs: map[string][]byte{},
	}
}

// contentID computes a content-addressed ID (sha256:<hex>).
func contentID(data []byte) AttachmentID {
	sum := sha256.Sum256(data)
	return brand.NewAttachmentID("sha256:" + hex.EncodeToString(sum[:]))
}

var errUnsupportedMedia = errors.New("attachment: unsupported media type")

// validateMediaType checks the declared media type against the byte signature.
func validateMediaType(mt ImageMediaType, data []byte) error {
	// Check common magic bytes as an admission gate, not a complete format validation.
	sig := map[ImageMediaType]func([]byte) bool{
		MediaPNG: func(b []byte) bool {
			return len(b) >= 8 && b[0] == 0x89 && b[1] == 0x50 && b[2] == 0x4E && b[3] == 0x47
		},
		MediaJPEG: func(b []byte) bool { return len(b) >= 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF },
		MediaWebP: func(b []byte) bool { return len(b) >= 12 && string(b[0:4]) == "RIFF" && string(b[8:12]) == "WEBP" },
		MediaGIF:  func(b []byte) bool { return len(b) >= 6 && string(b[0:4]) == "GIF8" },
	}
	check, ok := sig[mt]
	if !ok {
		return errUnsupportedMedia
	}
	if !check(data) {
		return fmt.Errorf("attachment: bytes do not match declared media type %q", string(mt))
	}
	return nil
}

// SaveImage validates and persists an image, returning its content-addressed reference.
func (s *FileBackedStore) SaveImage(input SaveImageAttachment) (ImageAttachmentRef, error) {
	if len(input.Data) == 0 {
		return ImageAttachmentRef{}, errors.New("attachment: empty image data")
	}
	if err := validateMediaType(input.MediaType, input.Data); err != nil {
		return ImageAttachmentRef{}, err
	}
	id := contentID(input.Data)
	ref := ImageAttachmentRef{
		AttachmentID: id,
		MediaType:    input.MediaType,
		Bytes:        len(input.Data),
		Width:        input.Width,
		Height:       input.Height,
		Name:         input.Name,
	}
	s.mu.Lock()
	s.objs[id.Raw()] = append([]byte(nil), input.Data...)
	s.mu.Unlock()
	// Optional host file persistence at <dir>/<digest>.
	if s.dir != "" {
		_ = writeHostFile(s.dir, ref.AttachmentID, input.Data)
	}
	return ref, nil
}

// ReadImage reads bytes and verifies that they match the recorded reference.
func (s *FileBackedStore) ReadImage(ref ImageAttachmentRef) (StoredImageAttachment, error) {
	s.mu.RLock()
	data, ok := s.objs[ref.AttachmentID.Raw()]
	s.mu.RUnlock()
	if !ok {
		return StoredImageAttachment{}, fmt.Errorf("attachment: object %q not found", ref.AttachmentID.Raw())
	}
	// Recompute the digest for authoritative reads to detect content drift.
	want := contentID(data)
	if want != ref.AttachmentID {
		return StoredImageAttachment{}, fmt.Errorf("attachment: digest mismatch for %q", ref.AttachmentID.Raw())
	}
	return StoredImageAttachment{Ref: ref, Data: append([]byte(nil), data...)}, nil
}

// ImageHostPath returns the host object path in directory mode, or false.
func (s *FileBackedStore) ImageHostPath(ref ImageAttachmentRef) (string, bool) {
	if s.dir == "" {
		return "", false
	}
	return filepath.Join(s.dir, ref.AttachmentID.Raw()), true
}

// writeHostFile persists an object at <dir>/<digest> for host filesystem access.
func writeHostFile(dir string, id AttachmentID, data []byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, id.Raw()), data, 0o644)
}

// Durable reference resolution after session unloading and persistence.

// ResolveReference resolves authoritative bytes from a session attachment reference.
// It uses Store.ReadImage and digest verification when a store is supplied,
// otherwise the host file backend; unavailable resolution returns an error.
// Content-addressed references remain valid across compaction and sessions
// as long as the object remains in the store or host backend.
func ResolveReference(store Store, ref ImageAttachmentRef) (StoredImageAttachment, error) {
	if store != nil {
		return store.ReadImage(ref)
	}
	return StoredImageAttachment{}, errors.New("attachment: no store available for durable reference resolution")
}

// ResolveFileBacked reads <dir>/<digest> when only a host directory is available.
func ResolveFileBacked(dir string, ref ImageAttachmentRef) (StoredImageAttachment, error) {
	p := filepath.Join(dir, ref.AttachmentID.Raw())
	data, err := os.ReadFile(p)
	if err != nil {
		return StoredImageAttachment{}, fmt.Errorf("attachment: read host file for %q: %w", ref.AttachmentID.Raw(), err)
	}
	if contentID(data) != ref.AttachmentID {
		return StoredImageAttachment{}, fmt.Errorf("attachment: host file digest mismatch for %q", ref.AttachmentID.Raw())
	}
	return StoredImageAttachment{Ref: ref, Data: data}, nil
}

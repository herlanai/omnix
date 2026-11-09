// Attachment canonical base64 admission tests.
package tests

import (
	"testing"

	"github.com/herlanai/omnix/pkg/attachment"
)

func TestCanonicalBase64OK(t *testing.T) {
	raw, err := attachment.DecodeCanonicalBase64("aGVsbG8=") // "hello"
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "hello" {
		t.Fatalf("decoded = %q", raw)
	}
}

func TestCanonicalBase64Reject(t *testing.T) {
	bad := []string{
		"",
		"aGVsbG8",   // Missing padding.
		"aGVsb G8=", // Contains whitespace.
		"!!!",
	}
	for _, b := range bad {
		if _, err := attachment.DecodeCanonicalBase64(b); err != attachment.ErrInvalidBase64 {
			t.Fatalf("input %q should be rejected, got %v", b, err)
		}
	}
}

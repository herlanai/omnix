// Session-title normalization tests.
package tests

import (
	"testing"

	"github.com/herlanai/omnix/pkg/sessiontitle"
)

func TestCleanTitle(t *testing.T) {
	in := "\x1b[31ma\x1b[0m   b\x00c\t d"
	got := sessiontitle.CleanTitle(in)
	// Remove control characters, joining b and c, and collapse whitespace to single spaces between a/b
	// and c/d.
	if got != "a bc d" {
		t.Fatalf("clean = %q", got)
	}
}

func TestTruncateUTF8(t *testing.T) {
	// Each Chinese character uses three bytes, so a seven-byte budget fits two complete characters.
	if got := sessiontitle.TruncateUTF8("你好世界", 7); got != "你好" {
		t.Fatalf("truncate = %q", got)
	}
}

func TestNormalizeTitle(t *testing.T) {
	if got := sessiontitle.Normalize("  hello   world  ", 100); got != "hello world" {
		t.Fatalf("normalize = %q", got)
	}
}

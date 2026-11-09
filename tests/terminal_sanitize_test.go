// Terminal escape-sequence sanitization tests.
package tests

import (
	"testing"

	"github.com/herlanai/omnix/pkg/terminal"
)

func TestStripANSICSI(t *testing.T) {
	in := "\x1b[31mred\x1b[0m"
	if got := terminal.StripANSI(in); got != "red" {
		t.Fatalf("csi = %q", got)
	}
}

func TestStripANSIOSC(t *testing.T) {
	// Terminate with BEL.
	if got := terminal.StripANSI("\x1b]0;title\x07out"); got != "out" {
		t.Fatalf("osc bel = %q", got)
	}
	// Terminate with ST.
	if got := terminal.StripANSI("\x1b]0;title\x1b\\out"); got != "out" {
		t.Fatalf("osc st = %q", got)
	}
}

func TestStripANSITwoByte(t *testing.T) {
	if got := terminal.StripANSI("\x1b7a\x1b8"); got != "a" {
		t.Fatalf("two byte = %q", got)
	}
}

func TestNormalizeText(t *testing.T) {
	if got := terminal.NormalizeText("a\r\nb\rc\x07"); got != "a\nb\nc" {
		t.Fatalf("normalize = %q", got)
	}
}

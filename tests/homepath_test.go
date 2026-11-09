// Package tests 的 homepath 验收测试。
package tests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/herlanai/omnix/pkg/homepath"
)

func TestResolveOmnixHomeDefault(t *testing.T) {
	// 空白 env 视为未设置，退回 ~/.omnix。
	got, err := homepath.Resolve("", map[string]string{homepath.EnvName: "   "})
	if err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	want, _ := filepath.Abs(filepath.Join(home, ".omnix"))
	if got != want {
		t.Fatalf("default = %q want %q", got, want)
	}
}

func TestResolveOmnixHomeEnv(t *testing.T) {
	got, err := homepath.Resolve("", map[string]string{homepath.EnvName: "/tmp/omnix"})
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.Abs("/tmp/omnix")
	if got != want {
		t.Fatalf("env = %q want %q", got, want)
	}
}

func TestResolveOmnixHomeConfiguredWins(t *testing.T) {
	got, err := homepath.Resolve("/cfg", map[string]string{homepath.EnvName: "/env"})
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.Abs("/cfg")
	if got != want {
		t.Fatalf("configured = %q want %q", got, want)
	}
}

func TestExpandHome(t *testing.T) {
	home, _ := os.UserHomeDir()
	got, err := homepath.ExpandHome("~/x")
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(home, "x") {
		t.Fatalf("expand = %q", got)
	}
}

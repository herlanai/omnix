// N04/D3 tests verify byte-identical catalogs over 1000 calls, hash changes and injection after
// skill additions/removals, and a single initial injection for unchanged skills.
package tests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/herlanai/omnix/pkg/skills"
)

// TestN04CatalogStable compares catalog bytes across 1000 calls.
func TestN04CatalogStable(t *testing.T) {
	root := t.TempDir()
	r := skills.New(root)
	for _, n := range []string{"b", "a", "c"} {
		writeSkill(t, filepath.Join(root, ".omnix", "skills"), n, "desc of "+n)
	}
	r.Scan()
	first := r.CatalogText()
	firstHash := r.CatalogHash()
	if first == "" {
		t.Fatal("CatalogText 不应为空")
	}
	for i := 0; i < 1000; i++ {
		if got := r.CatalogText(); got != first {
			t.Fatalf("第 %d 次 CatalogText 不一致", i)
		}
		if got := r.CatalogHash(); got != firstHash {
			t.Fatalf("CatalogHash 不稳定")
		}
	}
	// Stable lexical ordering puts a before b.
	if indexOf(first, "a ") > indexOf(first, "b ") {
		t.Fatalf("catalog 应按 name 字典序: %q", first)
	}
}

// TestN04ChangeOnlyInject injects after skill changes but skips unchanged catalogs.
func TestN04ChangeOnlyInject(t *testing.T) {
	root := t.TempDir()
	r := skills.New(root)
	proj := filepath.Join(root, ".omnix", "skills")
	writeSkill(t, proj, "alpha", "AAA")
	r.Scan()

	in := skills.NewInjector(r)
	// Initial injection.
	content, injected := in.MaybeInject()
	if !injected || content == "" {
		t.Fatal("首次应注入")
	}
	// Repeated calls with unchanged skills must not reinject.
	for i := 0; i < 50; i++ { // Simulate 50 turns.
		if _, injected := in.MaybeInject(); injected {
			t.Fatalf("skills 不变时第 %d 轮应不注入", i)
		}
	}
	if in.InjectCount() != 1 {
		t.Fatalf("应只注入 1 次, 实际 %d", in.InjectCount())
	}

	// Adding a skill changes the hash and triggers injection.
	writeSkill(t, proj, "beta", "BBB")
	r.Scan()
	if _, injected := in.MaybeInject(); !injected {
		t.Fatal("新增 skill 后应重新注入")
	}
	if in.InjectCount() != 2 {
		t.Fatalf("应有两次注入, 实际 %d", in.InjectCount())
	}

	// Removing alpha changes the hash and triggers injection.
	if err := os.Remove(filepath.Join(proj, "alpha.md")); err != nil {
		t.Fatal(err)
	}
	r.Scan()
	if _, injected := in.MaybeInject(); !injected {
		t.Fatal("删除 skill 后应重新注入")
	}
}

// TestN04InjectorBlockedInAvailableSkills verifies the available_skills block.
func TestN04InjectorBlockedInAvailableSkills(t *testing.T) {
	root := t.TempDir()
	r := skills.New(root)
	writeSkill(t, filepath.Join(root, ".omnix", "skills"), "refactor", "tool tips")
	r.Scan()
	in := skills.NewInjector(r)
	content, _ := in.MaybeInject()
	if indexOf(content, "<available_skills>") < 0 || indexOf(content, "</available_skills>") < 0 {
		t.Fatalf("注入应含 <available_skills> 区块: %q", content)
	}
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

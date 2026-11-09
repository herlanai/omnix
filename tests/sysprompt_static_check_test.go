// N03/D2 tests verify byte-identical rendering and system hashes across 1000 calls, detection of
// time/working-directory/randomness antipatterns, and clean sections with no violations.
package tests

import (
	"testing"

	"github.com/herlanai/omnix/pkg/sysprompt"
)

// TestN03SystemPromptPureFunction compares 1000 byte-identical renderings.
func TestN03SystemPromptPureFunction(t *testing.T) {
	a := sysprompt.New()
	a.Register("persona", sysprompt.SectionOrderPersona, "You are a helpful agent.")
	a.Register("policy", sysprompt.SectionOrderPolicy, "Follow plan mode policy.")
	a.Register("tools", sysprompt.SectionOrderToolsSchema, "# tools schema")

	first := a.Assemble()
	firstHash := a.AssembleHash()
	for i := 0; i < 1000; i++ {
		if got := a.Assemble(); got != first {
			t.Fatalf("第 %d 次渲染不一致（纯函数破坏）", i)
		}
		if got := a.AssembleHash(); got != firstHash {
			t.Fatalf("第 %d 次哈希不稳定: %s != %s", i, got, firstHash)
		}
	}
}

// TestN03StaticCheckCatchesBanned detects forbidden patterns.
func TestN03StaticCheckCatchesBanned(t *testing.T) {
	bad := `Today is ` + "`time.Now()`" + ` in $(os.Getwd())` // Express the antipattern explicitly.
	// Construct text that triggers detection.
	dirty := "const now = time.Now(); const cwd = os.Getwd(); use math/rand"
	violations := sysprompt.StaticCheck(dirty)
	if len(violations) == 0 {
		t.Fatalf("应捕获 time.Now/os.Getwd/rand 反模式")
	}
	if sysprompt.IsPure(dirty) {
		t.Fatal("含反模式文本不应是纯函数")
	}
	_ = bad
}

// TestN03StaticCheckClean reports no violations for a clean section.
func TestN03StaticCheckClean(t *testing.T) {
	clean := "You are a helpful assistant. Follow the workspace policy."
	if v := sysprompt.StaticCheck(clean); len(v) != 0 {
		t.Fatalf("干净文本应无违规: %v", v)
	}
	if !sysprompt.IsPure(clean) {
		t.Fatal("干净文本应是纯函数")
	}
}

// TestN03CheckSections checks the entire assembler.
func TestN03CheckSections(t *testing.T) {
	a := sysprompt.New()
	a.Register("persona", sysprompt.SectionOrderPersona, "You are helpful.")
	a.Register("bad", sysprompt.SectionOrderPolicy, "os.Gos.Getwd() dynamic")
	_, forbidden := sysprompt.CheckSections(a)
	_ = forbidden
	// The os.Getwd section must be forbidden; recheck after correcting it.
	if !forbidden {
		t.Fatal("含反模式 section 应 forbidden")
	}
	// Removing the section must leave no violations.
	a.Unregister("bad")
	viol, forb := sysprompt.CheckSections(a)
	if len(viol) != 0 || forb {
		t.Fatalf("移除后应 0 violation: %v", viol)
	}
}

// TestN03OrderConst checks fixed section-order values.
func TestN03OrderConst(t *testing.T) {
	if sysprompt.SectionOrderPersona >= sysprompt.SectionOrderPolicy {
		t.Fatal("persona(100) 应小于 policy(200)")
	}
	// Assembler uses the supplied order; sections expose constant-style order fields.
	a := sysprompt.New()
	a.Register("policy", sysprompt.SectionOrderPolicy, "p")
	s := a.Sections()[0]
	if s.Order != sysprompt.SectionOrderPolicy {
		t.Fatalf("order 应来自常量 200, 实际 %d", s.Order)
	}
}

// M40 skill tests cover dynamic discovery/removal under .omnix/skills, six-layer authority with
// project-omnix rank 0 above bundled rank 5, tool injection and invocation policy, and polling
// change notifications.
package tests

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/herlanai/omnix/pkg/skills"
)

// writeSkill writes a skill Markdown file.
func writeSkill(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte("Description: "+name+"\n"+body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestSkillRegistryDynamicDiscovery reflects skill creation and removal in List.
func TestSkillRegistryDynamicDiscovery(t *testing.T) {
	root := t.TempDir()
	r := skills.New(root)
	projSkills := filepath.Join(root, ".omnix", "skills")

	// Initially empty.
	r.Scan()
	if got := len(r.List()); got != 0 {
		t.Fatalf("初始应为空, 实际 %d", got)
	}

	// Creating my-skill.md makes it appear in the next List.
	writeSkill(t, projSkills, "my-skill", "do the thing")
	r.Scan()
	if _, ok := r.Get("my-skill"); !ok {
		t.Fatal("新建技能后应能被发现")
	}
	if len(r.List()) != 1 {
		t.Fatalf("应有 1 个技能, 实际 %d", len(r.List()))
	}

	// Deleting it removes it from the next List.
	if err := os.Remove(filepath.Join(projSkills, "my-skill.md")); err != nil {
		t.Fatal(err)
	}
	r.Scan()
	if _, ok := r.Get("my-skill"); ok {
		t.Fatal("删除后不应再被发现")
	}
}

// TestSkillRankPrecedence prefers project-omnix rank 0 over bundled rank 5 for identical names.
func TestSkillRankPrecedence(t *testing.T) {
	root := t.TempDir()
	r := skills.New(root)
	writeSkill(t, filepath.Join(root, ".omnix", "skills"), "cmd", "project version")
	writeSkill(t, filepath.Join(root, ".config", "skills"), "cmd", "custom version") // rank 2

	r.Scan()
	s, ok := r.Get("cmd")
	if !ok {
		t.Fatal("应有 cmd")
	}
	if s.Rank != 0 {
		t.Fatalf("project-omnix 应胜 (rank 0), 实际 rank %d", s.Rank)
	}
	if !containsStr(s.Content, "project version") {
		t.Fatalf("内容应取 project 版本, 实际 %q", s.Content)
	}
	// Candidates include rank and locator information.
	cands := r.Candidates()
	if len(cands) != 1 || cands[0].Rank != 0 {
		t.Fatalf("候选应 1 个且 rank 0, 实际 %+v", cands)
	}
}

// TestSkillToolInjection verifies injected content and invocation policy.
func TestSkillToolInjection(t *testing.T) {
	root := t.TempDir()
	r := skills.New(root)
	writeSkill(t, filepath.Join(root, ".omnix", "skills"), "refactor", "Refactor tips")
	r.Scan()

	tool := skills.NewSkillTool(r)
	content, err := tool.Resolve("refactor", true) // by model
	if err != nil {
		t.Fatal(err)
	}
	if !containsStr(content, "Refactor tips") {
		t.Fatalf("应注入正文, 实际 %q", content)
	}
	// An unknown skill returns an error.
	if _, err := tool.Resolve("nope", true); err == nil {
		t.Fatal("不存在技能应报错")
	}
}

// TestSkillWatchChange invokes the polling callback after directory changes.
func TestSkillWatchChange(t *testing.T) {
	root := t.TempDir()
	r := skills.New(root)
	stop := make(chan struct{})
	var mu sync.Mutex
	calls := 0
	go r.Watch(stop, 20*time.Millisecond, func([]string) {
		mu.Lock()
		calls++
		mu.Unlock()
	})

	// Wait for the initial scan to finish.
	time.Sleep(60 * time.Millisecond)
	writeSkill(t, filepath.Join(root, ".omnix", "skills"), "newskill", "body")
	time.Sleep(120 * time.Millisecond)
	close(stop)

	mu.Lock()
	n := calls
	mu.Unlock()
	if n < 1 {
		t.Fatal("目录新增后 watch 应触发 onChange")
	}
	if _, ok := r.Get("newskill"); !ok {
		t.Fatal("watch 后技能应可见")
	}
}

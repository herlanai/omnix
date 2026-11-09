// M03 scoped registry primitives.
package tests

import (
	"strings"
	"testing"

	"github.com/herlanai/omnix/pkg/scope"
)

// TestScopeNearestWins verifies nearest-scope precedence over host definitions.
func TestScopeNearestWins(t *testing.T) {
	// The host defines bash and the session overrides it with a restricted version.
	host := scope.NewLayer[string](scope.Key("host"))
	host.Register("bash", "host/bash:full", 0)

	session := scope.NewLayer[string](scope.Key("session:s1"))
	session.Register("bash", "session/bash:restricted", 0)

	layers := scope.NewScopedLayers[string]().
		Push(host). // Resolve the host first.
		Push(session)

	// The nearest session scope must override the host.
	got, ok := layers.Get("bash")
	if !ok {
		t.Fatal("bash 应可解析")
	}
	if got != "session/bash:restricted" {
		t.Fatalf("nearest-scope-wins 失效: got %q", got)
	}

	// Resolve a key defined only at host level.
	host.Register("ls", "host/ls", 0)
	got, ok = layers.Get("ls")
	if !ok || got != "host/ls" {
		t.Fatalf("host 独有 key 解析失败: got %q ok=%v", got, ok)
	}

	// Missing key.
	if _, ok := layers.Get("nope"); ok {
		t.Fatal("不存在的 key 不应解析成功")
	}
}

// TestScopeRankTiebreak prefers higher rank, then later registration at equal rank.
func TestScopeRankTiebreak(t *testing.T) {
	layer := scope.NewLayer[int](scope.Key("session:s1"))

	// At equal rank, later registration wins.
	layer.Register("toolA", 1, 0)
	layer.Register("toolA", 2, 0) // Register later.

	// Resolve through ScopedLayers.
	layers := scope.NewScopedLayers[int]().Push(layer)
	if v, ok := layers.Get("toolA"); !ok || v != 2 {
		t.Fatalf("rank 相同应后注册覆盖: got %d ok=%v", v, ok)
	}

	// Higher rank wins over later registration.
	layer.Register("toolB", 10, 1)
	layer.Register("toolB", 20, 0) // Registered later but with lower rank.
	if v, ok := layers.Get("toolB"); !ok || v != 10 {
		t.Fatalf("rank 大者胜: got %d ok=%v", v, ok)
	}
}

// TestScopeAnonymousNotConflict keeps anonymous and named entries independent.
func TestScopeAnonymousNotConflict(t *testing.T) {
	layer := scope.NewLayer[string](scope.Key("host"))
	// Named entry.
	layer.Register("bash", "named-bash", 0)
	// Anonymous entry with an empty name.
	layer.Register("", "anonymous-default", 0)

	layers := scope.NewScopedLayers[string]().Push(layer)

	// Anonymous entries do not participate in named lookup; Get("bash") still finds the named entry.
	if v, ok := layers.Get("bash"); !ok || v != "named-bash" {
		t.Fatalf("具名查找应命中具名条目: got %q ok=%v", v, ok)
	}
	// Looking up an empty string must not match an anonymous entry.
	if _, ok := layers.Get(""); ok {
		t.Fatal("空串查找不应命中")
	}

	// Merge retains anonymous entries alongside named entries.
	merged := layers.Merge()
	hasAnon, hasNamed := false, false
	for _, e := range merged {
		if e.Name == "" {
			hasAnon = true
		}
		if e.Name == "bash" {
			hasNamed = true
		}
	}
	if !hasAnon || !hasNamed {
		t.Fatalf("匿名与具名条目应同时保留在 Merge 结果中: %+v", merged)
	}
}

// TestScopeMergeDeterministic verifies identical entry order across repeated merges.
func TestScopeMergeDeterministic(t *testing.T) {
	host := scope.NewLayer[string](scope.Key("host"))
	host.Register("zebra", "z", 0)
	host.Register("alpha", "a", 0)
	session := scope.NewLayer[string](scope.Key("session"))
	session.Register("mike", "m", 0)

	layers := scope.NewScopedLayers[string]().Push(host).Push(session)

	first := layers.Merge()
	second := layers.Merge()

	if len(first) != len(second) {
		t.Fatalf("两次 Merge 长度不一致")
	}
	for i := range first {
		if first[i].Name != second[i].Name || first[i].Value != second[i].Value {
			t.Fatalf("Merge 输出不确定: %+v vs %+v", first, second)
		}
	}

	// Named entries must be in lexical order.
	var names []string
	for _, e := range first {
		if e.Name != "" {
			names = append(names, e.Name)
		}
	}
	joined := strings.Join(names, ",")
	if joined != "alpha,mike,zebra" {
		t.Fatalf("具名条目应字典序: %q", joined)
	}
}

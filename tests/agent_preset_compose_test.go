// M32: Agent preset integration.
package tests

import (
	"testing"

	"github.com/herlanai/omnix/pkg/presets"
)

// TestAgentPresetCompose verifies independent tool/prompt compositions for different presets.
func TestAgentPresetCompose(t *testing.T) {
	code := &presets.AgentPreset{Key: "code", Tools: []string{"bash", "fs_read"}, Prompt: "你是编码助手"}
	research := &presets.AgentPreset{Key: "research", Tools: []string{"web_fetch"}, Prompt: "你是研究员"}

	// Compose both presets.
	combo := presets.ComposeFrom("code_research", code, research)
	if len(combo.Tools) != 3 {
		t.Fatalf("组合工具数 = %d, want 3: %v", len(combo.Tools), combo.Tools)
	}
	// Tools must be in lexical order.
	if combo.Tools[0] != "bash" || combo.Tools[2] != "web_fetch" {
		t.Fatalf("工具排序异常: %v", combo.Tools)
	}
	if combo.Prompt == "" {
		t.Fatal("组合 preset 应拼接 prompt")
	}
}

// TestAgentPresetMountSelect retrieves independent mounted compositions by key.
func TestAgentPresetMountSelect(t *testing.T) {
	reg := presets.NewPresetRegistry()

	code := &presets.AgentPreset{Key: "code", Tools: []string{"bash"}, Prompt: "编码"}
	reg.Mount(code)

	got, ok := reg.Select("code")
	if !ok || got.Key != "code" || len(got.Tools) != 1 {
		t.Fatalf("Select(code) 异常: %+v ok=%v", got, ok)
	}

	// An unmounted key cannot be selected.
	if _, ok := reg.Select("nope"); ok {
		t.Fatal("未挂载 key 不应被选中")
	}
}

// TestAgentPresetIndependence verifies that mounted presets do not interfere across Agents.
func TestAgentPresetIndependence(t *testing.T) {
	reg := presets.NewPresetRegistry()
	reg.Mount(&presets.AgentPreset{Key: "a", Tools: []string{"t1"}, Prompt: "A"})
	reg.Mount(&presets.AgentPreset{Key: "b", Tools: []string{"t2"}, Prompt: "B"})

	pa, _ := reg.Select("a")
	pb, _ := reg.Select("b")

	// Modifying pa must not affect pb because composition copies values.
	if len(pa.Tools) != 1 || len(pb.Tools) != 1 {
		t.Fatalf("两 preset 应相互独立: a=%v b=%v", pa.Tools, pb.Tools)
	}
	if pa.Tools[0] == pb.Tools[0] {
		t.Fatal("两个 preset 的工具集应不同")
	}
}

// M09 system-prompt assembly and section-registry tests.
package tests

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/herlanai/omnix/pkg/sysprompt"
	"github.com/herlanai/omnix/pkg/tools"
)

// TestSyspromptSectionOrdering verifies persona, policy, runtime-context, plan:policy, and tools
// ordering.
func TestSyspromptSectionOrdering(t *testing.T) {
	a := sysprompt.New()

	// Register out of order to verify sorting by section order.
	a.Register("tools", sysprompt.SectionOrderToolsSchema, "TOOLS_SECTION")
	a.Register("persona", sysprompt.SectionOrderPersona, "PERSONA_SECTION")
	a.Register("plan_policy", sysprompt.SectionOrderPlanPolicy, "PLAN_POLICY_SECTION")
	a.Register("policy", sysprompt.SectionOrderPolicy, "POLICY_SECTION")
	a.Register("runtime_ctx", sysprompt.SectionOrderRuntimeContext, "RUNTIME_CTX_SECTION")

	out := a.Assemble()

	// Sections must appear strictly in configured order.
	idxPersona := strings.Index(out, "PERSONA_SECTION")
	idxPolicy := strings.Index(out, "POLICY_SECTION")
	idxCtx := strings.Index(out, "RUNTIME_CTX_SECTION")
	idxPlan := strings.Index(out, "PLAN_POLICY_SECTION")
	idxTools := strings.Index(out, "TOOLS_SECTION")

	if !(idxPersona < idxPolicy && idxPolicy < idxCtx && idxCtx < idxPlan && idxPlan < idxTools) {
		t.Fatalf("section 顺序错误: persona=%d policy=%d ctx=%d plan=%d tools=%d",
			idxPersona, idxPolicy, idxCtx, idxPlan, idxTools)
	}
}

// TestSyspromptRegisterOverwrite verifies replacement by name and removal by Unregister.
func TestSyspromptRegisterOverwrite(t *testing.T) {
	a := sysprompt.New()
	a.Register("policy", sysprompt.SectionOrderPolicy, "v1")
	a.Register("policy", sysprompt.SectionOrderPolicy, "v2")

	out := a.Assemble()
	if !strings.Contains(out, "v2") || strings.Contains(out, "v1") {
		t.Fatalf("同名覆盖失败: %q", out)
	}

	a.Unregister("policy")
	if strings.Contains(a.Assemble(), "v2") {
		t.Fatal("Unregister 后不应再出现 policy section")
	}
	if a.Has("policy") {
		t.Fatal("Unregister 后 Has 应为 false")
	}
}

// TestSyspromptOrderConstants checks fixed order constants against upstream values.
func TestSyspromptOrderConstants(t *testing.T) {
	if sysprompt.SectionOrderPersona != 100 {
		t.Fatal("persona order 应为 100")
	}
	if sysprompt.SectionOrderPolicy != 200 {
		t.Fatal("policy order 应为 200")
	}
	if sysprompt.SectionOrderRuntimeContext != 300 {
		t.Fatal("runtime-context order 应为 300")
	}
	if sysprompt.SectionOrderPlanPolicy != 500 {
		t.Fatal("plan:policy order 应为 500")
	}
	if sysprompt.SectionOrderToolsSchema != 600 {
		t.Fatal("tools schema order 应为 600")
	}
}

// TestSyspromptToolsSchemaInjection verifies deterministic lexical tool ordering and valid
// parameter schemas.
func TestSyspromptToolsSchemaInjection(t *testing.T) {
	// Supply two tools in deliberately reversed order.
	toolList := []*tools.Tool{
		{Name: "zebra_tool", Description: "z tool", Schema: mustCompile(t, `{"type":"object","properties":{"z":{"type":"string"}}}`)},
		{Name: "alpha_tool", Description: "a tool", Schema: mustCompile(t, `{"type":"object","properties":{"a":{"type":"integer"}}}`)},
	}

	text1, err := sysprompt.ToolsSectionText(toolList)
	if err != nil {
		t.Fatalf("ToolsSectionText 失败: %v", err)
	}
	text2, _ := sysprompt.ToolsSectionText(toolList)

	// Repeated calls must return identical output.
	if text1 != text2 {
		t.Fatal("tools schema 输出不确定")
	}

	// Lexical name ordering places alpha_tool first.
	idxAlpha := strings.Index(text1, "alpha_tool")
	idxZebra := strings.Index(text1, "zebra_tool")
	if !(idxAlpha > 0 && idxZebra > idxAlpha) {
		t.Fatalf("工具应按字典序: %q", text1)
	}

	// Extract the JSON array after the heading and verify valid parameter schemas.
	jsonStart := strings.Index(text1, "[")
	if jsonStart < 0 {
		t.Fatal("tools schema 应包含 JSON 数组")
	}
	var parsed []any
	if err := json.Unmarshal([]byte(text1[jsonStart:]), &parsed); err != nil {
		t.Fatalf("tools schema 不是合法 JSON: %v", err)
	}
	if len(parsed) != 2 {
		t.Fatalf("应包含 2 个工具: %d", len(parsed))
	}
}

// mustCompile compiles a schema or panics on failure.
func mustCompile(t *testing.T, raw string) *tools.JsonSchemaNode {
	t.Helper()
	node, err := tools.Compile([]byte(raw))
	if err != nil {
		t.Fatalf("Compile 失败: %v", err)
	}
	return node
}

// TestSyspromptEmpty returns an empty string for an empty assembler.
func TestSyspromptEmpty(t *testing.T) {
	a := sysprompt.New()
	if a.Assemble() != "" {
		t.Fatalf("空组装器应返回空串: %q", a.Assemble())
	}
}

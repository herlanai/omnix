// S13 MCP bridge tests cover automatic tools/list conversion into callable tools, tools/call
// execution, fail-closed behavior for unsupported schema keywords, and isError propagation.
package tests

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/herlanai/omnix/pkg/mcp"
	"github.com/herlanai/omnix/pkg/tools"
)

// mockMCP implements an in-memory MCP JSON-RPC Transport.
type mockMCP struct {
	tools []mcp.MCPSchema
	calls int
}

func (m *mockMCP) RoundTrip(_ context.Context, req mcp.Request) (mcp.Response, error) {
	switch req.Method {
	case "initialize":
		return mcp.Response{ID: req.ID, Result: json.RawMessage(`{"protocolVersion":"2025-06-18","serverInfo":{"name":"mock"}}`)}, nil
	case "tools/list":
		b, _ := json.Marshal(map[string]any{"tools": m.tools})
		return mcp.Response{ID: req.ID, Result: b}, nil
	case "tools/call":
		m.calls++
		return mcp.Response{ID: req.ID, Result: json.RawMessage(`{"content":[{"type":"text","text":"mcp:ok"}],"isError":false}`)}, nil
	default:
		return mcp.Response{ID: req.ID, Result: json.RawMessage(`{}`)}, nil
	}
}

// TestMCPListToolsAutoRegister maps and invokes tools from the mock server.
func TestMCPListToolsAutoRegister(t *testing.T) {
	ctx := context.Background()
	server := &mockMCP{tools: []mcp.MCPSchema{
		{Name: "read_file", Description: "读取文件",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{"type": "string", "description": "文件路径"},
				},
				"required": []any{"path"},
			}},
		{Name: "list_dir", Description: "列目录",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},
	}}
	client := mcp.NewClient(server)
	if err := client.Initialize(ctx); err != nil {
		t.Fatal(err)
	}

	// Fetch tools automatically with tools/list.
	schemas, err := client.ListTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(schemas) != 2 {
		t.Fatalf("应列出 2 个工具，实际 %d", len(schemas))
	}

	// Bridge the tools into ToolDefinition values.
	bridge := mcp.NewBridge(client)
	tls, err := bridge.ToTools(schemas)
	if err != nil {
		t.Fatal(err)
	}
	if len(tls) != 2 {
		t.Fatalf("应映射 2 个 tool，实际 %d", len(tls))
	}
	if tls[0].Name != "read_file" || tls[0].Schema == nil {
		t.Fatalf("read_file 映射异常: %+v", tls[0])
	}

	// A successful tools/call returns text.
	out, err := tls[0].Execute(ctx, map[string]any{"path": "/a/b"})
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := out.(string); !ok || s != "mcp:ok" {
		t.Fatalf("read_file 调用结果应为 mcp:ok，实际 %v", out)
	}
	if server.calls != 1 {
		t.Fatalf("tools/call 应被调用 1 次，实际 %d", server.calls)
	}
}

// TestMCPUnsupportedSchemaFailClosed rejects unsupported schema keywords.
func TestMCPUnsupportedSchemaFailClosed(t *testing.T) {
	_, err := mcp.MapSchema(map[string]any{"type": "object", "anyOf": []any{}})
	if err == nil {
		t.Fatal("含 anyOf 的 schema 应 fail-closed 报错")
	}
}

// TestMCPToolIsError returns an error when MCP reports isError=true.
func TestMCPToolIsError(t *testing.T) {
	ctx := context.Background()
	client := mcp.NewClient(errorTransport{})
	_, err := client.CallTool(ctx, "failing", map[string]any{})
	if err == nil {
		t.Fatal("MCP isError=true 时 CallTool 应返回错误")
	}
}

// errorTransport is an always-failing transport stub.
type errorTransport struct{}

func (errorTransport) RoundTrip(ctx context.Context, req mcp.Request) (mcp.Response, error) {
	return mcp.Response{ID: req.ID, Result: json.RawMessage(`{"content":[],"isError":true}`)}, nil
}

// TestMCPToolMappedSchema verifies properties, required, and enum mapping.
func TestMCPToolMappedSchema(t *testing.T) {
	node, err := mcp.MapSchema(map[string]any{
		"type":                 "object",
		"properties":           map[string]any{"mode": map[string]any{"type": "string", "enum": []any{"a", "b"}}},
		"required":             []any{"mode"},
		"additionalProperties": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if node.Type != tools.TypeObject {
		t.Fatalf("type 应为 object，实际 %s", node.Type)
	}
	if len(node.Required) != 1 || node.Required[0] != "mode" {
		t.Fatalf("required 应含 mode，实际 %v", node.Required)
	}
	mode := node.Properties["mode"]
	if mode == nil || len(mode.Enum) != 2 {
		t.Fatalf("mode 子 schema enum 应为 [a b]，实际 %+v", mode)
	}
	if node.AdditionalProperties == nil || *node.AdditionalProperties {
		t.Fatal("additionalProperties=false 应被映射")
	}
}

// MCP client-to-tool bridging tutorial. Maps tools exposed by an MCP server into Agent-callable
// pkg/tools.Tool values, including schema validation. MCP uses JSON-RPC independently of transport
// (stdio, SSE, or in-memory); this example uses an in-memory server and needs no network or
// external process. Flow: NewClient -> Initialize -> ListTools -> bridge.ToTools -> tool.Execute.
// Run: go run ./examples/mcp. See pkg/mcp/mcp.go for Transport, Client, Bridge, and MapSchema.
package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/herlanai/omnix/pkg/mcp"
	"github.com/herlanai/omnix/pkg/tools"
)

func main() {
	ctx := context.Background()

	// 1. Use an in-memory server implementing mcp.Transport.RoundTrip for initialize, tools/list, and
	// tools/call.
	srv := newMemoryServer()
	client := mcp.NewClient(srv)

	// 2. Initialize MCP and negotiate the protocol version.
	if err := client.Initialize(ctx); err != nil {
		panic(err)
	}
	fmt.Println("— MCP 初始化握手成功 —")

	// 3. List tools exposed by the server using tools/list.
	schemas, err := client.ListTools(ctx)
	if err != nil {
		panic(err)
	}
	fmt.Printf("— MCP 服务器暴露工具数: %d —\n", len(schemas))
	for _, s := range schemas {
		fmt.Printf("  · %s — %s\n", s.Name, s.Description)
	}

	// 4. Convert MCP tools to pkg/tools.Tool so the Agent and pipeline can use them without knowing
	// their origin.
	bridge := mcp.NewBridge(client)
	toolList, err := bridge.ToTools(schemas)
	if err != nil {
		panic(err)
	}

	// 5. Execute through the common tool interface, equivalent to the Agent's tool/execute stage.
	fmt.Println("\n— 通过桥接后的 Tool 统一调用 —")
	for _, t := range toolList {
		// Each MCP tool provides a JSON Schema using the supported M48 subset. This example invokes it
		// directly; production callers register it with tools.NewPipeline().WithTool(t).
		out, err := t.Execute(ctx, demoInputFor(t.Name))
		if err != nil {
			fmt.Printf("  [%s] ❌ %v\n", t.Name, err)
			continue
		}
		fmt.Printf("  [%s] => %v\n", t.Name, out)
	}

	// 6. Add the bridged tools to the full four-stage waterfall pipeline. External tools then
	// participate in interception, approval, and restrictions just like local tools.
	fmt.Println("\n— 挂进 tools 流水线（与本地工具同权）—")
	p := tools.NewPipeline()
	for _, t := range toolList {
		p.WithTool(t)
	}
	// Execute directly for brevity; full waterfall logging is omitted here.
	fmt.Printf("  · 已注册 %d 个 MCP 工具到流水线\n", len(toolList))
}

// demoInputFor returns valid sample input for each demonstration tool.
func demoInputFor(name string) map[string]any {
	switch name {
	case "echo":
		return map[string]any{"text": "你好，MCP"}
	case "add":
		return map[string]any{"a": float64(2), "b": float64(3)}
	default:
		return map[string]any{}
	}
}

// In-memory MCP server implementing mcp.Transport. Real deployments use a stdio/SSE transport to
// connect to an external process; this example embeds server behavior for reproducible protocol
// exchanges.

// memoryServer handles three MCP JSON-RPC methods in memory.
type memoryServer struct {
	// Exposed tools could be stored here; real servers typically use a server-side registry.
}

// newMemoryServer constructs the in-memory server.
func newMemoryServer() *memoryServer {
	return &memoryServer{}
}

// RoundTrip implements mcp.Transport by returning a JSON-RPC response for each request.
func (s *memoryServer) RoundTrip(ctx context.Context, req mcp.Request) (mcp.Response, error) {
	switch req.Method {
	case "initialize":
		// Handshake: return the protocol version and server metadata.
		return ok(req.ID, map[string]any{
			"protocolVersion": "2025-06-18",
			"serverInfo":      map[string]any{"name": "demo-memory-server", "version": "0.0.1"},
		}), nil

	case "tools/list":
		// Expose two demonstration tools: echo and add.
		return ok(req.ID, map[string]any{"tools": []map[string]any{
			{
				"name":        "echo",
				"description": "回显输入文本",
				"inputSchema": map[string]any{
					"type":     "object",
					"required": []any{"text"},
					"properties": map[string]any{
						"text": map[string]any{"type": "string", "description": "要回显的文本"},
					},
				},
			},
			{
				"name":        "add",
				"description": "两数相加",
				"inputSchema": map[string]any{
					"type":     "object",
					"required": []any{"a", "b"},
					"properties": map[string]any{
						"a": map[string]any{"type": "number"},
						"b": map[string]any{"type": "number"},
					},
				},
			},
		}}), nil

	case "tools/call":
		// Execute the named tool and encode its result as an MCP response.
		name, _ := req.Params["name"].(string)
		args, _ := req.Params["arguments"].(map[string]any)
		switch name {
		case "echo":
			text, _ := args["text"].(string)
			return ok(req.ID, map[string]any{"content": []map[string]any{{"type": "text", "text": "echo: " + text}}}), nil
		case "add":
			a, _ := args["a"].(float64)
			b, _ := args["b"].(float64)
			return ok(req.ID, map[string]any{"content": []map[string]any{{"type": "text", "text": fmt.Sprintf("%.0f", a+b)}}}), nil
		default:
			return errResp(req.ID, fmt.Sprintf("unknown tool %q", name)), nil
		}
	}
	return errResp(req.ID, "method not found: "+req.Method), nil
}

// ok constructs a successful response.
func ok(id uint64, result map[string]any) mcp.Response {
	raw, _ := json.Marshal(result)
	return mcp.Response{ID: id, Result: raw}
}

// errResp constructs an error response.
func errResp(id uint64, msg string) mcp.Response {
	raw, _ := json.Marshal(map[string]any{"message": msg})
	return mcp.Response{ID: id, Error: raw}
}

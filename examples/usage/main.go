// This example embeds omnix in a Go service. It demonstrates event-sourced sessions and
// projections, Goal tools and slash commands, the DeepSeek provider and stable failure
// classification, and JSONL persistence. Run: go run ./examples/usage. It is self-contained and
// makes no real LLM requests; supply your own DEEPSEEK_API_KEY and call Chat to use the real API.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/commands"
	"github.com/herlanai/omnix/pkg/credentials"
	"github.com/herlanai/omnix/pkg/goal"
	"github.com/herlanai/omnix/pkg/llm"
	"github.com/herlanai/omnix/pkg/llm/provider_deepseek"
	"github.com/herlanai/omnix/pkg/persistence"
	"github.com/herlanai/omnix/pkg/session"
)

func main() {
	ctx := context.Background()

	// 1. Create an event-sourced session log and append events.
	sl := session.NewSessionLog(brand.NewSessionID("demo-session-1"))

	// Append a user message through the sole write path, which validates ordering invariants.
	if _, err := sl.Append(session.UserMessageData{Content: "帮我评估 omnix 的用法"}); err != nil {
		panic(err)
	}
	// Append a user/assistant exchange normally produced by the Agent.
	if _, err := sl.Append(session.AssistantMessageData{Content: "好的，我来分析。"}); err != nil {
		panic(err)
	}

	// Derive read-only projections without modifying events.
	proj := session.FoldAll(sl.Events())
	fmt.Printf("— 事件数=%d, 派生消息=%d —\n", len(sl.Events()), len(proj.Messages))
	for _, m := range proj.Messages {
		fmt.Printf("  [%s] %s\n", m.Role, m.Content)
	}

	// Use the incremental projection after Append (H04).
	sl.EnableIncrementalProjection()
	fmt.Printf("— 增量投影一层: goal.Present=%v, todo.Present=%v —\n",
		sl.Projection().Goal.Present, sl.Projection().Todo.Present)

	// 2. Goal tools: state machine, stable error codes, and BlockReason.
	toolset := goal.NewGoalToolset(sl)
	// List goal_* tools; registering them in the four-stage pipeline makes them available to the LLM.
	names := make([]string, 0, len(toolset.Tools()))
	for _, t := range toolset.Tools() {
		names = append(names, t.Name)
	}
	fmt.Printf("— goal 工具集: %v —\n", names)
	// Demonstrate the stable GOAL_INVALID_MAX_ROUNDS error for an invalid maxRounds value.
	var ge *goal.GoalError
	if _, err := callTool(toolset, "goal_set_max_rounds", map[string]any{"maxRounds": float64(-1)}); err != nil {
		if asErr, ok := err.(*goal.GoalError); ok {
			ge = asErr
		}
	}
	if ge != nil {
		fmt.Printf("— 稳定错误码: %s —\n", ge.Code)
	}

	// 3. Slash commands such as /plan on append plan/mode events rather than ordinary messages.
	cmd := commands.NewRegistry()
	out, err := cmd.Dispatch(ctx, "/plan on", sl)
	if err != nil {
		fmt.Printf("  /plan 执行错误: %v\n", err)
	} else {
		fmt.Printf("— 命令 /plan on => %s —\n", out)
	}

	// 4. Create the DeepSeek provider using an in-memory credential store and tuned connection pool;
	// inspect upstream-compatible failure classifications.
	store := credentials.NewMemoryStore()
	if err := store.Set(ctx, brand.NewCredentialRef("DEEPSEEK_API_KEY"), "sk-你的密钥"); err != nil {
		panic(err)
	}
	d := provider_deepseek.NewDeepSeek(store) // Optionally supply provider_deepseek.WithBaseURL(...).
	tp := d.Transport()
	fmt.Printf("— DeepSeek 连接池: MaxIdleConnsPerHost=%d, IdleConnTimeout=%v —\n",
		tp.MaxIdleConnsPerHost, tp.IdleConnTimeout)

	// Example real streaming call returning usage and StreamChunk values:
	// usage, err := d.Chat(ctx, llm.ChatRequest{Model: "deepseek-chat", Messages: ...},
	//     func(c llm.StreamChunk) { _ = c })

	// Map provider error text to stable failure kinds, matching upstream (R04).
	kind := llm.ClassifyProviderDetail("Insufficient_quota for the workspace")
	fmt.Printf("— provider 文本分类 => kind=%q —\n", kind)
	f := llm.NewProviderFailure("quota exceeded")
	fmt.Printf("— NewProviderFailure => kind=%q —\n", f.Kind)

	// 5. Persist the session to a local directory and load it again.
	dir := filepath.Join(".", "tmp-omnix-demo")
	_ = os.RemoveAll(dir)
	if err := persistDemo(ctx, sl, dir); err != nil {
		fmt.Printf("  persistence 演示: %v\n", err)
	}
	_ = os.RemoveAll(dir)

	fmt.Println("\n示例完成：omnix 可在 Go 服务中直接集成 DeepSeek Agent 规划能力。")
}

// callTool finds and invokes a tool by name.
func callTool(ts *goal.GoalToolset, name string, input map[string]any) (any, error) {
	for _, t := range ts.Tools() {
		if t.Name == name {
			return t.Execute(context.Background(), input)
		}
	}
	return nil, fmt.Errorf("demo: tool %q not found", name)
}

// persistDemo stores a JSONL header and events, flushes them, then reloads the session.
func persistDemo(ctx context.Context, sl *session.SessionLog, dir string) error {
	backend, err := persistence.NewJSONL(dir,
		100, // Flush asynchronously when a batch reaches 100 entries.
		persistence.WithShardCount(4))
	if err != nil {
		return err
	}
	defer backend.Close()
	id := sl.SessionID()

	// Write the header and append all events; Close flushes any remaining batch.
	if err := backend.SaveHeader(ctx, session.NewSessionHeader(id, "/workspace")); err != nil {
		return err
	}
	for _, ev := range sl.Events() {
		if err := backend.Append(ctx, id, ev); err != nil {
			return err
		}
	}
	// Explicit Flush creates a durable checkpoint before Load reads it back.
	if err := backend.Flush(ctx, id); err != nil {
		return err
	}
	_, loaded, err := backend.Load(ctx, id)
	if err != nil {
		return err
	}
	fmt.Printf("— 持久化 read-back: %d 条事件 —\n", len(loaded))
	return nil
}

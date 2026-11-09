// This example calls the real DeepSeek REST and SSE APIs for a multi-turn conversation. It reads
// DEEPSEEK_API_KEY into an in-memory credential store and DEEPSEEK_MODEL (default: deepseek-chat;
// deepseek-reasoner also emits reasoning chunks), builds system/user/assistant messages with
// temperature and length limits, streams reasoning/text/tool-call chunks, and prints usage
// including cache hits and misses. Run from the repository root with your own key from
// https://platform.deepseek.com. PowerShell: set $env:DEEPSEEK_API_KEY and optionally
// $env:DEEPSEEK_MODEL, then run go run ./examples/chat. Linux/macOS:
// DEEPSEEK_API_KEY="sk-your-key" DEEPSEEK_MODEL="deepseek-reasoner" go run ./examples/chat.
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/credentials"
	"github.com/herlanai/omnix/pkg/llm"
	"github.com/herlanai/omnix/pkg/llm/provider_deepseek"
)

func main() {
	// 1. Read the API key from the environment into the in-memory credential store. The provider
	// resolves it for each request so updates take effect immediately.
	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		fmt.Println("缺少 DEEPSEEK_API_KEY 环境变量，示例结束。")
		fmt.Println("请先设置：PowerShell 下执行  $env:DEEPSEEK_API_KEY=\"sk-你的密钥\"")
		os.Exit(1)
	}

	store := credentials.NewMemoryStore()
	if err := store.Set(context.Background(), brand.NewCredentialRef("DEEPSEEK_API_KEY"), apiKey); err != nil {
		fmt.Printf("写入凭证失败: %v\n", err)
		os.Exit(1)
	}

	// 2. Create the DeepSeek provider. Defaults include MaxIdleConnsPerHost=100 and
	// IdleConnTimeout=90s; WithBaseURL and WithTransport customize the endpoint and connection pool.
	d := provider_deepseek.NewDeepSeek(store)
	tp := d.Transport()
	fmt.Printf("— DeepSeek 连接池: MaxIdleConnsPerHost=%d, IdleConnTimeout=%v —\n",
		tp.MaxIdleConnsPerHost, tp.IdleConnTimeout)

	// 3. Build a multi-turn conversation using llm.Message{Role, []ContentBlock}. System is passed
	// separately and converted to a system-role message by the provider.
	messages := []llm.Message{
		llm.NewUserMessage("用一句话介绍你自己（你好，我是 omnix 示例）。"),
		// Include a previous assistant reply to form a user/assistant/user conversation.
		llm.NewAssistantText("你好！我是由 DeepSeek 模型驱动的 omnix 示例助手。"),
		llm.NewUserMessage("那你能用中文再给我讲一条 DeepSeek 官方的实用技巧吗？"),
	}

	// Read the model name from the environment, defaulting to deepseek-chat.
	model := os.Getenv("DEEPSEEK_MODEL")
	if model == "" {
		model = "deepseek-chat" // General chat model; use deepseek-reasoner for reasoning.
	}

	req := llm.ChatRequest{
		// Default to deepseek-chat; override it with DEEPSEEK_MODEL.
		Model:       model,
		System:      "你是一个非常友好、简洁的助手。请用中文回答，每次答复不超过 3 句话。",
		Messages:    messages,
		Temperature: 0.7,
		MaxTokens:   512,
	}

	// 4. Start streaming with an overall context timeout; cancellation produces a stable failure
	// classification. Dispatch reasoning chunks (deepseek-reasoner only), text chunks, and tool calls
	// (when tools were supplied). ChunkDone ends the stream, after which Chat returns final usage.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	fmt.Printf("\n— 当前模型: %s，正在流式回复 —\n", model)
	usage, err := d.Chat(ctx, req, func(c llm.StreamChunk) {
		switch c.Kind {
		case llm.ChunkReasoning:
			// Use a gray prefix to distinguish reasoning output.
			fmt.Printf("🧠 %s", c.Reasoning)
		case llm.ChunkText:
			fmt.Print(c.Text)
		case llm.ChunkToolCall:
			if c.ToolCall != nil {
				fmt.Printf("\n[工具调用] %s(%v)\n", c.ToolCall.Name, c.ToolCall.Input)
			}
		case llm.ChunkDone:
			fmt.Println("\n[流结束]")
		}
	})
	fmt.Println() // End the response with a newline.

	if err != nil {
		// Stable failure kinds such as overload, rate-limit, quota, and invalid-credential support retries
		// and user-facing errors.
		if f, ok := err.(*llm.LlmFailure); ok {
			fmt.Printf("\n❌ 调用失败 [kind=%s]: %s\n", f.Kind, f.Message)
		} else {
			fmt.Printf("\n❌ 调用失败: %v\n", err)
		}
		os.Exit(1)
	}

	// 5. Print usage statistics.
	fmt.Printf("\n— 用量 —\n")
	fmt.Printf("  prompt tokens      = %d\n", usage.PromptTokens)
	fmt.Printf("  completion tokens  = %d\n", usage.CompletionTokens)
	fmt.Printf("  cache hit tokens   = %d\n", usage.PromptCacheHitTokens)
	fmt.Printf("  cache miss tokens  = %d\n", usage.PromptCacheMissTokens)

	// 6. Optionally continue interactively; an empty line or exit ends the conversation. Append each
	// assistant reply to preserve user/assistant alternation and satisfy ReAct ordering checks.
	fmt.Println("\n— 交互式续聊（输入提问继续，输入 exit 退出）—")
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Print("\n你> ")
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" || strings.EqualFold(line, "exit") {
			break
		}
		// Append the user message while retaining conversation context.
		messages = append(messages, llm.NewUserMessage(line))
		req.Messages = messages

		// Give each turn a separate timeout; the outer 60-second budget applies only to the initial
		// request.
		roundCtx, roundCancel := context.WithTimeout(context.Background(), 30*time.Second)

		// Collect the assistant reply for this turn so it can be added to conversation history.
		var sb strings.Builder
		usage, err = d.Chat(roundCtx, req, func(c llm.StreamChunk) {
			if c.Kind == llm.ChunkText {
				sb.WriteString(c.Text)
				fmt.Print(c.Text)
			}
		})
		fmt.Println()
		if err != nil {
			if f, ok := err.(*llm.LlmFailure); ok {
				fmt.Printf("❌ [%s] %s\n", f.Kind, f.Message)
			} else {
				fmt.Printf("❌ %v\n", err)
			}
			break
		}
		// Append the assistant reply to preserve the multi-turn conversation.
		if sb.Len() > 0 {
			messages = append(messages, llm.NewAssistantText(sb.String()))
		}
		// Release this turn's timeout resources to avoid accumulating timers and goroutines.
		roundCancel()
	}
}

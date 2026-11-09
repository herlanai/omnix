# 运行 Agent

[文档目录](README.md) · [English](../en/agent.md) · [工具](tools.md)

`Agent` 在 Go 进程内执行模型请求和工具调用。构造函数接收会话日志、适配器，以及可选的提示词和工具组件。日志保存在内存中，存储接入由宿主另外负责。

## 从离线适配器开始

使用 [`go.mod`](../../go.mod) 指定的 Go 版本。将以下完整程序保存为 `/tmp/omnix-agent-guide.go`，在仓库根目录执行 `go run /tmp/omnix-agent-guide.go`。程序不需要凭证或网络连接。

```go
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/herlanai/omnix/pkg/agent"
	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/llm"
	"github.com/herlanai/omnix/pkg/session"
)

type offlineAdapter struct{}

func (offlineAdapter) Name() string { return "offline" }

func (offlineAdapter) Chat(ctx context.Context, req llm.ChatRequest, emit func(llm.StreamChunk)) (llm.Usage, error) {
	if err := ctx.Err(); err != nil {
		return llm.Usage{}, err
	}
	emit(llm.StreamChunk{Kind: llm.ChunkText, Text: "Hello from the offline adapter."})
	emit(llm.StreamChunk{Kind: llm.ChunkDone})
	return llm.Usage{}, nil
}

func main() {
	id := brand.NewSessionID("offline-guide")
	log := session.NewSessionLog(id)
	a := agent.NewAgent(id, log, nil, nil, offlineAdapter{}, agent.AgentOptions{
		DefaultTimeout: 5 * time.Second,
	})
	defer a.Dispose()

	if err := a.Run("Hello"); err != nil {
		panic(err)
	}
	waitCtx, stopWaiting := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopWaiting()
	if err := a.WhenIdle(waitCtx); err != nil {
		a.Cancel(agent.CancelUser)
		panic(err)
	}

	// Idle is a lifecycle state, not a successful execution result.
	for _, event := range log.Events() {
		switch data := event.Data.(type) {
		case session.AssistantMessageData:
			fmt.Println(data.Content)
		case session.AgentErrorData:
			fmt.Printf("execution error: %s\n", data.Message)
		case session.TurnEndData:
			fmt.Printf("turn %d: %s\n", data.Turn, data.Reason)
		}
	}
}
```

输出为 `Hello from the offline adapter.`，随后是 `turn 0: finished`。当前循环使用值为 `finished` 的 `ReasonFinished`；另外定义的 `ReasonCompleted` 是不同的值。

## 装配应用边界

[`NewAgent`](../../pkg/agent/agent.go) 接收 `(id, log, prompt, pipeline, adapter, options...)`。应传入有效日志，并使用创建该日志时相同的会话 ID。提示词组装器和流水线可以为 nil。构造时允许传入 nil 适配器，但执行 step 时会失败。

[`LLMAdapter`](../../pkg/llm/llm.go) 接口有两个方法：`Name` 和 `Chat`。`Chat` 接收请求，通过回调按顺序交付分片，再返回用量或错误。适配器应顺序调用回调，并在返回前停止使用回调；通过传入的上下文响应取消。

内置网络适配器是 [`provider_deepseek`](../../pkg/llm/provider_deepseek/deepseek.go)。[`examples/chat`](../../examples/chat/main.go) 展示凭证配置和直接请求。其他服务需要自行实现并注入 `LLMAdapter`；设置 `AgentOptions.Provider` 不会切换适配器。

`Model`、`MaxTokens` 和 `ReasoningEffort` 会进入请求；模型名为空时，当前默认值是 `deepseek-chat`。[工具流水线](tools.md) 提供模型可见的 schema 和执行查找。在提交任务前完成配置，不要在执行期间修改流水线或工具定义。

## 提交和等待

`Run` 按需启动 worker 并将一个 turn 入队。`Followup` 将另一个 turn 入队，不会向当前模型流注入输入。worker 顺序处理 turn，每个 turn 可以包含多个模型与工具 step。

提交成功只表示接受，不表示完成。使用 `errors.Is` 处理 `agent.ErrTurnQueueFull` 和 `agent.ErrDisposed`。队列有容量限制，但 worker 会并发取出请求，因此不要依赖固定数量的提交必定被接受。

提交成功后调用 `WhenIdle(ctx)`。它等待当前忙碌期间已接受的工作结束；后续提交可能开启新的忙碌期间。返回值只表示等待结果，不表示模型或工具是否成功。

## 控制执行生命周期

| 方法 | 行为 |
|---|---|
| `SetRunContext(parent, timeout)` | 设置 turn 开始时使用的父上下文和超时。非正 timeout 回落到 `DefaultTimeout`；两者为零时只受父上下文取消约束。 |
| `Cancel(cause)` | 取消活动执行，丢弃排队请求。 |
| `CancelWithOptions(cause, agent.CancelOptions{KeepInbox: true})` | 取消活动执行，保留排队请求。 |
| `Dispose()` | 永久关闭 Agent，取消工作并等待 worker；支持重复和并发调用。 |

传给 **WhenIdle** 的上下文超时只会停止等待。停止执行需要调用 `Cancel`，或取消通过 `SetRunContext` 设置的父上下文。已经取消的父上下文也会影响之后继续使用它的 turn。

取消是协作式的。忽略上下文的工具或适配器可能使 `Dispose` 一直等待。不要在 Agent 自己的工具回调中同步调用 `Dispose`：它会等待正在执行该回调的 worker。

## 读取结果和错误

等待结束后读取 `log.Events()`，检查具体事件载荷：

- `AssistantMessageData` 包含累计的助手文本和推理内容；取消前收到的部分文本会保留。
- `ToolResultData` 包含 `IsError` 和输出文本。工具失败通常会成为下一次模型 step 的输入，而不是立即令整个 turn 失败。
- `AgentErrorData` 记录执行错误。当前 `TurnEndData.Reason` 正常结束为 `finished`，取消或 deadline 错误为 `aborted`，其他执行错误为 `interrupted`。

直接调用适配器时，`Chat` 会返回错误。使用 `errors.As` 提取 `*llm.LlmFailure`，使用 `errors.Is` 检查包装后的上下文错误。Agent 持久化的错误事件是文本，不是原始 Go error 对象。

复用同一日志能让新的执行器读取既有对话，包括工具调用和结果。不要让两个执行器同时驱动同一日志。加载得到的事件切片不会自动成为可运行的 `SessionLog`；存储、恢复、检查点和重启编排由宿主负责。应将返回的事件载荷视为只读。

构造函数不会自动接入重试、provider 路由、压缩、Goal 调度或完整的 Inbox 实时引导驱动。应用需要这些能力时，应显式组合。

## 验证行为

参考 [Agent 实现](../../pkg/agent/agent.go)、[生命周期和重放测试](../../pkg/agent/agent_test.go)，以及[离线工具循环示例](../../examples/agent_loop/main.go)。

```sh
go test ./pkg/agent
go run ./examples/agent_loop
```

# omnix

**可嵌入 Go 应用的 Agent 运行时，支持持久会话状态与工具调用。**

[English](README.md) · [简体中文](README.zh-CN.md)

omnix 将 Agent 执行循环、工具调用和可重放的会话状态放在同一个 Go 进程中。核心由追加式事件日志、状态投影、四阶段工具流水线和 DeepSeek 流式适配器组成，规划、持久化、子代理和工作流则以独立包提供。

项目仍在持续开发。可以先运行本地示例，再按应用需求装配各个组件。

## 可以用来做什么

- **构建历史可追踪的 Agent。** 将轮次、模型回复、工具调用和结果记录为会话事件，派生当前状态，或重放已保存的对话。
- **为工具配置执行策略。** 通过中间件校验参数，处理审批、访问限制、超时和结果加工。
- **管理有状态的规划。** 在会话中维护目标、待办和计划模式，并按需向模型提供规划工具。
- **接入 Go 应用。** 注入模型适配器、注册工具、选择持久化方式，在应用内组合子代理和工作流。

## 快速开始

需要 **Go 1.25 或更新版本**。SQLite 支持使用纯 Go 的 `modernc.org/sqlite` 驱动。

```bash
git clone https://github.com/herlanai/omnix.git
cd omnix
go run ./examples/agent_loop
go run ./examples/tutorial
```

这两个示例均不需要模型 API Key。`agent_loop` 使用脚本化适配器演示完整的工具调用闭环。`tutorial` 介绍会话与投影；其中的目标工具部分尚未创建目标，因此当前会输出 `GOAL_NOT_FOUND`。

在已有 Go 模块中引入：

```bash
go get github.com/herlanai/omnix
```

### 创建会话

会话状态由事件日志派生。如果需要随事件追加反复读取状态，可以启用增量投影。

```go
package main

import (
	"fmt"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/session"
)

func main() {
	log := session.NewSessionLog(brand.NewSessionID("hello"))
	log.EnableIncrementalProjection()

	if _, err := log.Append(session.UserMessageData{Content: "Hello, omnix!"}); err != nil {
		panic(err)
	}

	for _, message := range log.Projection().Messages {
		fmt.Printf("%s: %s\n", message.Role, message.Content)
	}
}
```

输出：

```text
user: Hello, omnix!
```

### 运行 Agent

[Agent 循环示例](examples/agent_loop/main.go) 展示了完整装配过程：将 `SessionLog`、系统提示词、工具 `Pipeline` 和 `llm.LLMAdapter` 传入 `agent.NewAgent`。

执行生命周期是异步的：

1. 启动前完成适配器、工具和中间件配置。
2. 使用 `SetRunContext` 设置执行上下文。
3. 通过 `Run` 或 `Followup` 提交输入；返回值表示输入是否已被接受。
4. 使用 `WhenIdle(ctx)` 等待已接受的工作结束；从会话事件中读取模型或工具错误，以及轮次结束原因。
5. 使用 `Cancel` 取消活动执行，在不再使用 Agent 时调用 `Dispose`。

`WhenIdle` 的上下文超时只会结束等待。执行的取消由运行上下文或 `Cancel` 控制，工具实现需要响应自己的上下文。

### 体验 DeepSeek 适配器

聊天示例会发起真实 API 请求，需要提供自己的凭证：

```bash
export DEEPSEEK_API_KEY="your-api-key"
export DEEPSEEK_MODEL="deepseek-chat"
go run ./examples/chat
```

`DEEPSEEK_MODEL` 可省略，示例默认使用 `deepseek-chat`。该示例直接演示模型流式访问；工具派发和会话记录的用法请参考 Agent 循环示例。

## 工作方式

```text
Application input
       |
       v
Agent turn / step loop <----> LLM adapter
       |                         |
       |                    Tool requests
       |                         v
       |        pre -> execute -> post -> result
       |                         |
       +------ session events ---+
                    |
                    v
             Append-only log
              /           \
       Projections     Persistence
```

一个轮次可以包含多次模型请求与工具执行。事件记录执行过程，投影提供当前状态视图，持久化保存历史以供加载和重放。压缩通过表面替换调整可见历史，保留原始事件。

## 模块

| 领域 | 包 | 职责 |
| --- | --- | --- |
| 运行时 | [`agent`](pkg/agent)、[`session`](pkg/session)、[`eventbus`](pkg/eventbus) | 执行生命周期、事件历史、投影、通知 |
| 模型访问 | [`llm`](pkg/llm)、[`provider_deepseek`](pkg/llm/provider_deepseek) | 适配器接口、流式响应、用量、错误分类 |
| 工具 | [`tools`](pkg/tools)、[`approval`](pkg/approval)、[`sandbox`](pkg/sandbox) | 参数 Schema、中间件、访问决策、执行约束 |
| 规划 | [`goal`](pkg/goal)、[`todo`](pkg/todo)、[`plan`](pkg/plan)、[`skills`](pkg/skills) | 目标状态、待办、计划模式、技能发现 |
| 存储 | [`persistence`](pkg/persistence)、[`sqlite`](pkg/persistence/sqlite)、[`storage`](pkg/storage) | JSONL、SQLite/FTS5、作用域存储 |
| 编排 | [`subagent`](pkg/subagent)、[`workflow`](pkg/workflow)、[`mcp`](pkg/mcp) | 子代理接口、工作流、MCP 工具桥接 |
| 本地执行 | [`fs`](pkg/fs)、[`shell`](pkg/shell)、[`subprocess`](pkg/subprocess)、[`jobs`](pkg/jobs) | 文件、命令、进程、后台任务 |
| 可观测性 | [`telemetry`](pkg/telemetry)、[`tokenmeter`](pkg/tokenmeter) | 会话指标、Token 估算与预算 |

## 示例

在仓库根目录运行以下命令。

| 命令 | 内容 |
| --- | --- |
| `go run ./examples/tutorial` | 事件、投影与目标工具校验 |
| `go run ./examples/agent_loop` | 使用脚本化适配器完成模型请求、工具执行和轮次结束 |
| `go run ./examples/usage` | SDK 组件与 JSONL 持久化 |
| `go run ./examples/todo` | 待办写入与状态派生 |
| `go run ./examples/workflow` | 串行与并行编排 |
| `go run ./examples/subagent` | 子代理后端与生命周期 |
| `go run ./examples/sandbox_approval` | 沙箱模式与审批策略解析 |
| `go run ./examples/mcp` | 使用内存传输实现 MCP 工具桥接 |
| `go run ./examples/chat` | DeepSeek 流式对话，需要 API Key |

## 性能

近期优化减少了事件分发的内存分配，使用合并区间过滤可见历史，并去掉了增量预设比较中的反射。以下为 Apple M5、macOS arm64、Go 1.26.2 环境中三次测试的中位数。

| 微基准场景 | 优化前 | 优化后 |
| --- | ---: | ---: |
| 向 16 个监听器分发事件 | 34.17 ns/op；1 alloc/op | 13.28 ns/op；0 allocs/op |
| 过滤含替换区间的 10,000 条事件 | 215.73 μs/op | 99.27 μs/op |
| 增量处理 10,000 条事件 | 1.010 ms/op；21,683 allocs/op | 0.493 ms/op；1,683 allocs/op |

结果取决于硬件和工作负载。事件总线将列表复制放在注册与注销阶段，使分发无需分配内存，适合分发频繁、监听器变更较少的场景。

复现当前实现的基准：

```bash
go test ./pkg/eventbus ./pkg/session -run '^$' \
  -bench 'BenchmarkEmit|BenchmarkEffectiveEvents' -benchmem -count=3
go test ./tests -run '^$' \
  -bench '^BenchmarkIncrementalH04IncrementalPath$' -benchmem -count=3
go test ./tests -run '^$' -bench '^BenchmarkSQLiteSearch$' -benchmem
```

## 开发与验证

```bash
gofmt -w examples internal pkg tests
go vet ./...
go test ./...
go test -race ./...
```

测试包含本地 HTTP/SSE 模拟服务，运行环境需要允许监听回环地址。代码注释统一使用英文；行为变更应补充回归测试，性能改动使用独立基准衡量。

## 当前实现范围

内置模型适配器面向 DeepSeek，其他模型服务需要实现 `llm.LLMAdapter`。实际请求由构造 Agent 时传入的适配器处理，`AgentOptions.Provider` 尚不提供运行时 Provider 路由。

MCP 网络传输、完整的 Inbox steering，以及压缩和重试策略的自动装配仍需进一步集成。沙箱约束能力因后端而异，Windows 实现仍有未完成的约束路径；部署前应核对对应包的实现。

详细行为见 [SDK 执行链路与集成边界](docs/zh-CN/agent.md)。

## 文档

从[简体中文文档](docs/zh-CN/README.md)或 [English guides](docs/en/README.md) 开始阅读。

- [运行时架构](docs/zh-CN/architecture.md)
- [运行 Agent](docs/zh-CN/agent.md)
- [工具与执行策略](docs/zh-CN/tools.md)
- [会话持久化](docs/zh-CN/persistence.md)
- [用量与性能观测](docs/zh-CN/observability.md)
- [DeepSeek 网页搜索和抓取](docs/zh-CN/web-tools.md)
- [开发指南](docs/zh-CN/development.md)

## 来源与许可证

omnix 起源于 `dsh-go`，设计参考了 DeepSeek Harness。本仓库独立演进 Go 实现，不声明与上游全部功能完全兼容。

采用 [MIT License](LICENSE)，保留原始版权声明。

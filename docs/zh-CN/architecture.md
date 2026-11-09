# 运行时架构

[指南目录](README.md) · [English](../en/architecture.md)

运行时在宿主 Go 进程内装配。宿主向 [`agent.NewAgent`](../../pkg/agent/agent.go) 提供会话日志、LLM 适配器、系统提示词组装器和工具流水线。该构造函数不会启动守护进程、HTTP 服务、数据库连接或模型 Provider 自动发现。

## 请求流程

```text
Run / Followup
      |
      v
Bounded turn queue -> single worker -> turn/start + user/message
                                           |
                                           v
                                  Assemble model request
                                           |
                                           v
                                      Adapter.Chat
                                           |
                               Record assistant output
                                           |
                                  Tool calls returned?
                                   /              \
                                 yes              no
                                  |                |
                            Tool pipeline      turn/end
                                  |
                       Record results; next step
```

[`runTurn` 和 `runStep`](../../pkg/agent/agent.go) 实现上述流程。工作协程串行处理已接受的轮次；每一步从会话历史构造模型消息，调用适配器，收集流式文本、推理和工具调用，再按返回顺序执行工具。包含工具调用的步骤会进入下一次模型请求。成功结束时，当前实现记录的轮次结束原因是 `finished`。

`Run` 只报告是否入队成功，`WhenIdle` 等待当前忙碌阶段结束，两者均不返回模型答案。输出和执行错误从事件日志读取，生命周期及取消用法见 [Agent 指南](agent.md)。

## 事件与投影

[`SessionEvent`](../../pkg/session/session.go) 包含序号、时间戳、事件类型、类型化载荷和可选的表面操作元数据。`SessionLog.Append` 接受事件前会检查时序约束，包括轮次与步骤配对，以及工具调用与结果配对。

日志是状态来源。投影函数从日志派生消息、目标、待办、请求头、预设等视图。以下程序无需 Agent 或模型，即可演示投影 API：

```go
package main

import (
	"fmt"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/session"
)

func main() {
	log := session.NewSessionLog(brand.NewSessionID("projection-demo"))
	log.EnableIncrementalProjection()
	if _, err := log.Append(session.UserMessageData{Content: "Hello"}); err != nil {
		panic(err)
	}
	for _, message := range log.Projection().Messages {
		fmt.Printf("%s: %s\n", message.Role, message.Content)
	}
}
```

输出：`user: Hello`。

- `FoldAll(events)` 从传入的事件切片重建聚合状态。
- `EnableIncrementalProjection()` 建立初始投影，并在后续追加事件时增量更新。
- `Projection()` 返回当前聚合视图；使用增量状态时会复制外层消息切片。
- `ProjectionMeta()` 省略消息，在增量路径上避免复制消息切片。

嵌套切片和指针仍可能共享。应将返回的事件载荷和投影按只读数据使用，修改前自行复制。单独使用 `IncrementalFolder` 时由调用方同步；`SessionLog` 使用互斥锁保护自身的追加和投影操作。

增量投影优化的是上述投影 API。当前 Agent 构造模型请求时仍调用 `log.Events()` 并重建模型消息，因此启用投影不会消除 Agent 循环中的全部历史处理开销。

## 可见历史与压缩

[`SurfaceOp`](../../pkg/session/surface.go) 表示读取时对一个序号区间进行替换。[`EffectiveEvents`](../../pkg/session/fold.go) 过滤被隐藏事件，同时保持输入顺序。实现会合并替换区间并查询区间，避免为每个序号值分配条目，尤其避免展开稀疏日志中并不存在的序号。

这会改变可见对话，而不改写原始事件切片。[压缩包](../../pkg/compaction) 提供独立的辅助能力，Agent 构造函数不会自动安排或安装压缩。应用决定何时压缩、保留哪些上下文，以及如何持久化产生的事件。

## 模块职责

| 层次 | 主要包 | 职责 |
| --- | --- | --- |
| 执行 | [`agent`](../../pkg/agent)、[`llm`](../../pkg/llm)、[`tools`](../../pkg/tools) | 模型与工具循环、执行契约 |
| 状态 | [`session`](../../pkg/session)、[`goal`](../../pkg/goal)、[`todo`](../../pkg/todo)、[`plan`](../../pkg/plan) | 事件记录与规划状态派生 |
| 提示词构造 | [`sysprompt`](../../pkg/sysprompt)、[`skills`](../../pkg/skills) | 提示词区段、动态上下文、技能目录 |
| 存储 | [`persistence`](../../pkg/persistence)、[`storage`](../../pkg/storage) | 显式事件持久化与作用域键值存储 |
| 宿主策略 | [`approval`](../../pkg/approval)、[`sandbox`](../../pkg/sandbox)、[`credentials`](../../pkg/credentials) | 审批、执行约束、凭证访问 |
| 编排 | [`workflow`](../../pkg/workflow)、[`subagent`](../../pkg/subagent)、[`mcp`](../../pkg/mcp) | 编排和可注入的集成接口 |

包存在不代表它已经在 Agent 循环中启用。例如，`mcp.Transport` 需要由宿主提供，持久化需要显式接线，审批中间件需要挂到实际执行路径。模型请求由传给 `NewAgent` 的适配器处理；`AgentOptions.Provider` 不实现动态 Provider 路由。

## 状态与生命周期边界

独立对话使用独立的 Agent 与日志。启动前配置工具定义，在执行期间保持稳定。事件总线使用不可变监听器快照，使回调在锁外执行；监听器变更会创建新列表，适合分发频繁、注册变更较少的场景。

宿主负责关闭流程：停止新提交，按需取消执行，释放 Agent，并完成存储和导出器的关闭。上下文取消需要被调用方配合，无法强制终止任意 Go 回调。存储细节见 [持久化指南](persistence.md)，策略接线见 [工具指南](tools.md)。

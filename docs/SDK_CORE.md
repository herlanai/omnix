# Go SDK 核心执行链路

本次以 [DeepSeek Harness Agent Loop](https://github.com/deepseek-ai/deepseek-harness/tree/master/packages/core/agent-loop) 的模型请求、工具执行、会话历史和取消契约为参考，补通现有 Go SDK 中的运行断点。不是对最新上游全部功能的兼容声明，原有上游版本锚点保持不变。

## 使用方式

```go
pipeline := tools.NewPipeline().WithTool(&tools.Tool{
    Name: "lookup",
    Description: "查询资料",
    Schema: &tools.JsonSchemaNode{Type: tools.TypeObject},
    Execute: func(ctx context.Context, input map[string]any) (any, error) {
        return "查询结果", nil
    },
})
a := agent.NewAgent(id, log, prompt, pipeline, adapter, agent.AgentOptions{
    Model: "deepseek-chat",
    MaxTokens: 4096,
    DefaultTimeout: 30 * time.Second,
})
defer a.Dispose()
if err := a.Run("请查询资料"); err != nil { return err }
if err := a.WhenIdle(ctx); err != nil { return err }
```

完整可运行示例：`go run ./examples/agent_loop`。Pipeline 的工具注册同时提供模型 schema 和执行查找，不必再复制一套 `SetToolProvider`。工具及中间件应在启动 Agent 前装配；运行期间不要修改 Pipeline 或工具定义。

`Run`/`Followup` 返回入队结果；`WhenIdle` 等待所有已接受工作达到静止，不返回模型运行错误。运行错误与完成原因从会话事件读取。`WhenIdle` 的等待上下文超时只结束等待；需要终止执行时调用 `Cancel`。

## 已接通的行为

- 模型请求使用配置的 Model、MaxTokens、ReasoningEffort。推理参数的可用值由具体模型决定，SDK 不推断模型能力。
- 注册工具按名称稳定排序进入模型请求；多工具流水线只执行匹配的工具。未知工具、schema 验证失败、中间件错误、工具 panic 均产生错误结果。
- 普通和对象池执行路径共享阶段逻辑，保持取消、错误码和校验行为一致。TimeoutMs 为协作式工具超时，工具必须尊重上下文。
- 助手文本及推理写入最终消息事件；下一步请求和重建 Agent 可从同一 SessionLog 重放助手消息、工具调用和结果。错误结果保留错误文本，压缩隐藏的旧事件不会重新进入请求。
- DeepSeek 工具参数按流式 index 合并；完整参数通过 JSON 对象校验后才发送调用回调。请求编码采用标准 assistant.tool_calls 和 role=tool/tool_call_id，不再把工具结果伪装成普通文本。参考 [DeepSeek Tool Calls](https://api-docs.deepseek.com/guides/tool_calls/)。
- Cancel 真正取消活动上下文并清空排队请求；CancelWithOptions(..., CancelOptions{KeepInbox: true}) 保留队列。已收到的文本在取消后仍保留。Dispose 可重复、可并发调用，取消活动执行，关闭后拒绝新请求。
- JSONL Flush 等待进行中的写入，缓冲移交与写入顺序由同一锁保护。写失败恢复所有尚未写入的会话缓冲；崩溃恢复补齐独立 tool/call 的错误结果并避免重复补写。
- 子进程完成通知等待 stdout/stderr 收集结束，保证大输出及 spill 读取完整；修复 Unix 信号常量类型导致的编译失败。

## 验证与边界

新增测试覆盖工具闭环与会话重建、取消及部分文本、并发释放、排队等待、默认超时、SSE 交错工具分片、非法参数拒绝、双执行路径治理、持久化刷新屏障与失败重试。现有测试中三处过时的 fixture 已修正：非法评分不再用于列表测试，崩溃测试显式 Flush 并验证三类补写，沙箱路径使用本机临时目录。

本次未接入真实 API Key，模型网络行为通过本地模拟服务和协议测试验证。仍未完成最新上游全部功能对齐，例如 MCP 的实际网络传输、运行期 provider 注册/路由、完整 Inbox steering 驱动、自动压缩与重试策略的统一装配。`AgentOptions.Provider` 仍不能替代传入的 adapter；当前调用的是构造时提供的适配器。超时和取消不能强制终止不遵守 context 的用户工具。

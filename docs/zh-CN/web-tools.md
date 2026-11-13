# DeepSeek 网页工具

[文档导航](README.md) · [English](../en/web-tools.md)

[DeepSeek Provider](../../pkg/llm/provider_deepseek/web.go) 同时提供直接调用 API 的方法和可接入现有 Agent 循环的工具。接口参考：[Search](https://api-docs.deepseek.com/zh-cn/api/search/) 和 [Fetch](https://api-docs.deepseek.com/zh-cn/api/fetch/)。

## 启用模型自主检索

先在凭证存储中设置 `DEEPSEEK_API_KEY`，再在启动 Agent 前注册工具。已有应用准备好会话 ID、日志和提示词组装器后，接入方式如下：

```go
provider := provider_deepseek.NewDeepSeek(store)
pipeline := tools.NewPipeline()
for _, tool := range provider.WebTools() {
    pipeline.WithTool(tool)
}
a := agent.NewAgent(id, log, prompt, pipeline, provider)
defer a.Dispose()
```

对应导入路径为 `github.com/herlanai/omnix` 下的 `pkg/llm/provider_deepseek`、`pkg/tools` 和 `pkg/agent`。`store` 是 `*credentials.Store`，每次请求都会重新解析凭证。同一个 Provider 可同时处理聊天和网页请求，共用 HTTP 客户端与连接池。支持工具调用的其他模型适配器也可以使用这两个工具。

注册后，模型可以看到 `deepseek_search` 和 `deepseek_fetch`，自主选择是否调用及其参数。Agent 通过流水线执行调用、记录结果，并在下一次模型请求中回传结果，因此能够完成搜索、选择来源、抓取正文和回答的连续流程，无需应用根据关键词分派。工具失败也会作为错误结果回传给模型。普通问题不强制搜索。

建议在系统提示词中要求按需检索、引用来源 URL，并将网页文本作为不可信证据而非指令。已有审批和执行前中间件仍然生效。请在并发执行前完成工具配置。

完整可运行示例见 [examples/web_agent](../../examples/web_agent/main.go)：

```sh
# Set DEEPSEEK_API_KEY in your environment first.
# Optionally set DEEPSEEK_MODEL.
go run ./examples/web_agent "Research Go's release policy and cite official sources"
```

该示例调用真实 API，单轮总超时为三分钟，支持 Ctrl+C，单次工具调用超时为 60 秒。网页请求遵循[官方使用与计费说明](https://api-docs.deepseek.com/zh-cn/guides/search/)，与模型调用分别计费。

## 直接调用

`provider.Search(ctx, SearchRequest{Query: "..."})` 返回 `*SearchResponse`。`Count` 可设为 1–50，零值表示省略，由服务使用默认值 10。`OriginalQuestion` 是可选字符串指针，`Localization` 是可选 IP 地址，`NeedContent` 用于请求正文。搜索语法原样传递。模型工具 Schema 不接受显式传入零条结果。

`provider.Fetch(ctx, "https://example.com")` 返回 `*FetchResponse`，包含 `Title` 和 Markdown 格式的 `Content`。请求发送给 DeepSeek，omnix 不会从本机直接访问目标网页。搜索结果保留 URL、长短摘要、正文、排序和可空元数据。

`WithBaseURL`、`WithHTTPClient` 和 `WithAPIKeyRef` 同样用于网页请求。Base URL 无论是否带 `/v1` 后缀，都只拼接一次 `/v1/tools/`。直接调用者应提供截止时间或取消上下文，共用 HTTP 客户端不设置全局超时。

## 错误与限制

本地校验会在发送请求前拒绝不合法的查询长度、条数、本地化 IP 和抓取 URL。响应超过 `MaxWebResponseBytes`（16 MiB）时返回错误，不会静默截断。应用还可通过结果中间件单独限制进入模型上下文的内容。

非 200 响应返回 `*WebAPIError`，可用 `errors.As` 获取 HTTP 状态、服务端错误类型、错误码、消息和原始 `Retry-After` 响应头。`Retryable()` 判断是否属于暂时性错误，客户端不会自动重试。取消和传输错误保留 Go 错误链，可用 `errors.Is` 检查。在 Agent 日志中，错误会转换为工具结果文本。

[集成测试](../../pkg/llm/provider_deepseek/web_test.go) 使用本地 HTTP 模拟接口，覆盖真实 SSE 工具调用、搜索到抓取的结果回传，以及抓取失败后的继续处理。测试验证调度机制，不代表真实模型的检索质量。

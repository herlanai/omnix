# DeepSeek web tools

[Documentation](README.md) · [简体中文](../zh-CN/web-tools.md)

The [DeepSeek provider](../../pkg/llm/provider_deepseek/web.go) supplies both direct API methods and tools for the existing Agent loop. Protocol references: [Search](https://api-docs.deepseek.com/api/search/) and [Fetch](https://api-docs.deepseek.com/api/fetch/).

## Enable model-directed research

Create a credential store with `DEEPSEEK_API_KEY`, then register the tools before starting the agent. In an existing application with a session ID/log and prompt assembler:

```go
provider := provider_deepseek.NewDeepSeek(store)
pipeline := tools.NewPipeline()
for _, tool := range provider.WebTools() {
    pipeline.WithTool(tool)
}
a := agent.NewAgent(id, log, prompt, pipeline, provider)
defer a.Dispose()
```

The imports are `pkg/llm/provider_deepseek`, `pkg/tools`, and `pkg/agent` under `github.com/herlanai/omnix`. `store` is a `*credentials.Store`; credentials are resolved for each request. The same provider can handle both chat and web requests, sharing its client and connection pool. A different chat adapter can also use these tools, provided it supports tool calls.

Registration advertises `deepseek_search` and `deepseek_fetch` to the model. The model chooses the arguments and whether either tool is needed. The Agent executes calls through the pipeline, records their results, and sends those results on the next model step. It can search, select a source, fetch its body, and then answer without application-side keyword routing. Tool failures also return to the model as errors. This does not force a search for every question.

Use a system prompt that asks for research when needed, source URL citations, and treatment of retrieved text as untrusted evidence. Existing approval and pre-execute middleware still applies. Configure these tools before concurrent use.

A complete runnable example is [examples/web_agent](../../examples/web_agent/main.go):

```sh
# Set DEEPSEEK_API_KEY in your environment first.
# Optionally set DEEPSEEK_MODEL.
go run ./examples/web_agent "Research Go's release policy and cite official sources"
```

This example calls the live APIs. It gives the turn three minutes, handles Ctrl+C, and uses a 60-second budget per tool call. [Official usage and billing](https://api-docs.deepseek.com/guides/search/) applies to web requests separately from model calls.

## Direct calls

`provider.Search(ctx, SearchRequest{Query: "..."})` returns `*SearchResponse`. Set `Count` to 1–50; zero omits it and uses the service default of ten. `OriginalQuestion` is an optional string pointer, `Localization` is an optional IP address, and `NeedContent` requests page bodies. Query syntax passes through unchanged. The model-facing schema rejects an explicit zero count.

`provider.Fetch(ctx, "https://example.com")` returns `*FetchResponse` with `Title` and Markdown `Content`. The request is sent to DeepSeek; omnix does not retrieve that target directly from the host. Search preserves result URLs, summaries, snippets, content, ranking, and nullable metadata.

`WithBaseURL`, `WithHTTPClient`, and `WithAPIKeyRef` also apply to web calls. Bases with or without a trailing `/v1` produce exactly one `/v1/tools/` prefix. Direct callers must supply a deadline or cancellation context; the shared HTTP client has no overall timeout.

## Failures and limits

Local validation rejects invalid query lengths, counts, localization addresses, and fetch URLs before sending a request. A response larger than `MaxWebResponseBytes` (16 MiB) is rejected instead of silently truncated. Applications can use result middleware to limit model context separately.

Non-200 responses return `*WebAPIError`, available through `errors.As`, with HTTP status, provider code/type/message, and the raw `Retry-After` header. `Retryable()` classifies transient statuses; the client does not automatically retry. Cancellation and transport failures preserve their Go error chain for `errors.Is`. In the Agent log, errors become tool-result text.

The [integration tests](../../pkg/llm/provider_deepseek/web_test.go) exercise actual SSE tool calls against local HTTP fixtures, including search-to-fetch feedback and recovery after a fetch error. They verify scheduling mechanics, not a live model's research quality.

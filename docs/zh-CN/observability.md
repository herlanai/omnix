# 用量、缓存观测与本地性能

[文档索引](README.md) · [English](../en/observability.md)

设定性能目标前，应先观测真实请求结果。Omnix 提供 usage 类型、缓存计算、回调钩子和本地测试；应用需要把这些组件连接到自己的日志、exporter 或通知系统。

## 收集模型用量

[`llm.LLMAdapter.Chat`](../../pkg/llm/llm.go) 返回 `Usage` 和错误。[DeepSeek 适配器](../../pkg/llm/provider_deepseek/deepseek.go)请求流式 usage，并读取以下 SSE 字段：

| 服务商字段 | Go 字段 | 含义 |
| --- | --- | --- |
| `prompt_tokens` | `PromptTokens` | 报告的输入 token 总数。 |
| `completion_tokens` | `CompletionTokens` | 报告的输出 token 数。 |
| `prompt_cache_hit_tokens` | `PromptCacheHitTokens` | 报告为缓存命中的输入 token 数。 |
| `prompt_cache_miss_tokens` | `PromptCacheMissTokens` | 报告为缓存未命中的输入 token 数。 |

[Agent 循环](../../pkg/agent/agent.go)目前会丢弃返回的 `Usage`。可以在把适配器传给 Agent 前进行包装，或观测直接的 `Chat` 调用。以下完整包装类型保持请求和流行为不变，在完成后调用观测函数，包括失败调用：

```go
package cacheobs

import (
	"context"

	"github.com/herlanai/omnix/pkg/llm"
)

type Adapter struct {
	Inner   llm.LLMAdapter
	Observe func(llm.Usage, error)
}

func (a Adapter) Name() string { return a.Inner.Name() }

func (a Adapter) Chat(
	ctx context.Context,
	req llm.ChatRequest,
	cb func(llm.StreamChunk),
) (llm.Usage, error) {
	usage, err := a.Inner.Chat(ctx, req, cb)
	if a.Observe != nil {
		a.Observe(usage, err)
	}
	return usage, err
}

var _ llm.LLMAdapter = Adapter{}
```

`Inner` 必须非 nil。观测函数同步执行，应保持简短；共享使用时还应保证并发安全。由于 `Usage` 没有会话 ID，可通过包装实例或请求 context 关联会话。适合记录模型、请求结果、提示词哈希和用量计数等有限元数据，不应记录凭据或完整提示词。

## 理解缓存计数

[`llm.HitRatioOf`](../../pkg/llm/cache.go)计算 `hit / (hit + miss)`，分母非正时返回零。输出 token 不参与计算。命中 token 不是额外输入，不要再把它加到 `PromptTokens` 上。

适配器独立保留四个字段，不修正总数或推算缺失的 miss 计数。整数字段没有存在性标记：hit/miss 同为零可能代表缺失缓存详情，而非确定的全未命中请求。后续 SSE usage 对象会替换前一个，不会累加。应尽可能单独分类不完整响应和缺失用量。

按 token 加权的整体命中率，应使用累计 hit 除以累计 hit 与 miss 之和。对每请求比例直接取平均，则会给短请求和长请求相同权重。`CacheStats` 没有同步保护，共享实例需要外部保护。

`CacheAlert` 比较连续比例。默认在绝对比例下降超过 `0.3` 时产生 warning，连续五次低于 `0.5` 时产生 error。每个比较序列应使用独立实例：会话 ID 参数只标注消息，不会划分内部状态。sink 在检测器持锁时同步运行，不能重入同一实例的 `Observe` 或 `Alerts`。`Alert.Webhook` 只是给 sink 的标记，不包含 HTTP 投递实现。

## 保持请求构建可重复

[系统组装器](../../pkg/sysprompt/sysprompt.go)按 order 和名称排列片段。`AssembleHash()` 仅对组装后的系统文本求哈希，不覆盖完整请求。应保持工具描述、Schema 数组、消息顺序和未变更片段一致，并记录有意的配置变化边界。

[`Pipeline.Tools()`](../../pkg/tools/pipeline.go)按工具名称字典序返回工具。直接调用适配器时，工具顺序由调用方决定。[`JsonSchemaNode.Marshal`](../../pkg/tools/schema.go)会对 `required` 排序，但 Agent 使用另一条 `json.Marshal(tool.Schema)` 路径，不能假定所有路径都规范化 Schema 数组。

[`ChangeOnlyInjector`](../../pkg/sysprompt/context_injector.go)返回变化的上下文文本，不会自行追加事件或持久化。宿主需把它连接到消息，并在重启后恢复哈希。[`ContextRegistry`](../../pkg/sysprompt/context.go)的片段应使用不同 order，因为相同 order 没有确定的名称排序规则。[`skills.Injector`](../../pkg/skills/skills.go)比较目录哈希；另一个用于发现变更的 `Hash()` 可能漏掉长度相同的内容变化。

稳定文本不保证服务商缓存命中。本地辅助组件不能控制缓存保留时间、分布、分词或计费。报告结果时应注明工作负载、样本数、模型和配置边界，而不是给出通用百分比目标。

## 指标与导出是分开的

[`internal/telemetry.CacheMetrics`](../../internal/telemetry/cache_metrics.go)是内存辅助聚合器，不是现成的 OTel instrument 集合或 Prometheus endpoint。它提供的名称是 `omnix.cache.hit_ratio`、`omnix.cache.hit_tokens`、`omnix.cache.miss_tokens` 和 `omnix.cache.broken_count`。

`Record` 添加样本和累计值，`MarkBroken` 手动增加计数。session/preset/turn 字段不会自动导出为标签。样本没有保留窗口；`HistogramP50()` 排序全部样本，偶数时取中间偏大的值，不是时间窗口指标或按 token 加权的值。

公开的 [`SessionTelemetry`](../../pkg/telemetry/session_telemetry.go)提供 event、chunk 和 tool 钩子。回调同步执行并隔离 panic，因此慢回调仍会延迟调用方。[`OTelBridge`](../../pkg/telemetry/otel.go)通过应用提供的 `Exporter` 发送 span，不会自动发布缓存指标或安装请求循环探针。

[Grafana JSON](../../deploy/grafana/omnix-cache-dashboard.json)是起始模板。第一条查询以 `_count` 计算直方图分位数，但标题描述的是 24 小时平均值；它需要真实 bucket 序列以及明确的时间和聚合定义。其他查询同样依赖 exporter 实际导出的序列。[测试](../../tests/cache_metrics_otel_integration_test.go)只检查 JSON 解析和面板数量，不验证 PromQL、数据源或已部署告警是否可用。

## 本地测试测量了什么

[SSE 探针](../../tests/cache_probe_deepseek_client_test.go)通过本地 HTTP 固定响应验证计数解析。[前缀场景](../../tests/cache_hit_rate_e2e_test.go)调用[模拟器](../../tests/testutil/deepseek_mock.go)，比较上一次提示词的字节前缀，并用字节数除以四近似 token。阈值是合成数据的测试预期，不是真实服务商结果。缩短历史场景没有调用生产压缩器。

[文本检查器](../../pkg/sysprompt/static_check.go)和[源码扫描器](../../internal/lint/cache_safety.go)只检测有限模式，不分析完整数据流或请求等价性。`ScanDir` 会跳过解析失败的文件。除这些启发式检查外，还应验证实际序列化输出。

## 运行针对性检查

在仓库根目录执行以下命令，不需要服务商 API key。HTTP 固定响应测试需要监听本地端口的权限。

```sh
go test ./pkg/sysprompt ./pkg/llm/... ./pkg/telemetry
go test ./tests -run 'Test(CacheStats|DeepSeekParseUsageFromMockSSE|N03|N06|N07|N08|N09)' -count=1
go test ./... -race
```

本地事件处理的 CPU 和分配开销可用以下有限范围的基准测量：

```sh
go test ./pkg/session -run '^$' -bench '^BenchmarkEffectiveEvents$' -benchmem -count=3
go test ./tests -run '^$' -bench '^BenchmarkIncrementalH04IncrementalPath$' -benchmem -count=3
```

这些基准测量事件过滤和增量投影计算，不测量远程缓存或 token 费用。应在相同机器、Go 版本和负载下比较，并保留原始结果。常规命令刻意排除了全量重放基线，因为反复折叠不断增长的历史可能很耗时。

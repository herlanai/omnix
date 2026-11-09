# 开发指南

[文档索引](./README.md) · [English](../en/development.md)

以下命令均在仓库根目录运行。模块路径为 `github.com/herlanai/omnix`，[go.mod](../../go.mod) 要求 Go 1.25 或更新版本。开始前安装 Go 工具链；首次获取缺失的模块依赖需要网络连接。

## 找到相关代码

| 位置 | 用途 |
|---|---|
| [pkg/](../../pkg/) | 对外 Go 包：Agent、会话、工具、模型适配器、持久化及配套服务 |
| [internal/](../../internal/) | 内部缓存安全分析与遥测辅助代码 |
| [examples/](../../examples/) | 可运行的集成示例 |
| [tests/](../../tests/) | 跨包行为测试与集成测试 |
| [tests/testutil/](../../tests/testutil/) | 共享测试工具，包括 DeepSeek 响应模拟器 |

包内单元测试放在实现旁。修改单个包时先运行对应包测试；跨包行为则通过集成测试验证。

## 构建与检查

```bash
# Format Go source and tests.
gofmt -w examples internal pkg tests

# Build every package, including examples.
go build ./...

# Run static checks.
go vet ./...

# Run tests without cached results.
go test ./... -count=1
```

代码注释统一使用英文。导出 API 的注释以声明名称开头，说明行为、资源归属、错误或并发约束。优先解释不直观的决策，避免重复代码本身。保留 `//go:build` 等编译指令。

改动应围绕具体问题。除非需求明确要求，保持对外 API 与事件格式兼容。修复缺陷时增加能够在修复前失败、验证可观察行为的断言。测试创建的资源使用临时目录与清理钩子管理。

## 验证并发与平台行为

```bash
# Run the race detector on a supported platform with a C toolchain.
go test -race ./... -count=1

# Focus on session state and event dispatch.
go test ./pkg/session ./pkg/eventbus -count=1

# Inspect detailed integration results, including skips.
go test ./tests -v -count=1
```

通过通道或其他同步机制协调并发测试，不要假定等待固定时间后后台任务一定完成。通过 `context.Context` 传递取消，停止定时器与工作协程，并在清理时等待自己启动的协程退出。

部分测试会监听本机回环地址或启动 shell 子进程。受限沙箱可能禁止这些操作；权限错误与断言失败应分别判断。普通测试使用模拟 HTTP 响应，不需要真实 DeepSeek API Key。

平台覆盖需要分别确认：Windows ACL 测试在 Windows 上运行；macOS 沙箱配置测试在其他平台跳过；没有可用交互 shell 时，终端测试可能跳过。SQLite 驱动不支持所需全文检索特性时，部分搜索测试也会跳过。查看跳过原因，不能仅凭命令成功就判断所有平台路径都已执行。

[聊天示例](../../examples/chat/main.go) 与模拟测试不同：它读取 `DEEPSEEK_API_KEY` 并调用真实 DeepSeek API。不要把凭证写入源码或测试数据。

## 测量性能

基准位于测试文件中，通过 Go benchmark runner 执行。对比版本时保持机器、Go 版本、输入与构建参数一致，同时记录 `ns/op`、`B/op` 和 `allocs/op`。启用竞态检测后的运行结果不能直接与普通性能测试比较。

```bash
# Event dispatch and filtering of visible session events.
go test ./pkg/eventbus ./pkg/session -run '^$' -bench 'Benchmark(Emit|EffectiveEvents)$' -benchmem -count=5

# Full-history versus incremental projection work.
go test ./tests -run '^$' -bench 'BenchmarkIncremental' -benchmem -count=5

# Persistence, tool pooling, registry reads, and SQLite search.
go test ./tests -run '^$' -bench 'PersistencePooledIO|Run.*Pooled|Read.*Freeze|SQLiteSearch' -benchmem -count=5
```

前缀缓存测试使用内存模拟器，命中率用于验证受控场景，不代表外部模型服务在生产环境中的实测结果。同样，局部基准变快不能直接证明完整请求更快；还需结合有代表性的工作负载判断。

可参考以下专项测试：

- [会话表面过滤](../../pkg/session/surface_performance_test.go)：替换边界、稀疏序号区间及参考实现一致性。
- [序号区间解码](../../pkg/session/seqrange_test.go)：数量限制与整数边界。
- [待办投影](../../pkg/session/todo_projection_test.go)：状态变化和结构化条目的相等性。
- [事件总线](../../pkg/eventbus/eventbus_test.go)：分发快照与并发注册。
- [SQLite 持久化](../../tests/persistence_sqlite_fts5_test.go)：读写往返、全文搜索与搜索基准。

## 交付前复核

```bash
# Inspect the complete patch and whitespace errors.
git diff --stat
git diff --check
git diff
```

说明行为如何改变、修改原因以及运行过的检查。相关时列出跳过或无法执行的平台检查，并交代基准条件。API、示例或文档命令变化时，同步更新对应的中英文指南。

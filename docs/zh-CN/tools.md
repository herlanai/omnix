# 注册和管理工具

[文档目录](README.md) · [English](../en/tools.md) · [Agent](agent.md)

工具由模型可见的名称、schema 和 Go 执行函数组成。`Pipeline` 校验参数、调用选中的单个工具，并执行前后中间件。只有显式接入约束后才会产生对应的权限控制；外部副作用仍由应用负责。

## 使用显式策略运行工具

将以下完整程序保存为 `/tmp/omnix-tools-guide.go`，在仓库根目录执行 `go run /tmp/omnix-tools-guide.go`。它先执行一次工具，再通过 pre-execute 中间件拒绝同一工具。

```go
package main

import (
	"context"
	"fmt"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/tools"
)

func main() {
	schema, err := tools.Compile([]byte(`{
        "type": "object",
        "properties": {"name": {"type": "string"}},
        "required": ["name"],
        "additionalProperties": false
    }`))
	if err != nil {
		panic(err)
	}
	policy := tools.NewRestrictionSet()
	pipeline := tools.NewPipeline().WithTool(&tools.Tool{
		Name: "greet", Description: "Return a greeting for a name.",
		Schema: schema, TimeoutMs: 1000,
		Execute: func(ctx context.Context, input map[string]any) (any, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return "Hello, " + input["name"].(string), nil
		},
	})
	pipeline.UsePre(tools.PreDecisionMiddleware(
		func(req *tools.ToolCallRequest) (tools.PreToolDecision, error) {
			if !policy.Allowed(req.Tool) {
				return tools.PreDeny, nil
			}
			return tools.PreAllow, nil
		}, nil,
	))

	invoke := func(id string) *tools.ToolCallResult {
		return pipeline.Run(context.Background(), &tools.ToolCallRequest{
			CallID: brand.NewToolCallID(id), Tool: "greet",
			Input: map[string]any{"name": "Ada"},
		}, nil)
	}
	result := invoke("call-1")
	if result.IsError {
		panic(result.Error)
	}
	fmt.Println(result.Value)

	// The pre-execute middleware consults the policy on every call.
	policy.Host(tools.RestrictionDeny("greet"))
	fmt.Printf("denied: %t\n", invoke("call-2").IsError)
}
```

预期输出：

```text
Hello, Ada
denied: true
```

`Run` 最后一个参数为 nil 时，从流水线注册表查找工具；传入非 nil 的 `*Tool` 则直接提供执行实现。上下文和请求都必须非 nil。

## 接入 Agent

按 [Agent 指南](agent.md) 将配置完成的流水线传给 `agent.NewAgent`。Agent 使用 `Pipeline.Tools()` 提供按名称排序的模型 schema，并从注册表查找执行函数。`SetToolProvider` 只覆盖执行查找，不会公布额外工具的 schema。

`WithTool` 修改当前注册表并返回同一流水线；同名注册会替换原工具。在并发使用前完成工具和中间件配置，因为注册表及中间件链不支持并发修改。执行函数还需要自行保护共享状态。

## 定义输入范围

[`tools.Compile`](../../pkg/tools/schema.go) 解析已经实现的 JSON Schema 子集，并拒绝不支持的关键字。支持对象属性与必填字段、基础类型、数组、enum/const、数值范围、字符串长度与 pattern，以及 `oneOf`。这不是完整的 JSON Schema 实现：当前 `oneOf` 接受任意一个分支匹配，不要求恰好一个分支匹配。

`default` 和 `examples` 只描述输入，不会在校验时自动填充值。需要拒绝额外字段时设置 `additionalProperties: false`。schema 校验发生在 pre-execute 之后的 dispatch terminal；自定义执行路径若绕过流水线，需要自行校验。

## 中间件执行顺序

| 注册方法 | 用途和实际流程 |
|---|---|
| `UsePre` | 检查或改写输入、查询策略、拒绝调用。返回错误或设置 `Denied=true` 会直接结束，不进入执行、post 和 result 阶段。 |
| `UseExecute` | 包裹 dispatch；terminal 检查取消、解析工具、校验 schema，再调用执行函数。 |
| `UsePost` | 在 dispatch 后检查或修改结果，包括执行错误；可以将结果标记为拒绝。 |
| `UseResult` | 对到达该阶段的路径做最终处理，不是保证执行的 finally 钩子。 |

处理函数接收 `*tools.ExecContext` 和 `waterfall.NextFunc`。调用 `next()` 推进当前阶段，每个处理函数至多调用一次。不调用 `next()` 只会短路当前阶段，不一定结束整个流水线。pre-handler 要拒绝调用，应设置 `ec.Denied` 或返回错误，不能仅返回 nil。

工具 `Execute` 的 panic 会转换为错误结果；这些流水线阶段不会自动捕获中间件 panic。无论何处提前退出都必须执行的清理，应使用普通 `defer`。

## 限制和审批

[`RestrictionSet`](../../pkg/tools/restriction.go) 是策略辅助组件，不会自动执行约束。示例在 `PreDecisionMiddleware` 中显式查询它。未提及的工具默认允许；由最近一个提及工具的层决定，较近层的 allow 可以覆盖外层 deny。它不是只取 deny 的交集，也不是不可覆盖的宿主安全边界。

使用 `Filter` 选择注册并提供给模型的工具，同时在调用时再次检查权限。只过滤 schema 无法阻止自定义调用或过期调用。更新已有策略层会原位修改 mask；新增层才会追加到最近的位置。

[`PreDecisionMiddleware`](../../pkg/tools/predecision.go) 接受 allow、deny 或 ask 决策。交互审批需要提供 `AskFunc`；没有处理函数、拒绝或发生错误都会拒绝 ask。批准只对当前调用有效。宿主负责用户交互、身份检查、审计持久化和决策回调中的取消处理。

## 取消、错误和外部副作用

`TimeoutMs` 为 `Execute` 派生超时上下文，但流水线仍同步调用执行函数，无法打断忽略 `ctx` 的代码。网络请求应传入上下文，合适时等待 `ctx.Done()`，并显式处理清理。

工具自身的 deadline 会映射为 `TOOL_TIMEOUT`；父上下文取消是不同的上下文错误。直接调用时读取 `ToolCallResult.IsError`、`Error` 和可选 `ErrorCode`。Agent 会将错误文本和 `IsError` 存入 `tool/result`，但不会保留结果中的 `ErrorCode` 字段。

`WrapTimeout` 是独立助手，会在 goroutine 中执行工具；超时返回不会杀死该 goroutine，也不会撤销副作用。不要假设非幂等工具可以安全地自动重试。

Go 回调不会因为进入流水线就获得操作系统隔离。文件系统边界、沙箱 provider、子进程执行、凭证访问和审批服务都需要宿主显式接入。`ToolRunContext` 和结束 turn 的回调也是独立助手；当前 Agent 构造函数不会自动装配所有工具运行时组件。

## 验证集成

普通和对象池执行共用约束逻辑。`SetPooled(true)` 复用内部执行状态；调用结束后不要继续持有 `ExecContext` 或其 `Meta` map。使用期间保持配置不变，并用自己的工作负载验证性能收益。

参考[分发及约束测试](../../pkg/tools/dispatch_test.go)、[对象池测试](../../pkg/tools/pool_test.go)和[限制策略测试](../../tests/tools_restriction_intersect_test.go)。

```sh
go test ./pkg/tools
go test ./tests -run 'TestToolRestriction|TestPreToolDecision|TestPreDecisionFromApproval'
```

现成的搜索和抓取工具见 [DeepSeek 网页工具](web-tools.md)。

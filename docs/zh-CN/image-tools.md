# 方舟图片生成

[文档导航](README.md) · [English](../en/image-tools.md)

[`provider_ark`](../../pkg/imagegen/provider_ark/ark.go) 接入非流式图片接口 `https://ark.cn-beijing.volces.com/api/v3/images/generations`。默认模型按本次接入配置为 `doubao-seedream-5-5-261028`，模型可用性和支持参数以方舟账号实际权限为准。

## 配置 Key 并注册工具

图片生成使用独立的 `ARK_API_KEY` 凭证。SDK 每次请求从 `credentials.Store` 读取，不会自动读取环境变量。在已有应用准备好 `ctx`、`store` 和工具 `pipeline` 后：

```go
// Copy ARK_API_KEY from the host environment into the credential store.
if err := store.Set(ctx, brand.NewCredentialRef("ARK_API_KEY"), os.Getenv("ARK_API_KEY")); err != nil {
    return err
}
images := provider_ark.New(store)
pipeline.WithTool(images.Tool())
```

导入 `github.com/herlanai/omnix/pkg/imagegen/provider_ark`、`github.com/herlanai/omnix/pkg/brand` 和 `os`。将该流水线传给 `agent.NewAgent`，其中也可以同时注册 [DeepSeek 网页工具](web-tools.md)。聊天适配器可以使用不同服务商和 Key。

模型可以看到 `ark_generate_image`，自主选择调用时机和图片参数。Agent 通过已有中间件执行工具、记录结果，再把图片 URL 或错误反馈给模型。建议要求模型以 Markdown 图片形式展示成功结果。工具回传元数据和 URL，不会将图片像素传给模型检查；展示或保存由宿主应用负责。

## 参数

| 字段 | 默认值 | 行为 |
| --- | --- | --- |
| `prompt` | 必填 | 图片描述，包括精确文字、风格等。空白提示词在本地报错。 |
| `size` | `2K` | 服务支持的分辨率或宽高，模型特有约束由服务端校验。 |
| `output_format` | `png` | 支持 `png`、`jpeg`。 |
| `background` | `opaque` | 支持 `opaque`、`transparent`，透明背景必须使用 PNG。 |
| `watermark` | `false` | 是否添加水印。 |

`response_format` 固定为 `url`，`stream` 固定为 `false`。Key、接口地址和模型由应用配置，不会作为工具参数交给模型。图片模型与 Agent 的聊天模型相互独立。可通过 `WithModel`、`WithEndpoint`（完整接口 URL）、`WithAPIKeyRef`、`WithHTTPClient` 覆盖配置。

直接调用方式为 `images.Generate(ctx, provider_ark.Request{Prompt: "Embroidered stickers", Background: "transparent"})`，与 Agent 工具使用相同请求格式。不同模型对透明背景的支持可能不同，服务端拒绝会返回给调用者。接口说明见[官方图片生成文档](https://docs.volcengine.com/docs/ark/image-generation-api?lang=en)。

## 运行 Agent 示例

在环境中设置聊天用的 `DEEPSEEK_API_KEY` 和生图用的 `ARK_API_KEY`。可选设置 `DEEPSEEK_MODEL` 和 `ARK_IMAGE_MODEL`。

```sh
go run ./examples/image_agent "Generate an embroidered sticker with a transparent background"
```

[完整示例](../../examples/image_agent/main.go) 调用真实 API，支持 Ctrl+C，单轮允许五分钟。工具调用超时为三分钟，默认 HTTP 客户端也设置三分钟超时；自定义客户端会替换客户端级别的超时配置。生成可能产生服务商费用，客户端不会自动重试。

## 结果与错误

`Response` 包含 `Model`、`Created`、`Data`（URL、尺寸、可选的单图错误）和原始 JSON `Usage`。部分成功时保留单图错误；全部失败或没有图片时返回错误。非 200 响应及服务端错误对象返回 `*APIError`，可通过 `errors.As` 检查。取消操作保留错误链，支持 `errors.Is`。

响应限制为 4 MiB，生成结果必须提供有效 HTTP(S) 图片 URL，客户端不会下载图片。工具 Schema 中不包含 Key，服务端错误码和消息中反射出的 Key 会被脱敏。[测试](../../pkg/imagegen/provider_ark/ark_test.go) 使用本地模拟接口覆盖请求字段、凭证轮换、失败、取消和 Agent 结果回传，不调用付费服务。

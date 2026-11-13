# Ark image generation

[Documentation](README.md) · [简体中文](../zh-CN/image-tools.md)

[`provider_ark`](../../pkg/imagegen/provider_ark/ark.go) implements the non-streaming image endpoint at `https://ark.cn-beijing.volces.com/api/v3/images/generations`. The default model is `doubao-seedream-5-5-261028`, matching the configured integration. Model availability and supported options depend on your Ark account.

## Configure the key and register the tool

Use an independent `ARK_API_KEY` credential. The SDK reads it from `credentials.Store` on every request; it does not read environment variables automatically. In an existing application with `ctx`, `store`, and a tool `pipeline`:

```go
// Copy ARK_API_KEY from the host environment into the credential store.
if err := store.Set(ctx, brand.NewCredentialRef("ARK_API_KEY"), os.Getenv("ARK_API_KEY")); err != nil {
    return err
}
images := provider_ark.New(store)
pipeline.WithTool(images.Tool())
```

Import `github.com/herlanai/omnix/pkg/imagegen/provider_ark`, `github.com/herlanai/omnix/pkg/brand`, and `os`. Pass that pipeline to `agent.NewAgent`. It may also contain the [DeepSeek web tools](web-tools.md). The chat adapter can use a different provider and key.

The model sees `ark_generate_image`, chooses when to call it and supplies image parameters. The Agent executes it through existing middleware, records the result, and feeds image URLs or errors back to the model. Ask the model to show successful URLs as Markdown images. The tool returns metadata and URLs, not image pixels for model inspection; the host decides how to display or save them.

## Options

| Field | Default | Behavior |
| --- | --- | --- |
| `prompt` | Required | Image description, including exact text and style. Blank prompts fail locally. |
| `size` | `2K` | Provider-supported resolution or dimensions; model-specific limits are checked by the service. |
| `output_format` | `png` | `png` or `jpeg`. |
| `background` | `opaque` | `opaque` or `transparent`; transparency requires PNG. |
| `watermark` | `false` | Whether to request a watermark. |

`response_format` is fixed to `url` and `stream` to `false`. API keys, endpoint, and model are host configuration and are never exposed as tool arguments. The image model is independent of the Agent's chat model. Use `WithModel`, `WithEndpoint` (complete endpoint URL), `WithAPIKeyRef`, or `WithHTTPClient` to override host configuration.

Direct calls use `images.Generate(ctx, provider_ark.Request{Prompt: "Embroidered stickers", Background: "transparent"})`. Both this call and the Agent tool send the same request shape. Transparency support varies by model; provider rejections are returned to the caller. See the [official image API reference](https://docs.volcengine.com/docs/ark/image-generation-api?lang=en).

## Run the Agent example

Set `DEEPSEEK_API_KEY` for chat and `ARK_API_KEY` for image generation in your environment. Optionally set `DEEPSEEK_MODEL` and `ARK_IMAGE_MODEL`.

```sh
go run ./examples/image_agent "Generate an embroidered sticker with a transparent background"
```

[The example](../../examples/image_agent/main.go) makes real API calls, handles Ctrl+C, and allows five minutes per turn. The tool has a three-minute timeout. The default HTTP client also has a three-minute timeout; a custom client replaces that client-level budget. Generation can incur provider charges and is not automatically retried.

## Results and failures

`Response` contains `Model`, `Created`, `Data` (URL, size, optional per-image error), and raw JSON `Usage`. Partial results retain per-image errors; all-failed or empty results return errors. Non-200 responses and provider error objects return `*APIError`, inspectable with `errors.As`. Cancellation preserves the error chain for `errors.Is`.

Responses are bounded to 4 MiB and require usable HTTP(S) image URLs. The client does not download images. No API keys are included in tool schemas, and reflected keys in provider error codes/messages are redacted. The [tests](../../pkg/imagegen/provider_ark/ark_test.go) cover request fields, key rotation, failures, cancellation, and Agent result feedback using local fixtures; they do not call the paid service.

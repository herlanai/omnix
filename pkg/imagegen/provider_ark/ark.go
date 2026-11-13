// Package provider_ark provides non-streaming Seedream image generation and an
// Agent-compatible tool. API credentials are supplied by the host application.
package provider_ark

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/credentials"
	"github.com/herlanai/omnix/pkg/tools"
)

const (
	// DefaultEndpoint is the Beijing Ark image generation endpoint.
	DefaultEndpoint = "https://ark.cn-beijing.volces.com/api/v3/images/generations"
	// DefaultModel is the Seedream model used unless the host overrides it.
	DefaultModel = "doubao-seedream-5-5-261028"
	// ToolName is the model-visible image generation tool name.
	ToolName = "ark_generate_image"
	// MaxResponseBytes limits buffered JSON responses. Images are returned as URLs.
	MaxResponseBytes = 4 << 20
)

// Client shares an HTTP client across generation calls. Configure it before use.
type Client struct {
	creds      *credentials.Store
	keyRef     brand.CredentialRef
	endpoint   string
	model      string
	httpClient *http.Client
}

// Option configures the client.
type Option func(*Client)

// WithEndpoint overrides the complete image generation URL.
func WithEndpoint(endpoint string) Option { return func(c *Client) { c.endpoint = endpoint } }

// WithModel selects a model or endpoint ID for all calls.
func WithModel(model string) Option { return func(c *Client) { c.model = model } }

// WithAPIKeyRef selects the credential reference instead of ARK_API_KEY.
func WithAPIKeyRef(ref brand.CredentialRef) Option { return func(c *Client) { c.keyRef = ref } }

// WithHTTPClient sets a caller-managed HTTP client. It must not be nil.
func WithHTTPClient(client *http.Client) Option { return func(c *Client) { c.httpClient = client } }

// New creates a client with a three-minute request timeout. The host must copy
// its API key into the credential store; this constructor does not read os.Environ.
func New(creds *credentials.Store, opts ...Option) *Client {
	c := &Client{creds: creds, keyRef: brand.NewCredentialRef("ARK_API_KEY"), endpoint: DefaultEndpoint, model: DefaultModel, httpClient: &http.Client{Timeout: 3 * time.Minute}}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Request controls generation. Defaults are 2K, png, opaque, and no watermark.
// Transport always uses response_format=url and stream=false to keep binary
// image data out of the model context. Model selection belongs to the host.
type Request struct {
	Prompt       string `json:"prompt"`
	Size         string `json:"size,omitempty"`
	OutputFormat string `json:"output_format,omitempty"`
	Background   string `json:"background,omitempty"`
	Watermark    bool   `json:"watermark"`
}

// Image holds a generated image URL, dimensions, or a per-image error.
type Image struct {
	URL   string       `json:"url,omitempty"`
	Size  string       `json:"size,omitempty"`
	Error *ErrorDetail `json:"error,omitempty"`
}

// ErrorDetail contains a provider error code and message.
type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Response preserves generated image metadata and optional provider usage.
type Response struct {
	Model   string          `json:"model,omitempty"`
	Created int64           `json:"created,omitempty"`
	Data    []Image         `json:"data"`
	Usage   json.RawMessage `json:"usage,omitempty"`
}

// APIError reports HTTP errors and generation failures, including HTTP 200
// responses containing no usable image. Requests are never retried automatically.
type APIError struct {
	StatusCode int
	Code       string
	Message    string
	RequestID  string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("ark image generation: HTTP %d (%s): %s", e.StatusCode, e.Code, e.Message)
}

// Generate sends one non-streaming request. Cancellation covers the entire HTTP
// operation. Returned image URLs are not downloaded or written to disk.
func (c *Client) Generate(ctx context.Context, input Request) (*Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.Prompt) == "" {
		return nil, fmt.Errorf("ark image generation: prompt must not be blank")
	}
	if input.Size == "" {
		input.Size = "2K"
	}
	if input.OutputFormat == "" {
		input.OutputFormat = "png"
	}
	if input.Background == "" {
		input.Background = "opaque"
	}
	if input.OutputFormat != "png" && input.OutputFormat != "jpeg" {
		return nil, fmt.Errorf("ark image generation: output_format must be png or jpeg")
	}
	if input.Background != "opaque" && input.Background != "transparent" {
		return nil, fmt.Errorf("ark image generation: background must be opaque or transparent")
	}
	if input.Background == "transparent" && input.OutputFormat != "png" {
		return nil, fmt.Errorf("ark image generation: transparent background requires png")
	}
	if strings.TrimSpace(c.model) == "" || c.httpClient == nil {
		return nil, fmt.Errorf("ark image generation: model and HTTP client are required")
	}
	if c.creds == nil {
		return nil, fmt.Errorf("ark image generation: missing API key credential")
	}
	key, ok := c.creds.Resolve(ctx, c.keyRef)
	if !ok || strings.TrimSpace(key) == "" {
		return nil, fmt.Errorf("ark image generation: missing API key credential")
	}
	payload := struct {
		Request
		Model          string `json:"model"`
		ResponseFormat string `json:"response_format"`
		Stream         bool   `json:"stream"`
	}{Request: input, Model: c.model, ResponseFormat: "url", Stream: false}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("ark image generation: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ark image generation: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ark image generation: request: %w", err)
	}
	defer resp.Body.Close()
	limit := int64(MaxResponseBytes)
	if resp.StatusCode != http.StatusOK {
		limit = 64 << 10
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("ark image generation: read response: %w", err)
	}
	var envelope struct {
		Response
		Error *ErrorDetail `json:"error"`
	}
	decodeErr := json.Unmarshal(data, &envelope)
	failure := func(detail *ErrorDetail) *APIError {
		e := &APIError{StatusCode: resp.StatusCode, Message: http.StatusText(resp.StatusCode), RequestID: resp.Header.Get("X-Request-Id")}
		if detail != nil {
			e.Code = detail.Code
			e.Message = detail.Message
		}
		e.Code = strings.ReplaceAll(e.Code, key, "[REDACTED]")
		e.Message = strings.ReplaceAll(e.Message, key, "[REDACTED]")
		return e
	}
	if resp.StatusCode != http.StatusOK {
		return nil, failure(envelope.Error)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("ark image generation: response exceeds %d bytes", limit)
	}
	if decodeErr != nil {
		return nil, fmt.Errorf("ark image generation: decode response: %w", decodeErr)
	}
	if envelope.Error != nil {
		return nil, failure(envelope.Error)
	}
	valid := 0
	for i := range envelope.Data {
		image := &envelope.Data[i]
		if image.Error != nil {
			image.Error.Code = strings.ReplaceAll(image.Error.Code, key, "[REDACTED]")
			image.Error.Message = strings.ReplaceAll(image.Error.Message, key, "[REDACTED]")
			continue
		}
		u, err := url.Parse(image.URL)
		if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
			return nil, fmt.Errorf("ark image generation: invalid image URL in response")
		}
		valid++
	}
	if valid == 0 {
		if len(envelope.Data) > 0 && envelope.Data[0].Error != nil {
			return nil, failure(envelope.Data[0].Error)
		}
		return nil, fmt.Errorf("ark image generation: response contains no images")
	}
	return &envelope.Response, nil
}

// Tool returns a fresh definition for registration before an Agent starts.
// The model chooses generation arguments; credentials and endpoint remain host-owned.
func (c *Client) Tool() *tools.Tool {
	schema, err := tools.Compile([]byte(`{"type":"object","properties":{"prompt":{"type":"string","minLength":1,"description":"Detailed description of the requested image, including style, composition and any exact text."},"size":{"type":"string","minLength":1,"default":"2K","description":"Provider-supported resolution, such as 2K, or widthxheight."},"output_format":{"type":"string","enum":["png","jpeg"],"default":"png"},"background":{"type":"string","enum":["opaque","transparent"],"default":"opaque","description":"Use transparent only when requested; requires png."},"watermark":{"type":"boolean","default":false}},"required":["prompt"],"additionalProperties":false}`))
	if err != nil {
		panic("invalid built-in Ark image schema: " + err.Error())
	}
	return &tools.Tool{Name: ToolName, Description: "Generate an image with Seedream when the user requests image creation. Describe the requested image in prompt. Returns image URLs to show to the user. Do not claim an image was generated if the tool fails.", Schema: schema, TimeoutMs: 180000, Execute: func(ctx context.Context, input map[string]any) (any, error) {
		data, err := json.Marshal(input)
		if err != nil {
			return nil, fmt.Errorf("ark image generation: encode arguments: %w", err)
		}
		var request Request
		if err := json.Unmarshal(data, &request); err != nil {
			return nil, fmt.Errorf("ark image generation: invalid arguments: %w", err)
		}
		return c.Generate(ctx, request)
	}}
}

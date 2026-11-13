package provider_ark_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/herlanai/omnix/pkg/agent"
	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/credentials"
	ark "github.com/herlanai/omnix/pkg/imagegen/provider_ark"
	"github.com/herlanai/omnix/pkg/llm"
	"github.com/herlanai/omnix/pkg/session"
	"github.com/herlanai/omnix/pkg/tools"
)

func client(t *testing.T, h http.HandlerFunc) (*ark.Client, *credentials.Store) {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	store := credentials.NewMemoryStore()
	if err := store.Set(context.Background(), brand.NewCredentialRef("IMAGE_KEY"), "secret-key"); err != nil {
		t.Fatal(err)
	}
	return ark.New(store, ark.WithEndpoint(s.URL+"/api/v3/images/generations"), ark.WithAPIKeyRef(brand.NewCredentialRef("IMAGE_KEY")), ark.WithHTTPClient(s.Client())), store
}

func TestGenerationPayloadAndCredentialRotation(t *testing.T) {
	expectedKey := "secret-key"
	c, store := client(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/v3/images/generations" || r.Header.Get("Authorization") != "Bearer "+expectedKey || r.Header.Get("Content-Type") != "application/json" {
			t.Error("incorrect request")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		want := map[string]any{"model": ark.DefaultModel, "prompt": "刺绣贴纸", "size": "2K", "response_format": "url", "output_format": "png", "background": "transparent", "stream": false, "watermark": false}
		if len(body) != len(want) {
			t.Errorf("body=%v", body)
		}
		for k, v := range want {
			if body[k] != v {
				t.Errorf("%s=%v want %v", k, body[k], v)
			}
		}
		fmt.Fprint(w, `{"model":"seedream","created":42,"data":[{"url":"https://example.com/image.png","size":"2048x2048"}],"usage":{"generated_images":1}}`)
	})
	for i := 0; i < 2; i++ {
		got, err := c.Generate(context.Background(), ark.Request{Prompt: "刺绣贴纸", Background: "transparent"})
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Data) != 1 || got.Data[0].Size != "2048x2048" || got.Created != 42 || len(got.Usage) == 0 {
			t.Fatalf("response=%+v", got)
		}
		expectedKey = "rotated-key"
		if err := store.Set(context.Background(), brand.NewCredentialRef("IMAGE_KEY"), expectedKey); err != nil {
			t.Fatal(err)
		}
	}
}

func TestValidation(t *testing.T) {
	c, _ := client(t, func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected network request") })
	for _, req := range []ark.Request{{}, {Prompt: " "}, {Prompt: "x", OutputFormat: "gif"}, {Prompt: "x", Background: "other"}, {Prompt: "x", Background: "transparent", OutputFormat: "jpeg"}} {
		if _, err := c.Generate(context.Background(), req); err == nil {
			t.Errorf("accepted %+v", req)
		}
	}
	if _, err := ark.New(nil).Generate(context.Background(), ark.Request{Prompt: "x"}); err == nil {
		t.Fatal("missing key accepted")
	}
	p := tools.NewPipeline().WithTool(c.Tool())
	for _, args := range []map[string]any{{"prompt": "x", "api_key": "bad"}, {"prompt": "x", "watermark": "false"}, {"prompt": "x", "stream": true}} {
		if result := p.Run(context.Background(), &tools.ToolCallRequest{Tool: ark.ToolName, Input: args}, nil); !result.IsError {
			t.Errorf("accepted %v", args)
		}
	}
}

func TestFailures(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		code   string
	}{
		{401, `{"error":{"code":"Unauthorized","message":"secret-key is invalid"}}`, "Unauthorized"},
		{429, `{"error":{"code":"RateLimit","message":"wait"}}`, "RateLimit"},
		{200, `{"error":{"code":"Blocked","message":"blocked"}}`, "Blocked"},
		{200, `{"data":[{"error":{"code":"Failed","message":"secret-key failed"}}]}`, "Failed"},
		{502, "upstream unavailable", ""}, {200, "invalid JSON", ""}, {200, "null", ""}, {200, `{"data":[]}`, ""}, {200, `{"data":[{"url":"file:///etc/passwd"}]}`, ""}, {200, strings.Repeat("x", ark.MaxResponseBytes+1), ""},
	} {
		c, _ := client(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); fmt.Fprint(w, tc.body) })
		_, err := c.Generate(context.Background(), ark.Request{Prompt: "x"})
		if err == nil || strings.Contains(err.Error(), "secret-key") {
			t.Fatalf("error=%v", err)
		}
		if tc.code != "" {
			var apiErr *ark.APIError
			if !errors.As(err, &apiErr) || apiErr.Code != tc.code || apiErr.StatusCode != tc.status {
				t.Fatalf("error=%v", err)
			}
		}
	}
}

func TestCancellation(t *testing.T) {
	started := make(chan struct{})
	c, _ := client(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := c.Generate(ctx, ark.Request{Prompt: "x"}); done <- err }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error=%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("request did not stop")
	}
}

type imageModel struct {
	t     *testing.T
	calls int
	fails bool
}

func (m *imageModel) Name() string { return "test" }
func (m *imageModel) Chat(ctx context.Context, req llm.ChatRequest, emit func(llm.StreamChunk)) (llm.Usage, error) {
	m.calls++
	if len(req.Tools) != 1 || req.Tools[0].Name != ark.ToolName || req.Tools[0].Parameters["properties"] == nil {
		m.t.Error("image schema not advertised")
	}
	if m.calls == 1 {
		emit(llm.StreamChunk{Kind: llm.ChunkToolCall, ToolCall: &llm.ToolCall{ID: "image-1", Name: ark.ToolName, Input: map[string]any{"prompt": "A transparent sticker", "background": "transparent"}}})
	} else {
		found := false
		for _, msg := range req.Messages {
			for _, block := range msg.Content {
				if result := block.ToolResult; result != nil && result.ToolUseID == "image-1" {
					expected := "https://example.com/image.png"
					if m.fails {
						expected = "generation failed"
					}
					if strings.Contains(result.Content, expected) {
						found = true
					}
				}
			}
		}
		if !found {
			m.t.Error("missing image result feedback")
		}
		emit(llm.StreamChunk{Kind: llm.ChunkText, Text: "Image result received"})
	}
	emit(llm.StreamChunk{Kind: llm.ChunkDone})
	return llm.Usage{}, nil
}

func TestAgentImageScheduling(t *testing.T) {
	for _, fails := range []bool{false, true} {
		t.Run(fmt.Sprint(fails), func(t *testing.T) {
			c, _ := client(t, func(w http.ResponseWriter, r *http.Request) {
				if fails {
					w.WriteHeader(500)
					fmt.Fprint(w, `{"error":{"message":"generation failed"}}`)
				} else {
					fmt.Fprint(w, `{"data":[{"url":"https://example.com/image.png"}]}`)
				}
			})
			model := &imageModel{t: t, fails: fails}
			id := brand.NewSessionID("image-test")
			log := session.NewSessionLog(id)
			a := agent.NewAgent(id, log, nil, tools.NewPipeline().WithTool(c.Tool()), model)
			defer a.Dispose()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			a.SetRunContext(ctx, 5*time.Second)
			if err := a.Run("Generate a sticker"); err != nil {
				t.Fatal(err)
			}
			if err := a.WhenIdle(ctx); err != nil {
				t.Fatal(err)
			}
			if model.calls != 2 {
				t.Fatalf("model calls=%d", model.calls)
			}
			found := false
			for _, event := range log.Events() {
				if data, ok := event.Data.(session.ToolResultData); ok {
					found = true
					if data.IsError != fails {
						t.Errorf("tool result=%+v", data)
					}
				}
			}
			if !found {
				t.Fatal("missing tool result")
			}
		})
	}
}

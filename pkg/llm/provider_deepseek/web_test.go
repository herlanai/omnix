package provider_deepseek_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/herlanai/omnix/pkg/agent"
	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/credentials"
	ds "github.com/herlanai/omnix/pkg/llm/provider_deepseek"
	"github.com/herlanai/omnix/pkg/session"
	"github.com/herlanai/omnix/pkg/tools"
)

func webClient(t *testing.T, handler http.HandlerFunc, suffix string) *ds.DeepSeek {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	store := credentials.NewMemoryStore()
	ref := brand.NewCredentialRef("WEB_KEY")
	if err := store.Set(context.Background(), ref, "test-key"); err != nil {
		t.Fatal(err)
	}
	return ds.NewDeepSeek(store, ds.WithBaseURL(server.URL+suffix), ds.WithAPIKeyRef(ref), ds.WithHTTPClient(server.Client()))
}

func TestWebRequests(t *testing.T) {
	for _, suffix := range []string{"", "/", "/v1", "/v1/"} {
		t.Run("base"+suffix, func(t *testing.T) {
			d := webClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("Content-Type") != "application/json" {
					t.Error("invalid method or headers")
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				switch r.URL.Path {
				case "/v1/tools/search":
					if body["query"] != strings.Repeat("中", 128) || body["count"] != float64(3) || body["need_content"] != true || body["ori_question"] != "question" || body["localization"] != "::1" {
						t.Errorf("body=%v", body)
					}
					fmt.Fprint(w, `{"results":[{"rerank_score":2.4,"title":"Page","url":"https://example.com","snippet":"short","summary":"long","content":"body","site_name":null,"site_icon":null,"date":null,"authority":3}]}`)
				case "/v1/tools/fetch":
					if body["url"] != "https://example.com" {
						t.Errorf("body=%v", body)
					}
					fmt.Fprint(w, `{"title":"Page","content":"# Body"}`)
				default:
					t.Errorf("path=%s", r.URL.Path)
					http.NotFound(w, r)
				}
			}, suffix)
			q := "question"
			got, err := d.Search(context.Background(), ds.SearchRequest{Query: strings.Repeat("中", 128), Count: 3, OriginalQuestion: &q, Localization: "::1", NeedContent: true})
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Results) != 1 || got.Results[0].Summary != "long" || got.Results[0].SiteName != nil || got.Results[0].Authority != 3 {
				t.Fatalf("result=%+v", got)
			}
			page, err := d.Fetch(context.Background(), "https://example.com")
			if err != nil || page.Content != "# Body" {
				t.Fatalf("page=%+v err=%v", page, err)
			}
		})
	}
}

func TestWebValidation(t *testing.T) {
	d := webClient(t, func(w http.ResponseWriter, r *http.Request) { t.Error("invalid request reached server") }, "")
	empty := ""
	for _, req := range []ds.SearchRequest{{}, {Query: " \t"}, {Query: strings.Repeat("中", 129)}, {Query: "x", Count: -1}, {Query: "x", Count: 51}, {Query: "x", OriginalQuestion: &empty}, {Query: "x", Localization: "not-an-ip"}} {
		if _, err := d.Search(context.Background(), req); err == nil {
			t.Errorf("accepted %+v", req)
		}
	}
	for _, target := range []string{"", "file:///tmp/file", "https:///path", "https://user:pass@example.com", "https://example.com/" + strings.Repeat("x", 8192)} {
		if _, err := d.Fetch(context.Background(), target); err == nil {
			t.Errorf("accepted %q", target)
		}
	}
	p := tools.NewPipeline()
	for _, tool := range d.WebTools() {
		p.WithTool(tool)
	}
	for _, input := range []map[string]any{{"query": "x", "count": 0}, {"query": "x", "count": 1.5}, {"query": "x", "extra": true}, {"query": "x", "need_content": "yes"}} {
		res := p.Run(context.Background(), &tools.ToolCallRequest{Tool: ds.SearchToolName, Input: input}, nil)
		if !res.IsError {
			t.Errorf("accepted %v", input)
		}
	}
	if _, err := ds.NewDeepSeek(nil).Search(context.Background(), ds.SearchRequest{Query: "x"}); err == nil {
		t.Fatal("accepted missing credentials")
	}
}

func TestWebErrorsAndLimits(t *testing.T) {
	for _, status := range []int{400, 401, 402, 403, 404, 408, 413, 429, 500, 504} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			d := webClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", "2")
				w.WriteHeader(status)
				fmt.Fprint(w, `{"error":{"message":"failure test-key","type":"request_error","code":"denied"}}`)
			}, "")
			_, err := d.Fetch(context.Background(), "https://example.com")
			var apiErr *ds.WebAPIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != status || apiErr.Code != "denied" || apiErr.RetryAfter != "2" || strings.Contains(err.Error(), "test-key") {
				t.Fatalf("error=%v", err)
			}
			if apiErr.Retryable() != (status == 408 || status == 429 || status >= 500) {
				t.Fatal("wrong retry classification")
			}
		})
	}
	for _, body := range []string{"not json", "null", `{}`, `{"results":null}`, `{"results":[]} trailing`, strings.Repeat("x", ds.MaxWebResponseBytes+1)} {
		d := webClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }, "")
		if _, err := d.Search(context.Background(), ds.SearchRequest{Query: "x"}); err == nil {
			t.Fatal("accepted malformed or oversized response")
		}
	}
}

func TestWebCancellation(t *testing.T) {
	started := make(chan struct{})
	d := webClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	}, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := d.Fetch(ctx, "https://example.com"); done <- err }()
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
		t.Fatal("body read did not cancel")
	}
}

func TestAgentSchedulesWebTools(t *testing.T) {
	// Exercise the real SSE adapter, tool schemas, HTTP APIs, and result feedback.
	for _, fetchFails := range []bool{false, true} {
		t.Run(fmt.Sprint(fetchFails), func(t *testing.T) {
			var chats, searches, fetches atomic.Int32
			d := webClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/tools/search":
					searches.Add(1)
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if _, exists := body["count"]; exists {
						t.Error("default count should be omitted")
					}
					if _, exists := body["ori_question"]; exists {
						t.Error("null ori_question should be omitted")
					}
					fmt.Fprint(w, `{"results":[{"title":"Example","url":"https://example.com/article","summary":"Search summary"}]}`)
				case "/v1/tools/fetch":
					fetches.Add(1)
					if fetchFails {
						w.WriteHeader(404)
						fmt.Fprint(w, `{"error":{"message":"page unavailable"}}`)
					} else {
						fmt.Fprint(w, `{"title":"Example","content":"# Verified article"}`)
					}
				case "/chat/completions":
					var req struct {
						Tools []struct {
							Function struct {
								Name       string         `json:"name"`
								Parameters map[string]any `json:"parameters"`
							} `json:"function"`
						} `json:"tools"`
						Messages []struct {
							Role       string `json:"role"`
							Content    string `json:"content"`
							ToolCallID string `json:"tool_call_id"`
						} `json:"messages"`
					}
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Error(err)
					}
					if len(req.Tools) != 2 || req.Tools[0].Function.Name != ds.FetchToolName || req.Tools[1].Function.Name != ds.SearchToolName || req.Tools[1].Function.Parameters["properties"] == nil {
						t.Errorf("tools=%+v", req.Tools)
					}
					w.Header().Set("Content-Type", "text/event-stream")
					n := chats.Add(1)
					var delta any
					switch n {
					case 1:
						delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "search-1", "function": map[string]any{"name": ds.SearchToolName, "arguments": `{"query":"example","ori_question":null}`}}}}
					case 2:
						last := req.Messages[len(req.Messages)-1]
						if last.Role != "tool" || last.ToolCallID != "search-1" || !strings.Contains(last.Content, "https://example.com/article") {
							t.Errorf("search feedback=%+v", last)
						}
						delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "fetch-1", "function": map[string]any{"name": ds.FetchToolName, "arguments": `{"url":"https://example.com/article"}`}}}}
					default:
						if n != 3 {
							t.Errorf("unexpected step %d", n)
						}
						last := req.Messages[len(req.Messages)-1]
						expected := "Verified article"
						if fetchFails {
							expected = "page unavailable"
						}
						if last.Role != "tool" || last.ToolCallID != "fetch-1" || !strings.Contains(last.Content, expected) {
							t.Errorf("fetch feedback=%+v", last)
						}
						delta = map[string]any{"content": "Final answer"}
					}
					chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta}}})
					fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
				default:
					t.Errorf("path=%s", r.URL.Path)
					http.NotFound(w, r)
				}
			}, "")
			p := tools.NewPipeline()
			for _, tool := range d.WebTools() {
				p.WithTool(tool)
			}
			id := brand.NewSessionID("web-test")
			log := session.NewSessionLog(id)
			a := agent.NewAgent(id, log, nil, p, d)
			defer a.Dispose()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			a.SetRunContext(ctx, 5*time.Second)
			if err := a.Run("Research this topic"); err != nil {
				t.Fatal(err)
			}
			if err := a.WhenIdle(ctx); err != nil {
				t.Fatal(err)
			}
			if chats.Load() != 3 || searches.Load() != 1 || fetches.Load() != 1 {
				t.Fatalf("requests=%d/%d/%d", chats.Load(), searches.Load(), fetches.Load())
			}
			found := false
			for _, event := range log.Events() {
				switch data := event.Data.(type) {
				case session.AssistantMessageData:
					if data.Content == "Final answer" {
						found = true
					}
				case session.ToolResultData:
					if data.CallID.Raw() == "fetch-1" && data.IsError != fetchFails {
						t.Errorf("result=%+v", data)
					}
				}
			}
			if !found {
				t.Fatal("missing final answer")
			}
		})
	}
}

func TestWebEmptyAndIncompleteResponses(t *testing.T) {
	d := webClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"results":[]}`) }, "")
	result, err := d.Search(context.Background(), ds.SearchRequest{Query: "no matches"})
	if err != nil || len(result.Results) != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	for _, body := range []string{`{}`, `null`, `{"title":"page"}`, `{"title":null,"content":"text"}`} {
		d := webClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }, "")
		if _, err := d.Fetch(context.Background(), "https://example.com"); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
}

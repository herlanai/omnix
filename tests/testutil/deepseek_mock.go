// Package testutil provides mock DeepSeek helpers for N07 cache E2E tests.
package testutil

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
)

// MockChatRequest carries the prompt fields used by the mock.
type MockChatRequest struct {
	Messages []struct {
		Role    string `json:"role"`
		Content any    `json:"content"`
	} `json:"messages"`
}

// PrefixHash fingerprints the prompt prefix for cache matching.
type PrefixHash struct {
	// PromptText contains the assembled prompt.
	PromptText string
	// Tokens estimates token count as characters divided by four.
	Tokens int
}

// PrefixCacheSimulator records prompts and estimates hits from their longest common prefix. Stable
// turns yield high ratios; a preset switch initially misses, then recovers as the new prefix
// repeats.
type PrefixCacheSimulator struct {
	mu         sync.Mutex
	lastPrompt string
}

// NewPrefixCacheSimulator creates a simulator.
func NewPrefixCacheSimulator() *PrefixCacheSimulator {
	return &PrefixCacheSimulator{}
}

// LongestCommonPrefix returns the length of the shared prefix of two strings.
func LongestCommonPrefix(a, b string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

// Simulate returns hit and miss token counts based on the shared prefix with the previous prompt;
// no shared prefix means all misses.
func (s *PrefixCacheSimulator) Simulate(prompt string) (hit, miss int) {
	tok := len(prompt) / 4
	if tok < 1 {
		tok = 1
	}
	s.mu.Lock()
	last := s.lastPrompt
	s.lastPrompt = prompt
	s.mu.Unlock()
	common := LongestCommonPrefix(prompt, last)
	hitFrac := float64(common) / float64(len(prompt))
	if len(prompt) == 0 {
		hitFrac = 0
	}
	hit = int(float64(tok) * hitFrac)
	miss = tok - hit
	return hit, miss
}

// NewDeepSeekMock creates a mock server whose SSE usage cache fields are derived from
// PrefixCacheSimulator for each request.
func NewDeepSeekMock() (*httptest.Server, *PrefixCacheSimulator) {
	sim := NewPrefixCacheSimulator()
	var mu sync.Mutex
	var reports []float64 // Cache-hit ratio history.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req MockChatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		// Assemble the prompt text.
		promptText := ""
		for _, m := range req.Messages {
			switch c := m.Content.(type) {
			case string:
				promptText += c + "\n"
			case []any:
				for _, blk := range c {
					if s, ok := blk.(string); ok {
						promptText += s + "\n"
					}
				}
			}
		}
		hit, miss := sim.Simulate(promptText)
		ratio := float64(hit) / float64(hit+miss)
		if hit+miss == 0 {
			ratio = 0
		}
		mu.Lock()
		reports = append(reports, ratio)
		mu.Unlock()
		// Return SSE containing usage data.
		body := `data: {"choices":[],"usage":{"prompt_tokens":` + itoa(hit+miss) +
			`,"completion_tokens":5,"prompt_cache_hit_tokens":` + itoa(hit) +
			`,"prompt_cache_miss_tokens":` + itoa(miss) + `}}` + "\n\ndata: [DONE]\n\n"
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(body))
	}))
	return srv, sim
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

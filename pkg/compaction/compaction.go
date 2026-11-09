// Package compaction summarizes long conversations using surface replacement (S01),
// matching compaction/compaction and compaction-engine-basic. When estimated event
// tokens exceed MaxTokens, it summarizes the oldest surface into an assistant
// message with a replacement range. Source events remain immutable and append-only.
// New events can still be appended, and request headers use effective events only.
// Summarizer supports BasicEngine (prefix summaries without an LLM) and an optional
// LLMEngine with an injected llm.LLMAdapter.
package compaction

import (
	"context"
	"fmt"
	"time"

	"github.com/herlanai/omnix/pkg/session"
)

// Constants and configuration.

// defaultMaxTokens is the estimated token threshold for compaction.
const defaultMaxTokens = 20000

// defaultKeepTail is the number of recent messages excluded from compaction.
const defaultKeepTail = 10

// defaultSummaryMaxRunes limits the length of each summary.
const defaultSummaryMaxRunes = 800

// Config configures compaction.
type Config struct {
	MaxTokens       int // Compact when estimated event tokens exceed this threshold.
	KeepTail        int // Number of recent messages preserved without replacement.
	SummaryMaxRunes int // Maximum summary length in runes.
}

// WithDefaults returns a copy with defaults filled in.
func (c Config) WithDefaults() Config {
	if c.MaxTokens <= 0 {
		c.MaxTokens = defaultMaxTokens
	}
	if c.KeepTail <= 0 {
		c.KeepTail = defaultKeepTail
	}
	if c.SummaryMaxRunes <= 0 {
		c.SummaryMaxRunes = defaultSummaryMaxRunes
	}
	return c
}

// Summarization interface.

// Summarizer summarizes old events selected for replacement.
type Summarizer interface {
	// Summarize returns the summary text.
	Summarize(ctx context.Context, events []session.SessionEvent, maxRunes int) (string, error)
}

// Token estimation and trigger helpers.

// tokenEstimate approximates event tokens: Chinese characters/1.5, Latin words.
func tokenEstimate(ev session.SessionEvent) int {
	var text string
	switch d := ev.Data.(type) {
	case session.UserMessageData:
		text = d.Content
	case session.AssistantMessageData:
		text = d.Content
	case session.SessionTitleData:
		text = d.Title
	case session.InjectionContextData:
		text = d.Content
	default:
		return 0
	}
	runes := len([]rune(text))
	return runes/2 + 1 // Approximate Chinese token density.
}

// EstimateTokens estimates total event tokens for trigger decisions.
func EstimateTokens(events []session.SessionEvent) int {
	total := 0
	for _, ev := range events {
		total += tokenEstimate(ev)
	}
	return total
}

// ShouldCompact reports whether compaction is needed.
func ShouldCompact(events []session.SessionEvent, maxTokens int) bool {
	if maxTokens <= 0 {
		return false
	}
	return EstimateTokens(events) > maxTokens
}

// ============================================================================
// Result
// ============================================================================

// Result contains the output of one compaction.
type Result struct {
	Start       uint64               // Inclusive start sequence of the replaced range.
	End         uint64               // Inclusive end sequence of the replaced range.
	Summary     string               // Summary text.
	Replacement session.SessionEvent // Generated replacement summary event, including SurfaceOp.
	// Effective contains the original events plus the appended Replacement.
	Effective []session.SessionEvent
}

// ============================================================================
// Compactor
// ============================================================================

// Compactor checks the trigger, selects a range, summarizes, and creates its replacement.
type Compactor struct {
	cfg        Config
	summarizer Summarizer
}

// New creates a compactor, using BasicEngine when summarizer is nil.
func New(cfg Config, summarizer Summarizer) *Compactor {
	cfg = cfg.WithDefaults()
	if summarizer == nil {
		summarizer = &BasicEngine{}
	}
	return &Compactor{cfg: cfg, summarizer: summarizer}
}

// ShouldCompact checks the configured threshold.
func (c *Compactor) ShouldCompact(events []session.SessionEvent) bool {
	return ShouldCompact(events, c.cfg.MaxTokens)
}

// Compact summarizes the oldest events while preserving KeepTail recent messages.
// It appends an assistant summary with SurfaceOp replacing that range and returns
// the resulting event list.
func (c *Compactor) Compact(ctx context.Context, events []session.SessionEvent) (Result, error) {
	if len(events) == 0 {
		return Result{}, fmt.Errorf("compaction: empty events")
	}
	if !c.ShouldCompact(events) {
		return Result{}, fmt.Errorf("compaction: below token threshold, no need to compact")
	}

	// Find the replacement boundary while retaining KeepTail recent user/assistant messages.
	keepTail := c.cfg.KeepTail * 2 // Approximate each exchange as a user question and assistant answer.
	if keepTail >= len(events) {
		keepTail = len(events) - 1
		if keepTail < 1 {
			keepTail = 0
		}
	}
	keepIndex := len(events) - keepTail // Keep events[keepIndex:] and replace events[:keepIndex].

	start := events[0].Seq
	end := events[keepIndex-1].Seq
	if keepIndex == 0 {
		// With no retained tail, end at the last replaceable sequence.
		end = events[len(events)-1].Seq
	}

	// Summarize the old events selected for replacement.
	oldForSummary := events[:keepIndex]
	summary, err := c.summarizer.Summarize(ctx, oldForSummary, c.cfg.SummaryMaxRunes)
	if err != nil {
		return Result{}, err
	}
	// Truncate summaries to maxRunes as a final length safeguard.
	runes := []rune(summary)
	if len(runes) > c.cfg.SummaryMaxRunes {
		summary = string(runes[:c.cfg.SummaryMaxRunes])
	}

	// Create an assistant/message replacing the inclusive range [start,end].
	lastSeq := events[len(events)-1].Seq
	replacement := session.SessionEvent{
		Seq:       lastSeq + 1,
		Time:      time.Now(),
		Type:      session.EventAssistantMessage,
		Data:      session.AssistantMessageData{Content: summary},
		SurfaceOp: session.NewReplaceOp(start, end, nil),
	}
	// Source events stay immutable; appending the replacement completes compaction.
	effective := append(append([]session.SessionEvent(nil), events...), replacement)

	return Result{
		Start:       start,
		End:         end,
		Summary:     summary,
		Replacement: replacement,
		Effective:   effective,
	}, nil
}

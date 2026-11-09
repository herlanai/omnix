// This file implements DeepSeek prefix-cache instrumentation (N01), following
// the API's KV-cache usage fields. CacheStats aggregates hit/miss tokens;
// ComputeHitRatio handles empty, all-hit, and all-miss usage without NaN.
// CacheLogLine provides cache.hit_ratio for monitoring, N08 alerts, and
// the cache acceptance-test baseline.
package llm

import (
	"fmt"
	"sync"
)

// CacheStats aggregates prefix-cache usage for one or more requests.
type CacheStats struct {
	// HitTokens counts prompt tokens served from cache.
	HitTokens int `json:"hitTokens"`
	// MissTokens counts prompt tokens not served from cache.
	MissTokens int `json:"missTokens"`
}

// Add accumulates one usage record.
func (c *CacheStats) Add(hit, miss int) {
	c.HitTokens += hit
	c.MissTokens += miss
}

// Total returns hit plus miss prompt tokens.
func (c *CacheStats) Total() int {
	return c.HitTokens + c.MissTokens
}

// ComputeHitRatio returns hit/total, or zero for empty usage.
// All hits yield one and all misses yield zero; the result is never 0/0.
func (c *CacheStats) ComputeHitRatio() float64 {
	total := c.Total()
	if total <= 0 {
		return 0.0
	}
	return float64(c.HitTokens) / float64(total)
}

// HitRatioOf computes the cache hit ratio directly from Usage.
func HitRatioOf(u Usage) float64 {
	cs := CacheStats{HitTokens: u.PromptCacheHitTokens, MissTokens: u.PromptCacheMissTokens}
	return cs.ComputeHitRatio()
}

// CacheLogLine formats a request log field such as cache.hit_ratio=0.95
// for N01 instrumentation and monitoring.
func CacheLogLine(u Usage) string {
	return fmt.Sprintf("cache.hit_ratio=%.3f", HitRatioOf(u))
}

// RecordCacheUsage adds usage to cumulative statistics for metering and monitoring.
func (c *CacheStats) RecordUsage(u Usage) {
	c.Add(u.PromptCacheHitTokens, u.PromptCacheMissTokens)
}

// N08: cache hit-rate degradation alerts.

// AlertLevel identifies alert severity.
type AlertLevel string

// Alert severity levels.
const (
	AlertWarn  AlertLevel = "warn"
	AlertError AlertLevel = "error"
)

// Alert describes a cache hit-rate degradation alert.
type Alert struct {
	// Level identifies severity.
	Level AlertLevel `json:"level"`
	// Message includes session ID and current/previous ratios.
	Message string `json:"message"`
	// Webhook requests notification after consecutive failures.
	Webhook bool `json:"webhook,omitempty"`
}

// AlertSink delivers alerts to logging or webhook handlers.
type AlertSink func(a Alert)

// CacheAlert detects and classifies cache hit-rate degradation. A drop larger
// than DropWarn emits a warning with session/current/previous values.
// ConsecutiveFails observations below Threshold emit an error and optional
// webhook notification. Recovery to Threshold resets the consecutive count.
type CacheAlert struct {
	// Threshold is the low hit-rate threshold; the default is 0.5.
	Threshold float64
	// DropWarn is the sudden-drop threshold relative to the previous ratio; default 0.3.
	DropWarn float64
	// ConsecutiveFails is the number of low ratios before an error; default five.
	ConsecutiveFails int
	// Sink receives alerts.
	Sink AlertSink

	mu               sync.Mutex
	prevRatio        float64
	consecutiveFails int
	hasPrev          bool
	alerts           []Alert
}

// NewCacheAlert creates an alert detector with default settings.
func NewCacheAlert(sink AlertSink) *CacheAlert {
	return &CacheAlert{
		Threshold:        0.5,
		DropWarn:         0.3,
		ConsecutiveFails: 5,
		Sink:             sink,
	}
}

// Observe records a ratio and emits any alerts, using sessionID as context.
func (a *CacheAlert) Observe(sessionID string, ratio float64) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Warn about a sudden drop from the previous observation.
	if a.hasPrev && a.prevRatio-ratio > a.DropWarn {
		alert := Alert{Level: AlertWarn,
			Message: "cache hit ratio dropped: session=" + sessionID +
				" current=" + ft(ratio) + " previous=" + ft(a.prevRatio)}
		a.emit(alert, false)
	}

	// Alert after consecutive low hit ratios.
	if ratio < a.Threshold {
		a.consecutiveFails++
		if a.consecutiveFails >= a.ConsecutiveFails {
			alert := Alert{Level: AlertError,
				Message: "cache hit ratio persistently low: session=" + sessionID +
					" consecutive=" + itoa2(a.consecutiveFails) + " ratio=" + ft(ratio)}
			a.emit(alert, true)
			a.consecutiveFails = 0 // Reset the counter after each error alert.
		}
	} else {
		a.consecutiveFails = 0
	}

	a.prevRatio = ratio
	a.hasPrev = true
}

func (a *CacheAlert) emit(al Alert, webhook bool) {
	al.Webhook = webhook
	a.alerts = append(a.alerts, al)
	if a.Sink != nil {
		a.Sink(al)
	}
}

// Alerts returns a snapshot of accumulated alerts.
func (a *CacheAlert) Alerts() []Alert {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]Alert(nil), a.alerts...)
}

func ft(f float64) string {
	return fmt.Sprintf("%.3f", f)
}

func itoa2(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

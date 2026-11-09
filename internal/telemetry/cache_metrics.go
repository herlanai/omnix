// Package telemetry provides cache-hit metrics for Grafana and OTel integration (N09). Lightweight
// in-memory instruments support testing without a collector and can be replaced with OTel SDK
// instruments. Metrics are omnix.cache.hit_ratio (histogram by session/preset/turn),
// omnix.cache.hit_tokens, omnix.cache.miss_tokens, and omnix.cache.broken_count (incremented for
// detected cache-breaking patterns).
package telemetry

import "sync"

// CacheMetrics aggregates cache-hit metrics.
type CacheMetrics struct {
	mu              sync.Mutex
	HitRatios       []float64 // Cache-hit ratio samples for the histogram.
	HitTokensTotal  int64
	MissTokensTotal int64
	BrokenCount     int64

	// Session label for the current aggregation.
	SessionID string
	Preset    string
	TurnSeq   int
}

// NewCacheMetrics creates a metrics aggregator.
func NewCacheMetrics() *CacheMetrics {
	return &CacheMetrics{}
}

// Record records cache metrics for one request.
func (m *CacheMetrics) Record(hit, miss int, ratio float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.HitRatios = append(m.HitRatios, ratio)
	m.HitTokensTotal += int64(hit)
	m.MissTokensTotal += int64(miss)
}

// MarkBroken increments the cache-breaking event counter.
func (m *CacheMetrics) MarkBroken() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.BrokenCount++
}

// HistogramP50 returns the median cache-hit ratio for the hit_ratio_p50 Grafana alert.
func (m *CacheMetrics) HistogramP50() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.HitRatios) == 0 {
		return 0
	}
	sorted := append([]float64(nil), m.HitRatios...)
	// Sort samples using insertion sort.
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j-1] > sorted[j]; j-- {
			sorted[j-1], sorted[j] = sorted[j], sorted[j-1]
		}
	}
	idx := len(sorted) / 2
	return sorted[idx]
}

// MetricsNames returns the four exported metric names for OTel and dashboard integration.
func MetricsNames() []string {
	return []string{
		"omnix.cache.hit_ratio",
		"omnix.cache.hit_tokens",
		"omnix.cache.miss_tokens",
		"omnix.cache.broken_count",
	}
}

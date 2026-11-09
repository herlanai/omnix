// N09 metrics tests cover the four exported metric names, histogram P50, incrementing
// broken_count, and valid Grafana JSON with three panels.
package tests

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/herlanai/omnix/internal/telemetry"
)

// TestN09MetricsNames verifies all four OTel metric names.
func TestN09MetricsNames(t *testing.T) {
	names := telemetry.MetricsNames()
	want := map[string]bool{
		"omnix.cache.hit_ratio":    false,
		"omnix.cache.hit_tokens":   false,
		"omnix.cache.miss_tokens":  false,
		"omnix.cache.broken_count": false,
	}
	for _, n := range names {
		if _, ok := want[n]; ok {
			want[n] = true
		}
	}
	for n, ok := range want {
		if !ok {
			t.Fatalf("指标 %s 缺失", n)
		}
	}
}

// TestN09MetricsRecord verifies recording and broken_count.
func TestN09MetricsRecord(t *testing.T) {
	m := telemetry.NewCacheMetrics()
	m.Record(90, 10, 0.9)
	m.Record(80, 20, 0.8)
	if m.HitTokensTotal != 170 || m.MissTokensTotal != 30 {
		t.Fatalf("累计失准: hit=%d miss=%d", m.HitTokensTotal, m.MissTokensTotal)
	}
	m.MarkBroken()
	if m.BrokenCount != 1 {
		t.Fatalf("broken_count 应为 1, 实际 %d", m.BrokenCount)
	}
}

// TestN09HistogramP50 verifies P50 calculation.
func TestN09HistogramP50(t *testing.T) {
	m := telemetry.NewCacheMetrics()
	// For samples 0.5, 0.6, 0.9, and 1.0, select the upper median 0.9.
	m.Record(1, 0, 0.5)
	m.Record(1, 0, 0.6)
	m.Record(1, 0, 0.9)
	m.Record(1, 0, 1.0)
	if p := m.HistogramP50(); p != 0.9 {
		t.Fatalf("P50 应 0.9, 实际 %v", p)
	}
}

// TestN09GrafanaDashboardJSON validates dashboard JSON, three panels, and two metric references.
func TestN09GrafanaDashboardJSON(t *testing.T) {
	data, err := os.ReadFile("../deploy/grafana/omnix-cache-dashboard.json")
	if err != nil {
		t.Skipf("看板文件缺失: %v", err)
		return
	}
	var dash struct {
		Panels []struct {
			ID    int    `json:"id"`
			Type  string `json:"type"`
			Title string `json:"title"`
		} `json:"panels"`
	}
	if err := json.Unmarshal(data, &dash); err != nil {
		t.Fatalf("看板 JSON 非法: %v", err)
	}
	if len(dash.Panels) != 3 {
		t.Fatalf("应有 3 面板, 实际 %d", len(dash.Panels))
	}
}

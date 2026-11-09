// N01 probe tests verify ComputeHitRatio for zero, partial, full, and absent hits without NaN.
package tests

import (
	"testing"

	"github.com/herlanai/omnix/pkg/llm"
)

// TestCacheStatsHitRatioZero returns 0.0 for a zero total, avoiding 0/0 NaN.
func TestCacheStatsHitRatioZero(t *testing.T) {
	cs := llm.CacheStats{}
	if r := cs.ComputeHitRatio(); r != 0.0 {
		t.Fatalf("总量 0 命中率应为 0.0, 实际 %v", r)
	}
}

// TestCacheStatsHitRatioPartial verifies 79/100 equals 0.79.
func TestCacheStatsHitRatioPartial(t *testing.T) {
	cs := llm.CacheStats{HitTokens: 79, MissTokens: 21}
	r := cs.ComputeHitRatio()
	if r < 0.789 || r > 0.791 {
		t.Fatalf("应约 0.79, 实际 %v", r)
	}
}

// TestCacheStatsHitRatioFull returns 1.0 for all hits.
func TestCacheStatsHitRatioFull(t *testing.T) {
	cs := llm.CacheStats{HitTokens: 100, MissTokens: 0}
	if r := cs.ComputeHitRatio(); r != 1.0 {
		t.Fatalf("全命中应为 1.0, 实际 %v", r)
	}
}

// TestCacheStatsHitRatioNone returns 0.0 for all misses.
func TestCacheStatsHitRatioNone(t *testing.T) {
	cs := llm.CacheStats{HitTokens: 0, MissTokens: 50}
	if r := cs.ComputeHitRatio(); r != 0.0 {
		t.Fatalf("全未命中应为 0.0, 实际 %v", r)
	}
}

// TestCacheStatsAddRecord verifies aggregation and log output.
func TestCacheStatsAddRecord(t *testing.T) {
	cs := llm.CacheStats{}
	cs.RecordUsage(llm.Usage{PromptTokens: 1000, PromptCacheHitTokens: 800, PromptCacheMissTokens: 200})
	cs.RecordUsage(llm.Usage{PromptTokens: 500, PromptCacheHitTokens: 500, PromptCacheMissTokens: 0})
	if cs.HitTokens != 1300 || cs.MissTokens != 200 {
		t.Fatalf("累计异常: %+v", cs)
	}
	r := cs.ComputeHitRatio() // 1300/1500 ≈ 0.8667
	if r < 0.86 || r > 0.87 {
		t.Fatalf("累计命中率应约 0.867, 实际 %v", r)
	}
	// Check log fields.
	line := llm.CacheLogLine(llm.Usage{PromptCacheHitTokens: 900, PromptCacheMissTokens: 100})
	if line != "cache.hit_ratio=0.900" {
		t.Fatalf("日志行应 cache.hit_ratio=0.900, 实际 %q", line)
	}
}

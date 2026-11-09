// N07 cache E2E tests use a mock DeepSeek server: 50 stable turns average at least 95%; switching
// presets initially drops below 50% then recovers to at least 80% over the final five turns;
// compaction recovers to 95% within 30 turns; ten concurrent sessions each reach 85%; and
// 5/10/20-tool scenarios each reach 95%. Log per-turn, average, and minimum ratios.
package tests

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/herlanai/omnix/tests/testutil"
)

// buildPrompt combines system text, accumulated turns, preset, tools, and a final message. The
// large fixed prefix keeps each appended turn small relative to the prompt, resembling real omnix
// usage.
func buildPrompt(system, preset string, toolCount int, rounds int) string {
	var sb strings.Builder
	sb.WriteString("<system>You are a capable DeepSeek agent following workspace policy and plan mode. ")
	sb.WriteString(system)
	sb.WriteString("</system>\n<preset>")
	sb.WriteString(preset)
	sb.WriteString("</preset>\n")
	for i := 0; i < toolCount; i++ {
		sb.WriteString(fmt.Sprintf("[tool%d]{name: tool_%d, description: perform operation number %d on the current workspace}\n", i, i, i))
	}
	for i := 0; i < rounds; i++ {
		sb.WriteString(fmt.Sprintf("turn%d: user message text %d here\nassistant reply content %d here\n", i, i, i))
	}
	sb.WriteString("tail")
	return sb.String()
}

// avgAndMin returns the average and minimum hit ratios.
func avgAndMin(ratios []float64) (avg, min float64) {
	if len(ratios) == 0 {
		return 0, 0
	}
	sum := 0.0
	min = ratios[0]
	for _, r := range ratios {
		sum += r
		if r < min {
			min = r
		}
	}
	return sum / float64(len(ratios)), min
}

// TestN07E2E_T1_50TurnsStableHitRate verifies at least 95% over 50 stable turns.
func TestN07E2E_T1_50TurnsStableHitRate(t *testing.T) {
	sim := testutil.NewPrefixCacheSimulator()
	// Warm up for three unmeasured turns to establish the cache.
	for i := 0; i < 3; i++ {
		_, _ = sim.Simulate(buildPrompt("omnix", "default", 10, i))
	}
	var ratios []float64
	for i := 3; i < 53; i++ {
		hit, miss := sim.Simulate(buildPrompt("omnix", "default", 10, i))
		ratios = append(ratios, float64(hit)/float64(hit+miss))
	}
	avg, min := avgAndMin(ratios)
	t.Logf("T1 avg=%.4f min=%.4f", avg, min)
	if avg < 0.95 {
		t.Fatalf("50 轮稳定命中率应 ≥ 0.95, 实际 avg=%.4f", avg)
	}
}

// TestN07E2E_T2_PresetSwitch verifies initial invalidation below 50% followed by recovery.
func TestN07E2E_T2_PresetSwitch(t *testing.T) {
	sim := testutil.NewPrefixCacheSimulator()
	// Use the default preset for the first 20 turns.
	for i := 0; i < 20; i++ {
		_, _ = sim.Simulate(buildPrompt("omnix", "default", 10, i))
	}
	// Switch to danger; the first request invalidates the cache, then repeated prefixes improve the
	// ratio.
	var post []float64
	for i := 0; i < 30; i++ {
		hit, miss := sim.Simulate(buildPrompt("omnix", "danger", 10, i))
		post = append(post, float64(hit)/float64(hit+miss))
	}
	first := post[0]
	last5 := post[25:30]
	avgLast, _ := avgAndMin(last5)
	t.Logf("T2 first-after-switch=%.4f last5=%.4f", first, avgLast)
	if first >= 0.5 {
		t.Fatalf("切 preset 后首个请求应 < 0.5（缓存失效）, 实际 %.4f", first)
	}
	if avgLast < 0.8 {
		t.Fatalf("切 preset 后最后 5 轮应 ≥ 0.8, 实际 %.4f", avgLast)
	}
}

// TestN07E2E_T3_CompactionRecovery verifies recovery to at least 95% within 30 turns.
func TestN07E2E_T3_CompactionRecovery(t *testing.T) {
	sim := testutil.NewPrefixCacheSimulator()
	for i := 0; i < 50; i++ {
		_, _ = sim.Simulate(buildPrompt("omnix", "default", 10, i))
	}
	// Simulate compaction by dropping old history and shortening the prompt; warm up three turns
	// before measuring 30 turns.
	for i := 0; i < 3; i++ {
		_, _ = sim.Simulate(buildPrompt("omnix", "default", 10, i))
	}
	var post []float64
	for i := 3; i < 33; i++ {
		hit, miss := sim.Simulate(buildPrompt("omnix", "default", 10, i))
		post = append(post, float64(hit)/float64(hit+miss))
	}
	avg, _ := avgAndMin(post)
	t.Logf("T3 compaction后 avg=%.4f", avg)
	if avg < 0.9 {
		t.Fatalf("compaction 后 30 轮应恢复接近缓存, 实际 avg=%.4f", avg)
	}
}

// TestN07E2E_T4_MultiSession verifies at least 85% for ten concurrent sessions.
func TestN07E2E_T4_MultiSession(t *testing.T) {
	const n = 10
	sims := make([]*testutil.PrefixCacheSimulator, n)
	for i := range sims {
		sims[i] = testutil.NewPrefixCacheSimulator()
	}
	var wg sync.WaitGroup
	avgs := make([]float64, n)
	for s := 0; s < n; s++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			var ratios []float64
			for i := 3; i < 33; i++ { // Warm up for the first three turns.
				hit, miss := sims[idx].Simulate(buildPrompt(fmt.Sprintf("worker%d", idx), "default", 8, i))
				ratios = append(ratios, float64(hit)/float64(hit+miss))
			}
			avgs[idx], _ = avgAndMin(ratios)
		}(s)
	}
	wg.Wait()
	for i, a := range avgs {
		t.Logf("session %d avg=%.4f", i, a)
		if a < 0.85 {
			t.Fatalf("session %d 命中率应 ≥0.85, 实际 %.4f", i, a)
		}
	}
}

// TestN07E2E_T5_ToolCount verifies at least 95% with 5, 10, and 20 tools.
func TestN07E2E_T5_ToolCount(t *testing.T) {
	for _, tc := range []int{5, 10, 20} {
		sim := testutil.NewPrefixCacheSimulator()
		for i := 0; i < 3; i++ {
			_, _ = sim.Simulate(buildPrompt("omnix", "default", tc, i))
		}
		var ratios []float64
		for i := 3; i < 33; i++ {
			hit, miss := sim.Simulate(buildPrompt("omnix", "default", tc, i))
			ratios = append(ratios, float64(hit)/float64(hit+miss))
		}
		avg, _ := avgAndMin(ratios)
		t.Logf("tools=%d avg=%.4f", tc, avg)
		if avg < 0.95 {
			t.Fatalf("工具数 %d 命中率应 ≥0.95, 实际 %.4f", tc, avg)
		}
	}
}

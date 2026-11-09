// N08 cache alert tests cover a hit-ratio drop greater than 30% producing a warning with
// session/current/previous fields, five consecutive ratios below 50% producing an error and
// webhook, recovery resetting the counter, and configurable thresholds.
package tests

import (
	"testing"

	"github.com/herlanai/omnix/pkg/llm"
)

// collectAlerts collects alerts through a sink.
type collectAlerts struct {
	alerts []llm.Alert
}

func (c *collectAlerts) sink(a llm.Alert) { c.alerts = append(c.alerts, a) }

// TestN08SuddenDropWarns warns on a drop greater than 30%.
func TestN08SuddenDropWarns(t *testing.T) {
	col := &collectAlerts{}
	ca := llm.NewCacheAlert(col.sink)
	// Establish a normal baseline first.
	ca.Observe("s1", 0.95)
	// Drop to 0.3: the 0.65 decrease exceeds the 0.3 threshold.
	ca.Observe("s1", 0.30)
	var sawWarn bool
	for _, a := range col.alerts {
		if a.Level == llm.AlertWarn {
			sawWarn = true
			if !containsStr(a.Message, "s1") || !containsStr(a.Message, "current") || !containsStr(a.Message, "previous") {
				t.Fatalf("warn 应含 session/current/previous: %q", a.Message)
			}
		}
	}
	if !sawWarn {
		t.Fatal("突降应产生 warn 告警")
	}
}

// TestN08ConsecutiveFailsSignalsError emits an error and webhook after five ratios below 50%.
func TestN08ConsecutiveFailsSignalsError(t *testing.T) {
	col := &collectAlerts{}
	ca := llm.NewCacheAlert(col.sink)
	for i := 0; i < 6; i++ {
		ca.Observe("s2", 0.10)
	}
	var sawError bool
	for _, a := range col.alerts {
		if a.Level == llm.AlertError {
			sawError = true
			if !a.Webhook {
				t.Fatal("连续破窗 error 应触发 webhook")
			}
		}
	}
	if !sawError {
		t.Fatal("连续 5 次低命中率应产生 error + webhook")
	}
}

// TestN08RecoveryResets clears consecutive failures after recovery.
func TestN08RecoveryResets(t *testing.T) {
	col := &collectAlerts{}
	ca := llm.NewCacheAlert(col.sink)
	// Three low ratios remain below the five-failure threshold.
	for i := 0; i < 3; i++ {
		ca.Observe("s3", 0.10)
	}
	ca.Observe("s3", 0.90) // Recovery resets the counter.
	// Four more low ratios reach four failures, still below the error threshold.
	col.alerts = nil
	errCount := 0
	for i := 0; i < 4; i++ {
		ca.Observe("s3", 0.10)
	}
	for _, a := range col.alerts {
		if a.Level == llm.AlertError {
			errCount++
		}
	}
	if errCount != 0 {
		t.Fatalf("恢复后 4 次低不应触发 error（consecutive 已重置）, 实际 %d", errCount)
	}
}

// TestN08ConfigOverridable verifies configurable thresholds.
func TestN08ConfigOverridable(t *testing.T) {
	col := &collectAlerts{}
	ca := &llm.CacheAlert{Threshold: 0.8, DropWarn: 0.1, ConsecutiveFails: 2, Sink: col.sink}
	// Two ratios of 0.75 below the 0.8 threshold trigger an error.
	ca.Observe("s4", 0.75)
	ca.Observe("s4", 0.75)
	var sawError bool
	for _, a := range col.alerts {
		if a.Level == llm.AlertError {
			sawError = true
		}
	}
	if !sawError {
		t.Fatal("配置 threshold=0.8/consecutive=2 时 2 次 0.75 应触发 error")
	}
}

// Advisory repeat detection mirrors the upstream repeat-tool reminder. It never blocks or
// rewrites calls; it emits reminders when consecutive calls use the same tool and arguments.
// Full canonical arguments form the chain key so property-order changes do not evade detection.
package tools

import (
	"encoding/json"
	"sort"
	"strings"
)

// DefaultRepeatThresholds escalate from gentle to detailed reminders.
var DefaultRepeatThresholds = []int{3, 5, 8}

// canonicalValue recursively sorts map keys while preserving slice order.
func canonicalValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := map[string]any{}
		for _, k := range keys {
			out[k] = canonicalValue(t[k])
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i := range t {
			out[i] = canonicalValue(t[i])
		}
		return out
	default:
		return v
	}
}

// CanonicalizeArgs serializes arguments with recursively sorted keys.
func CanonicalizeArgs(input map[string]any) string {
	b, err := json.Marshal(canonicalValue(input))
	if err != nil {
		return ""
	}
	return string(b)
}

// RepeatState tracks one agent's consecutive repeated calls.
type RepeatState struct {
	lastKey string
	count   int
}

// Observe records a call and returns its consecutive repetition count; a different call resets
// the count to one.
func (r *RepeatState) Observe(toolName string, args map[string]any) int {
	key := toolName + "\x00" + CanonicalizeArgs(args)
	if key == r.lastKey {
		r.count++
	} else {
		r.lastKey = key
		r.count = 1
	}
	return r.count
}

// Reset clears the chain after user input so repetitions across interruptions do not count as
// loops.
func (r *RepeatState) Reset() {
	r.lastKey = ""
	r.count = 0
}

// HitThreshold reports whether the count matches a reminder threshold.
func HitThreshold(count int, thresholds []int) bool {
	for _, th := range thresholds {
		if count == th {
			return true
		}
	}
	return false
}

// GentleReminder is the first-threshold reminder.
const GentleReminder = "你在以完全相同的参数重复同一工具调用。请先仔细分析上一次结果，" +
	"若任务未完成，换一种方式或换一组参数，而不是重复调用。"

// DetailedReminder builds the reminder for later thresholds.
func DetailedReminder(toolName string, count int, canonical string, previewCap int) string {
	preview := canonical
	if previewCap > 0 && len(preview) > previewCap {
		preview = string([]rune(preview)[:previewCap]) + "…"
	}
	var b strings.Builder
	b.WriteString("检测到重复工具调用：\n")
	b.WriteString("- 工具：" + toolName + "\n")
	b.WriteString("- 连续次数：")
	b.WriteString(itoaRepeat(count))
	b.WriteString("\n- 参数：" + preview + "\n")
	b.WriteString("这些重复没有取得进展，请勿再用相同参数调用该工具。")
	return b.String()
}

func itoaRepeat(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

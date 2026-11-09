// Package timecontext renders request-time context matching context/time-context.
// It samples the current time and zone and reports elapsed time since the previous
// model-visible message so the model knows the current time and intervening duration.
package timecontext

import (
	"strings"
	"time"
)

// FormatElapsed renders whole seconds using compact d/h/m/s units.
func FormatElapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	seconds := int(d / time.Second)
	days := seconds / 86400
	seconds %= 86400
	hours := seconds / 3600
	seconds %= 3600
	minutes := seconds / 60
	seconds %= 60
	var parts []string
	if days > 0 {
		parts = append(parts, itoa(days)+"d")
	}
	if hours > 0 {
		parts = append(parts, itoa(hours)+"h")
	}
	if minutes > 0 {
		parts = append(parts, itoa(minutes)+"m")
	}
	parts = append(parts, itoa(seconds)+"s")
	return strings.Join(parts, " ")
}

// Render formats clock context; nil loc uses UTC and nil previous reports unavailable elapsed time.
func Render(now time.Time, loc *time.Location, previous *time.Time) string {
	if loc == nil {
		loc = time.UTC
	}
	elapsed := "unavailable"
	if previous != nil {
		elapsed = FormatElapsed(now.Sub(*previous))
	}
	var b strings.Builder
	b.WriteString("当前采样时间：")
	b.WriteString(now.In(loc).Format("2006-01-02 15:04:05 MST"))
	b.WriteString("\n时区：")
	b.WriteString(loc.String())
	b.WriteString("\n距上一条模型可见消息：")
	b.WriteString(elapsed)
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}

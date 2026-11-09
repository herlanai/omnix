// Tool-result pruning matches compaction-tool-result-pruner: keep the head and
// tail of long text and replace the middle with a marker. Rune boundaries preserve
// Unicode code points; text below the threshold is unchanged with changed=false.
package compaction

// PruneMarker replaces the removed middle portion.
const PruneMarker = "\n... [内容已省略] ...\n"

// PruneText preserves text at or below threshold runes. Otherwise it keeps head
// and tail runes with PruneMarker between them. Counts are nonnegative and the
// tail is limited to available content.
func PruneText(text string, threshold, head, tail int) (string, bool) {
	r := []rune(text)
	if len(r) <= threshold {
		return text, false
	}
	if head < 0 {
		head = 0
	}
	if tail < 0 {
		tail = 0
	}
	if head+tail > len(r) {
		// Keep the original when head plus tail covers it, avoiding overlap or inversion.
		return text, false
	}
	return string(r[:head]) + PruneMarker + string(r[len(r)-tail:]), true
}

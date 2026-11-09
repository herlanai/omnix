// Title normalization matches session-title/normalize: remove escapes, control
// and directional characters, normalize whitespace, and truncate to a UTF-8 byte
// budget without splitting characters.
package sessiontitle

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// CleanTitle removes escape/control characters and collapses whitespace to one line.
func CleanTitle(input string) string {
	var b strings.Builder
	b.Grow(len(input))
	prevSpace := false
	leading := true
	runes := []rune(input)
	for i := 0; i < len(runes); {
		r := runes[i]
		// Skip complete ANSI escape sequences beginning with ESC.
		if r == 0x1b {
			i = skipAnsi(runes, i)
			continue
		}
		switch {
		case isTitleControl(r):
			i++ // Discard control characters.
			continue
		case unicode.IsSpace(r):
			if leading || prevSpace {
				i++
				continue
			}
			b.WriteRune(' ')
			prevSpace = true
			i++
			continue
		}
		b.WriteRune(r)
		prevSpace = false
		leading = false
		i++
	}
	return strings.TrimRight(b.String(), " ")
}

// skipAnsi returns the index after the escape sequence starting at idx (ESC).
func skipAnsi(runes []rune, idx int) int {
	if idx+1 >= len(runes) {
		return len(runes)
	}
	switch runes[idx+1] {
	case '[': // CSI ends at a byte in 0x40-0x7e.
		j := idx + 2
		for j < len(runes) && !(runes[j] >= 0x40 && runes[j] <= 0x7e) {
			j++
		}
		return j + 1
	case ']': // OSC ends at BEL or ST.
		j := idx + 2
		for j < len(runes) {
			if runes[j] == 0x07 {
				return j + 1
			}
			if runes[j] == 0x1b && j+1 < len(runes) && runes[j+1] == '\\' {
				return j + 2
			}
			j++
		}
		return j
	default: // Two-byte escape sequences.
		return idx + 2
	}
}

// isTitleControl identifies control, directional, and invisible characters to remove.
func isTitleControl(r rune) bool {
	if r == '\t' || r == '\n' || r == '\r' {
		return false // Handled by whitespace normalization.
	}
	if r <= 0x1f || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
		return true // C0 and C1 controls.
	}
	switch {
	case r == 0x200b, r == 0x200e, r == 0x200f: // Zero-width and directional characters.
		return true
	case r >= 0x202a && r <= 0x202e: // Directional formatting.
		return true
	case r >= 0x2060 && r <= 0x2064: // Invisible joining and formatting characters.
		return true
	case r >= 0x2066 && r <= 0x206f: // Directional isolates.
		return true
	case r == 0xfeff: // BOM and zero-width nonbreaking space.
		return true
	}
	return false
}

// TruncateUTF8 returns the longest complete-rune prefix within a UTF-8 byte budget.
func TruncateUTF8(input string, maxBytes int) string {
	if maxBytes <= 0 || len(input) <= maxBytes {
		if maxBytes <= 0 {
			return ""
		}
		return input
	}
	used := 0
	for _, r := range input {
		size := utf8.RuneLen(r)
		if used+size > maxBytes {
			break
		}
		used += size
	}
	return input[:used]
}

// Normalize cleans a title and truncates it to a byte budget.
func Normalize(input string, maxBytes int) string {
	return strings.TrimRight(TruncateUTF8(CleanTitle(input), maxBytes), " ")
}

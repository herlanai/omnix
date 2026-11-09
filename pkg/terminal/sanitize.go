// Terminal sanitization matches terminal-bash: strip ANSI CSI/OSC escape sequences
// from raw PTY output and normalize line endings into displayable plain text.
package terminal

import "strings"

// StripANSI removes CSI, OSC, and two-byte ANSI escape sequences.
func StripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != 0x1b { // ESC
			b.WriteByte(s[i])
			i++
			continue
		}
		if i+1 >= len(s) { // Discard a trailing lone ESC.
			break
		}
		switch s[i+1] {
		case '[': // CSI ends at a final byte in 0x40-0x7e after ESC[.
			j := i + 2
			for j < len(s) && !(s[j] >= 0x40 && s[j] <= 0x7e) {
				j++
			}
			i = j + 1
		case ']': // OSC ends at BEL or ST (ESC followed by backslash).
			j := i + 2
			for j < len(s) {
				if s[j] == 0x07 { // BEL
					j++
					break
				}
				if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
					j += 2
					break
				}
				j++
			}
			i = j
		default: // Two-byte escape sequences, such as ESC 7 and ESC 8.
			i += 2
		}
	}
	return b.String()
}

// NormalizeText converts CRLF and lone CR to LF and removes BEL.
func NormalizeText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, "\x07", "")
	return s
}

// Sanitize strips escapes and normalizes text in one call.
func Sanitize(s string) string {
	return NormalizeText(StripANSI(s))
}

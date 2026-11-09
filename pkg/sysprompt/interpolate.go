// Strict system-prompt interpolation replaces complete {{name}} groups using
// variables with names matching [a-z][a-z0-9_]*. Unknown or missing values fail.
// Replacement values are not rescanned; an unmatched {{ remains literal text.
package sysprompt

import (
	"errors"
	"regexp"
	"strings"
)

var variableName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Interpolate performs strict variable interpolation on text.
func Interpolate(text string, variables map[string]string) (string, error) {
	var b strings.Builder
	last := 0
	for {
		open := strings.Index(text[last:], "{{")
		if open < 0 {
			b.WriteString(text[last:])
			break
		}
		open += last
		// Find the first closing delimiter after open.
		relClose := strings.Index(text[open:], "}}")
		if relClose < 0 {
			// Without a closing delimiter, preserve the remainder literally.
			b.WriteString(text[last:])
			break
		}
		closeEnd := open + relClose + 2
		name := text[open+2 : open+relClose]
		// Another opening delimiter before the close is malformed nesting.
		if strings.Contains(name, "{{") {
			return "", errors.New("malformed variable reference near " + text[open:min(closeEnd, len(text))])
		}
		if !variableName.MatchString(name) {
			return "", errors.New("malformed variable name: " + name)
		}
		value, ok := variables[name]
		if !ok {
			return "", errors.New("unknown variable: " + name)
		}
		b.WriteString(text[last:open])
		b.WriteString(value) // Do not rescan replacement values.
		last = closeEnd
	}
	return b.String(), nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Package fileref implements context/file-reference @ mentions: find the token
// at the cursor and format selected paths as @path or @"path".
package fileref

import "regexp"

// Token describes the active @ token at the cursor.
type Token struct {
	Prefix string // The complete token to replace.
	Query  string // Path query following @.
	Quoted bool   // Whether the path is quoted.
}

var (
	reQuoted = regexp.MustCompile(`(?:^|\s)(@"([^"]*))$`)
	rePlain  = regexp.MustCompile(`(?:^|\s)(@([^\s@]*))$`)
)

// ActiveAtToken extracts an @path or quoted token before the cursor, or returns false.
func ActiveAtToken(line string, cursorCol int) (Token, bool) {
	if cursorCol < 0 || cursorCol > len(line) {
		return Token{}, false
	}
	before := line[:cursorCol]
	if m := reQuoted.FindStringSubmatch(before); m != nil {
		return Token{Prefix: m[1], Query: m[2], Quoted: true}, true
	}
	if m := rePlain.FindStringSubmatch(before); m != nil {
		return Token{Prefix: m[1], Query: m[2], Quoted: false}, true
	}
	return Token{}, false
}

var unsafePath = regexp.MustCompile(`[\x00-\x1f\x7f-\x9f"]`)
var hasSpace = regexp.MustCompile(`\s`)

// FormatMention formats a selected path, retaining directory slashes and quoting
// whitespace. It returns empty when the syntax cannot safely represent the path.
func FormatMention(path string, isDir, preserveQuote bool) string {
	if isDir && len(path) > 0 && path[len(path)-1] != '/' {
		path += "/"
	}
	if unsafePath.MatchString(path) {
		return ""
	}
	quoted := preserveQuote || hasSpace.MatchString(path)
	if !quoted {
		return "@" + path
	}
	if isDir {
		return `@"` + path
	}
	return `@"` + path + `"`
}

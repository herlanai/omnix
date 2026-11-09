// Static system-prompt checks support N03 and D2, matching upstream sections.
// Sections must render identical bytes for identical inputs, without time, cwd,
// randomness, or dynamic environment values. StaticCheck detects known patterns
// that break prefix caching; a clean result has no violations. SystemHash compares
// assembled prompts across turns to verify stability for the same configuration.
package sysprompt

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// Static checks for cache-breaking patterns.

// bannedPattern pairs a regex with an explanation.
type bannedPattern struct {
	re   *regexp.Regexp
	note string
}

// bannedPatterns contains the statically detectable D2 dynamic-value patterns.
var bannedPatterns = []bannedPattern{
	{re: regexp.MustCompile(`time\.Now\(\)`), note: "time.Now() 引入动态时间"},
	{re: regexp.MustCompile(`time\.Date\(`), note: "time.Date() 固定时刻"},
	{re: regexp.MustCompile(`os\.Getwd\(\)`), note: "os.Getwd() 依赖运行目录"},
	{re: regexp.MustCompile(`math/rand`), note: "引入随机数"},
	{re: regexp.MustCompile(`\brand\.\w+\(`), note: "rand.*() 随机调用"},
	{re: regexp.MustCompile(`os\.Getenv\("OMNIX_`), note: "动态读取 OMNIX_* env"},
	{re: regexp.MustCompile(`fmt\.Sprintf\(`), note: "fmt.Sprintf 动态拼串"},
}

// StaticCheck returns descriptions of matched forbidden patterns, or none.
func StaticCheck(text string) []string {
	var violations []string
	for _, bp := range bannedPatterns {
		if bp.re.MatchString(text) {
			violations = append(violations, bp.note)
		}
	}
	return violations
}

// CheckSections checks all registered sections; forbidden is true if any
// cache-breaking violation is detected.
func CheckSections(a *Assembler) (violations []string, forbidden bool) {
	for _, s := range a.Sections() {
		for _, v := range StaticCheck(s.Text) {
			violations = append(violations, s.Name+": "+v)
		}
	}
	return violations, len(violations) > 0
}

// Stable system-prompt hashes.

// SystemHash returns the assembled prompt's stable SHA-256 digest.
func SystemHash(sys string) string {
	sum := sha256.Sum256([]byte(sys))
	return hex.EncodeToString(sum[:])
}

// AssembleHash hashes the assembler's current prompt.
func (a *Assembler) AssembleHash() string {
	return SystemHash(a.Assemble())
}

// IsPure reports whether text passes the known forbidden-pattern checks.
func IsPure(sectionText string) bool {
	return len(StaticCheck(sectionText)) == 0
}

// StripBanned replaces common dynamic calls with static placeholders for test examples.
func StripBanned(s string) string {
	return strings.NewReplacer(
		"time.Now()", "NOW_STATIC",
		"os.Getwd()", "CWD_STATIC",
		"math/rand", "STATIC_RAND",
	).Replace(s)
}

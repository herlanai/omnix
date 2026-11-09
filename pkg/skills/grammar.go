// Skill grammar matches upstream kebab-case names and skill_content rendering.
// The block contains resource guidance and instructions; embedded text is escaped
// to prevent it from breaking out of the wrapper.
package skills

import (
	"regexp"
	"strings"
)

var skillName = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// IsName validates a kebab-case skill name.
func IsName(name string) bool { return skillName.MatchString(name) }

func escapeText(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

func escapeAttr(s string) string {
	r := strings.NewReplacer("&", "&amp;", `"`, "&quot;", "<", "&lt;")
	return r.Replace(s)
}

// RenderContent renders a skill_content block; provider identifies the resource
// manager and body contains instructions.
func RenderContent(name, provider, body string) string {
	var b strings.Builder
	b.WriteString(`<skill_content name="`)
	b.WriteString(escapeAttr(name))
	b.WriteString("\">\n<skill_resources>\n")
	b.WriteString("Resources for this skill are managed by provider \"")
	b.WriteString(escapeText(provider))
	b.WriteString("\".\nLoad referenced resources only as needed.\n</skill_resources>\n\n")
	b.WriteString("<skill_instructions>\n")
	b.WriteString(body)
	b.WriteString("\n</skill_instructions>\n</skill_content>")
	return b.String()
}

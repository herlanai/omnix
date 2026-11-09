// Package skills implements six-layer discovery, change watching, and tool-skill
// (M40), matching core/skills and providers. Layers are project-omnix, project-agents,
// custom, user-omnix, user-agents, and bundled; lower ranks are more authoritative.
// A Windows-friendly polling watcher detects directory hash changes and emits
// skills/change; production may substitute fsnotify. The skill tool checks model
// and user invocation permissions and injects context.
package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Types.

// Skill describes a skill.
type Skill struct {
	// Name is the source filename without its extension.
	Name string
	// Description is a short summary from the first Markdown line or description field.
	Description string
	// Path identifies the source file.
	Path string
	// Rank indicates authority: project-omnix=0 is highest, bundled=5 is lowest.
	Rank int
	// ModelInvocable permits model invocation.
	ModelInvocable bool
	// UserInvocable permits user invocation.
	UserInvocable bool
	// Content contains instructions to inject into context.
	Content string
}

// SkillCandidate records a discovered rank and locator.
type SkillCandidate struct {
	// Rank indicates authority.
	Rank int
	// Locator identifies the source path.
	Locator string
}

// SkillLayer describes one skill directory layer.
type SkillLayer struct {
	// Title names the layer, such as project-omnix or bundled.
	Title string
	// Dir is the directory path.
	Dir string
	// Rank indicates authority.
	Rank int
}

// defaultLayers returns six project-based layers ordered by rank 0 through 5.
func defaultLayers(root string) []SkillLayer {
	home, _ := os.UserHomeDir()
	return []SkillLayer{
		{Title: "project-omnix", Dir: filepath.Join(root, ".omnix", "skills"), Rank: 0},
		{Title: "project-agents", Dir: filepath.Join(root, ".agents", "skills"), Rank: 1},
		{Title: "custom", Dir: filepath.Join(root, ".config", "skills"), Rank: 2},
		{Title: "user-omnix", Dir: filepath.Join(home, ".omnix", "skills"), Rank: 3},
		{Title: "user-agents", Dir: filepath.Join(home, ".agents", "skills"), Rank: 4},
		{Title: "bundled", Dir: filepath.Join("internal", "skills"), Rank: 5},
	}
}

// SkillRegistry registers skills.
type SkillRegistry struct {
	mu     sync.RWMutex
	layers []SkillLayer
	skills map[string]*Skill // Names mapped to the most authoritative skill.
}

// New creates a registry with six default layers.
func New(root string) *SkillRegistry {
	return &SkillRegistry{
		layers: defaultLayers(root),
		skills: map[string]*Skill{},
	}
}

// AddLayer appends a custom layer.
func (r *SkillRegistry) AddLayer(l SkillLayer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.layers = append(r.layers, l)
}

// Layers returns the layers.
func (r *SkillRegistry) Layers() []SkillLayer {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]SkillLayer{}, r.layers...)
}

// Scan rescans all layer directories and keeps the most authoritative skill per name.
func (r *SkillRegistry) Scan() {
	r.mu.Lock()
	defer r.mu.Unlock()
	merged := map[string]*Skill{}
	// Scan layers in authority order. Retain an existing same-name skill when its
	// rank is lower, since lower numeric ranks have greater authority.
	layers := append([]SkillLayer{}, r.layers...)
	// Stable-sort by authority, with lower ranks first.
	sort.SliceStable(layers, func(i, j int) bool { return layers[i].Rank < layers[j].Rank })
	for _, layer := range layers {
		for _, skill := range scanDir(layer.Dir, layer.Rank) {
			if existing, ok := merged[skill.Name]; ok && existing.Rank < skill.Rank {
				// Do not overwrite a more authoritative skill.
				continue
			}
			merged[skill.Name] = skill
		}
	}
	r.skills = merged
}

// List returns all skills in lexical name order.
func (r *SkillRegistry) List() []*Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.skills))
	for n := range r.skills {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]*Skill, 0, len(names))
	for _, n := range names {
		out = append(out, r.skills[n])
	}
	return out
}

// Get looks up a skill, returning nil and false if absent.
func (r *SkillRegistry) Get(name string) (*Skill, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.skills[name]
	return s, ok
}

// Candidates returns discovered rank/locator pairs.
func (r *SkillRegistry) Candidates() []SkillCandidate {
	list := r.List()
	out := make([]SkillCandidate, 0, len(list))
	for _, s := range list {
		out = append(out, SkillCandidate{Rank: s.Rank, Locator: s.Path})
	}
	return out
}

// scanDir discovers Markdown skill files in one layer directory.
func scanDir(dir string, rank int) []*Skill {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []*Skill
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		s := parseSkill(name, path, string(data), rank)
		if s != nil {
			out = append(out, s)
		}
	}
	return out
}

// parseSkill parses Markdown, extracting the first description line and remaining body.
func parseSkill(name, path, text string, rank int) *Skill {
	lines := strings.Split(text, "\n")
	desc := ""
	modelInv := true
	userInv := true
	var bodyLines []string
	for _, ln := range lines {
		trim := strings.TrimSpace(ln)
		low := strings.ToLower(trim)
		if strings.HasPrefix(low, "description") || strings.HasPrefix(low, "**description**") {
			desc = strings.Trim(strings.TrimSpace(strings.SplitN(trim, ":", 2)[0]), "*")
			continue
		}
		if strings.Contains(low, "model-invocable: false") {
			modelInv = false
			continue
		}
		if strings.Contains(low, "user-invocable: false") {
			userInv = false
			continue
		}
		bodyLines = append(bodyLines, ln)
	}
	if desc == "" && len(bodyLines) > 0 {
		// Use the first nonempty line as the description.
		for _, ln := range bodyLines {
			if strings.TrimSpace(ln) != "" {
				desc = strings.TrimSpace(ln)
				break
			}
		}
	}
	return &Skill{
		Name:           name,
		Description:    desc,
		Path:           path,
		Rank:           rank,
		ModelInvocable: modelInv,
		UserInvocable:  userInv,
		Content:        strings.Join(bodyLines, "\n"),
	}
}

// Hash fingerprints skill names, ranks, and content lengths. Same-length
// content changes do not alter this lightweight fingerprint.
func (r *SkillRegistry) Hash() string {
	list := r.List()
	if len(list) == 0 {
		return "empty"
	}
	var sb strings.Builder
	for _, s := range list {
		sb.WriteString(fmt.Sprintf("%s@%d:%d:", s.Name, s.Rank, len(s.Content)))
	}
	// Combine ordered names and content lengths to detect changes.
	return sb.String()
}

// Stable skill catalog serialization and change-only injection (N04, D3).

// CatalogText renders available_skills in lexical name order with fixed field
// ordering and no randomness or timestamps, producing identical bytes across calls.
func (r *SkillRegistry) CatalogText() string {
	list := r.List() // Already in lexical name order.
	if len(list) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("<available_skills>\n")
	for _, s := range list {
		sb.WriteString("- ")
		sb.WriteString(s.Name)
		sb.WriteString(" (rank ")
		sb.WriteString(fmt.Sprint(s.Rank))
		sb.WriteString("): ")
		sb.WriteString(strings.TrimSpace(s.Description))
		sb.WriteString("\n")
	}
	sb.WriteString("</available_skills>")
	return sb.String()
}

// CatalogHash returns the stable SHA-256 hex digest of CatalogText.
func (r *SkillRegistry) CatalogHash() string {
	return catalogHashOf(r.CatalogText())
}

func catalogHashOf(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// Injector returns catalog content only when its hash changes. An unchanged
// catalog is injected once on the first turn, then again only after changes.
type Injector struct {
	registry *SkillRegistry
	lastHash string
	injects  int
}

// NewInjector creates a change-only injector.
func NewInjector(r *SkillRegistry) *Injector {
	return &Injector{registry: r, lastHash: ""}
}

// MaybeInject returns and counts content on the first call or a catalog hash change.
func (in *Injector) MaybeInject() (content string, injected bool) {
	hash := in.registry.CatalogHash()
	if hash == in.lastHash {
		return "", false
	}
	in.lastHash = hash
	in.injects++
	return in.registry.CatalogText(), true
}

// InjectCount returns the cumulative injection count.
func (in *Injector) InjectCount() int { return in.injects }

// Change watching (skills/change).

// Watch polls directories and invokes onChange when the hash changes, allowing
// skills/change events. Closing stop ends polling. This Windows-friendly backend
// can be replaced with fsnotify.
func (r *SkillRegistry) Watch(stop <-chan struct{}, interval time.Duration, onChange func(names []string)) {
	if interval <= 0 {
		interval = time.Second
	}
	r.Scan()
	cur := r.Hash()
	for {
		select {
		case <-stop:
			return
		case <-time.After(interval):
			r.Scan()
			newHash := r.Hash()
			if newHash != cur && onChange != nil {
				onChange(currentNames(r))
			}
			cur = newHash
		}
	}
}

// currentNames returns skill names in lexical order.
func currentNames(r *SkillRegistry) []string {
	list := r.List()
	names := make([]string, 0, len(list))
	for _, s := range list {
		names = append(names, s.Name)
	}
	return names
}

// ============================================================================
// tool-skill
// ============================================================================

// SkillTool implements skill(name).
type SkillTool struct {
	registry *SkillRegistry
}

// NewSkillTool creates a skill tool.
func NewSkillTool(r *SkillRegistry) *SkillTool { return &SkillTool{registry: r} }

// Resolve finds a skill and enforces invocation permissions. Missing skills
// return an error; model calls require ModelInvocable. The returned body is
// injected as an injected-context user message.
func (t *SkillTool) Resolve(name string, byModel bool) (string, error) {
	s, ok := t.registry.Get(name)
	if !ok {
		return "", fmt.Errorf("skills: skill %q not found", name)
	}
	if byModel && !s.ModelInvocable {
		return "", fmt.Errorf("skills: %q is not model-invocable", name)
	}
	if !byModel && !s.UserInvocable {
		return "", fmt.Errorf("skills: %q is not user-invocable", name)
	}
	return s.Content, nil
}

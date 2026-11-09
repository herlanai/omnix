// Package sysprompt registers and orders system-prompt sections, matching
// core/system-prompt. Sections have names, fixed orders, and text. Assembler joins
// them in ascending order: persona, policy, runtime context, plan policy, tool
// schemas, then skill catalog. Order constants avoid runtime-dependent ordering
// that could change model behavior.
package sysprompt

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/herlanai/omnix/pkg/tools"
)

// Fixed section-order constants matching core/system-prompt upstream.

// Section ordering values match upstream.
const (
	// SectionOrderPersona orders the persona.
	SectionOrderPersona = 100
	// SectionOrderPolicy orders general policy.
	SectionOrderPolicy = 200
	// SectionOrderRuntimeContext orders runtime context snapshots.
	SectionOrderRuntimeContext = 300
	// SectionOrderPlanPolicy orders policy injected only in plan mode.
	SectionOrderPlanPolicy = 500
	// SectionOrderToolsSchema orders tool JSON schemas.
	SectionOrderToolsSchema = 600
	// SectionOrderSkillCatalog orders the stable, change-only skill catalog (D3).
	SectionOrderSkillCatalog = 700
)

// ============================================================================
// Section & Assembler
// ============================================================================

// Section is a system-prompt block.
type Section struct {
	// Name uniquely identifies a section for registration, removal, and debugging.
	Name string `json:"name"`
	// Order is the fixed ordering value supplied from constants.
	Order int `json:"order"`
	// Text contains the block content.
	Text string `json:"text"`
}

// Assembler registers sections and assembles them in order.
type Assembler struct {
	mu       sync.RWMutex
	sections []*Section
}

// New creates an empty assembler.
func New() *Assembler {
	return &Assembler{sections: []*Section{}}
}

// Register replaces a same-name section using the supplied order.
func (a *Assembler) Register(name string, order int, text string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, s := range a.sections {
		if s.Name == name {
			s.Order = order
			s.Text = text
			return
		}
	}
	a.sections = append(a.sections, &Section{Name: name, Order: order, Text: text})
}

// Unregister removes a section.
func (a *Assembler) Unregister(name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i, s := range a.sections {
		if s.Name == name {
			a.sections = append(a.sections[:i], a.sections[i+1:]...)
			return
		}
	}
}

// Has checks section presence, including the plan:policy approval toggle.
func (a *Assembler) Has(name string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, s := range a.sections {
		if s.Name == name {
			return true
		}
	}
	return false
}

// Sections returns section copies sorted by ascending order.
func (a *Assembler) Sections() []*Section {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]*Section, len(a.sections))
	copy(out, a.sections)
	// Break equal-order ties by name for cross-machine determinism independent of registration order.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Order != out[j].Order {
			return out[i].Order < out[j].Order
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Assemble joins sections by ascending order with one blank line between them.
func (a *Assembler) Assemble() string {
	parts := make([]string, 0, len(a.sections))
	for _, s := range a.Sections() {
		if strings.TrimSpace(s.Text) == "" {
			continue
		}
		parts = append(parts, s.Text)
	}
	return strings.Join(parts, "\n\n")
}

// Tool schema injection.

// toolsSchemaEntry represents one injected tool schema.
type toolsSchemaEntry struct {
	Type     string        `json:"type"`
	Function toolsFunction `json:"function"`
}

// toolsFunction describes an OpenAI-style function.
type toolsFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// ToolsSectionText renders deterministic tool schemas in lexical name order
// using M48's deterministic parameter-schema marshaling.
func ToolsSectionText(toolList []*tools.Tool) (string, error) {
	if len(toolList) == 0 {
		return "", nil
	}
	// Sort names lexically for deterministic D3 ordering.
	sorted := append([]*tools.Tool(nil), toolList...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	entries := make([]toolsSchemaEntry, 0, len(sorted))
	for _, tl := range sorted {
		var params json.RawMessage
		if tl.Schema != nil {
			raw, err := tl.Schema.Marshal()
			if err != nil {
				return "", fmt.Errorf("sysprompt: marshal schema for tool %q: %w", tl.Name, err)
			}
			params = raw
		} else {
			params = json.RawMessage(`{"type":"object"}`)
		}
		entries = append(entries, toolsSchemaEntry{
			Type: "function",
			Function: toolsFunction{
				Name:        tl.Name,
				Description: tl.Description,
				Parameters:  params,
			},
		})
	}

	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return "", fmt.Errorf("sysprompt: marshal tools schema: %w", err)
	}
	return "# 可用工具（Tools Schema）：\n" + string(data), nil
}

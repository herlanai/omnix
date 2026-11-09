// Package presets implements agent presets, matching core/agent-preset.
// Each preset combines independent tools and prompts for an agent instance.
// The registry groups presets by standingKey: composeFrom overlays matching
// presets, recompose rebuilds the combination, and select chooses by key.
package presets

import (
	"sort"
	"sync"

	"github.com/herlanai/omnix/pkg/scope"
)

// StandingKey is a persistent preset key such as code, research, or default.
type StandingKey string

// AgentPreset combines an agent preset's settings.
type AgentPreset struct {
	// Key is the standing key.
	Key StandingKey
	// Tools contains the allowed tool names for this preset.
	Tools []string
	// Prompt contains a system-prompt fragment.
	Prompt string
}

// PresetRegistry layers host defaults and scoped overrides for agent presets.
type PresetRegistry struct {
	mu        sync.RWMutex
	hostLayer *scope.Layer[*AgentPreset]
	layers    *scope.ScopedLayers[*AgentPreset]
}

// NewPresetRegistry creates an empty registry.
func NewPresetRegistry() *PresetRegistry {
	host := scope.NewLayer[*AgentPreset](scope.Key("host"))
	return &PresetRegistry{
		hostLayer: host,
		layers:    scope.NewScopedLayers[*AgentPreset]().Push(host),
	}
}

// Mount installs a host-layer preset, replacing the same name while retaining others.
func (r *PresetRegistry) Mount(p *AgentPreset) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hostLayer.Register(string(p.Key), p, 0)
}

// ComposeFrom combines base presets using a tool union and concatenated prompts.
func ComposeFrom(key StandingKey, bases ...*AgentPreset) *AgentPreset {
	toolSet := map[string]struct{}{}
	var prompt string
	for _, b := range bases {
		if b == nil {
			continue
		}
		for _, t := range b.Tools {
			toolSet[t] = struct{}{}
		}
		if prompt != "" {
			prompt += "\n"
		}
		prompt += b.Prompt
	}
	tools := make([]string, 0, len(toolSet))
	for t := range toolSet {
		tools = append(tools, t)
	}
	sort.Strings(tools)
	return &AgentPreset{Key: key, Tools: tools, Prompt: prompt}
}

// Select resolves a key using nearest-scope-wins.
func (r *PresetRegistry) Select(key StandingKey) (*AgentPreset, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.layers.Get(string(key))
}

// StandingKeyFor returns the agent's standing key; currently it returns the supplied key.
func StandingKeyFor(key StandingKey) StandingKey {
	return key
}

// Recompose replaces a key's composition with a new preset.
func (r *PresetRegistry) Recompose(p *AgentPreset) {
	r.Mount(p)
}

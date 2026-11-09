// Layered tool restrictions follow nearest-scope-wins semantics (M24). Search from the nearest
// scope toward the host; the first layer mentioning a tool decides access. A local allow can
// exempt a host denial, local deny remains denied, and unmentioned tools are allowed. Presets
// and subagents use these masks to filter available tools.
package tools

import (
	"slices"
	"sync"
)

// Restriction is one layer of tool-access policy. Allow explicitly exempts a tool from outer
// denials; Deny explicitly rejects it.
type Restriction struct {
	// Allow contains explicitly exempted tool names.
	Allow []string `json:"allow,omitempty"`
	// Deny contains explicitly rejected tool names.
	Deny []string `json:"deny,omitempty"`
}

// RestrictionAllow creates a mask containing only explicit allowances.
func RestrictionAllow(names ...string) Restriction {
	return Restriction{Allow: names}
}

// RestrictionDeny creates a mask containing only explicit denials.
func RestrictionDeny(names ...string) Restriction {
	return Restriction{Deny: names}
}

// RestrictionLayer associates a policy mask with a scope name.
type RestrictionLayer struct {
	// Key identifies the scope for auditing, such as host, session:xxx, user:xxx, or subagent:xxx.
	Key string
	// Mask contains this layer's policy.
	Mask Restriction
}

// RestrictionSet stores an ordered stack of tool-access masks.
type RestrictionSet struct {
	mu     sync.RWMutex
	layers []RestrictionLayer // Index zero is the outermost host layer; later indexes have nearer-scope precedence.
}

// NewRestrictionSet creates an empty restriction stack.
func NewRestrictionSet() *RestrictionSet {
	return &RestrictionSet{}
}

// Host sets the outermost host policy.
func (rs *RestrictionSet) Host(mask Restriction) {
	rs.SetLayer("host", mask)
}

// Scope appends the nearest policy layer, taking precedence over all existing layers. Keys
// typically use session:xxx, user:xxx, or subagent:xxx.
func (rs *RestrictionSet) Scope(key string, mask Restriction) {
	rs.SetLayer(key, mask)
}

// SetLayer adds or updates a named layer, moving an existing layer to the nearest position.
func (rs *RestrictionSet) SetLayer(key string, mask Restriction) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	for i := range rs.layers {
		if rs.layers[i].Key == key {
			rs.layers[i].Mask = mask
			return
		}
	}
	rs.layers = append(rs.layers, RestrictionLayer{Key: key, Mask: mask})
}

// Allowed searches from the nearest scope toward the host. The first layer mentioning the tool
// determines access: Allow grants an exemption and Deny rejects. Tools absent from all layers
// are allowed.
func (rs *RestrictionSet) Allowed(tool string) bool {
	if rs == nil {
		return true
	}
	rs.mu.RLock()
	layers := make([]RestrictionLayer, len(rs.layers))
	copy(layers, rs.layers)
	rs.mu.RUnlock()
	// Walk backward from the nearest layer to the host.
	for i := len(layers) - 1; i >= 0; i-- {
		mask := layers[i].Mask
		if slices.Contains(mask.Allow, tool) {
			return true
		}
		if slices.Contains(mask.Deny, tool) {
			return false
		}
	}
	return true
}

// Denied negates Allowed for auditing and assertions.
func (rs *RestrictionSet) Denied(tool string) bool {
	return !rs.Allowed(tool)
}

// Filter returns allowed tools in their original order for preset filtering and subagent
// restrictions.
func (rs *RestrictionSet) Filter(tools []*Tool) []*Tool {
	if rs == nil {
		return tools
	}
	out := make([]*Tool, 0, len(tools))
	for _, t := range tools {
		if t != nil && rs.Allowed(t.Name) {
			out = append(out, t)
		}
	}
	return out
}

// Layers returns a shallow copy of the host-to-nearest stack for auditing and tests.
func (rs *RestrictionSet) Layers() []RestrictionLayer {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	out := make([]RestrictionLayer, len(rs.layers))
	copy(out, rs.layers)
	return out
}

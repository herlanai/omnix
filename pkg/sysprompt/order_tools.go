// Tool ordering follows upstream orderTools. Explicit names follow configuration;
// unlisted names are inserted alphabetically at ToolOrderRest. Configuration must
// contain exactly one rest marker, no duplicates, and no unknown tool names.
package sysprompt

import (
	"errors"
	"sort"
)

// ToolOrderRest is the reserved marker for remaining tools.
const ToolOrderRest = "<unlisted-tools>"

// OrderTools orders names according to configured.
func OrderTools(names, configured []string) ([]string, error) {
	if configured == nil {
		out := append([]string(nil), names...)
		sort.Strings(out)
		return out, nil
	}
	seen := map[string]bool{}
	for _, c := range configured {
		if c == ToolOrderRest {
			if seen[ToolOrderRest] {
				return nil, errors.New("rest marker appears more than once")
			}
		} else if seen[c] {
			return nil, errors.New("duplicate tool order entry: " + c)
		}
		seen[c] = true
	}
	if !seen[ToolOrderRest] {
		return nil, errors.New("tool order must contain the rest marker")
	}
	known := map[string]bool{}
	for _, n := range names {
		known[n] = true
	}
	for c := range seen {
		if c != ToolOrderRest && !known[c] {
			return nil, errors.New("tool order lists unknown tool: " + c)
		}
	}
	listed := map[string]bool{}
	var rest []string
	for _, n := range names {
		if seen[n] {
			listed[n] = true
		} else {
			rest = append(rest, n)
		}
	}
	sort.Strings(rest)
	out := make([]string, 0, len(names))
	for _, c := range configured {
		if c == ToolOrderRest {
			out = append(out, rest...)
		} else if listed[c] {
			out = append(out, c)
		}
	}
	return out, nil
}

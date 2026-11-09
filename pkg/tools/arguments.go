// Match the upstream agent loop's argument parsing: empty input becomes an empty object, valid
// JSON becomes its decoded value, and invalid JSON remains a string for the tool to handle.
// Malformed model output must not abort the whole turn at the dispatch boundary.
package tools

import "encoding/json"

// ParseArguments parses the model's raw argument string.
func ParseArguments(raw string) any {
	if raw == "" {
		return map[string]any{}
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return raw
	}
	return v
}

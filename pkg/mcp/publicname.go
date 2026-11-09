// Public MCP names match mcp-client/tools: stable identity is (server, raw) and
// the model-facing name is mcp__<server>__<raw>. Sanitization or truncation to the
// 64-character [A-Za-z0-9_-] limit appends an identity hash to prevent collisions.
package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
)

// MaxPublicNameLength limits function-name length.
const MaxPublicNameLength = 64

const hashLength = 12

var invalidNameChars = regexp.MustCompile(`[^A-Za-z0-9_-]`)

// PublicToolName deterministically derives a public name from server and raw names.
func PublicToolName(serverName, rawName string) string {
	joined := "mcp__" + serverName + "__" + rawName
	normalized := invalidNameChars.ReplaceAllString(joined, "_")
	if normalized == joined && len(normalized) <= MaxPublicNameLength {
		return normalized
	}
	sum := sha256.Sum256([]byte(serverName + "\x00" + rawName))
	hash := hex.EncodeToString(sum[:])[:hashLength]
	keep := MaxPublicNameLength - hashLength - 1
	if keep > len(normalized) {
		keep = len(normalized)
	}
	return normalized[:keep] + "_" + hash
}

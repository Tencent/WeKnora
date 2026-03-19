package tools

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"
)

// maxToolCallIDLen is the maximum allowed length for a tool call ID.
// Some providers return very long IDs; others return empty ones.
const maxToolCallIDLen = 64

// validIDChars matches only alphanumeric and common safe characters.
var validIDChars = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// NormalizeToolCallID normalizes a tool call ID for cross-provider compatibility.
//
// Different LLM providers return IDs in different formats:
//   - OpenAI: "call_abc123" (well-formed)
//   - Some providers: "" (empty)
//   - Some providers: very long UUIDs or random strings
//   - Some providers: IDs with special characters
//
// This function ensures IDs are:
//   - Non-empty (generates deterministic ID from tool name + index if empty)
//   - Within length limits (truncated with hash suffix if too long)
//   - Alphanumeric-safe (special characters replaced)
func NormalizeToolCallID(id string, toolName string, index int) string {
	id = strings.TrimSpace(id)

	// Generate deterministic ID if empty
	if id == "" {
		hash := sha256.Sum256([]byte(fmt.Sprintf("%s_%d", toolName, index)))
		id = fmt.Sprintf("call_%x", hash[:6])
	}

	// Remove unsafe characters
	id = validIDChars.ReplaceAllString(id, "_")

	// Truncate if too long, preserving uniqueness with hash suffix
	if len(id) > maxToolCallIDLen {
		hash := sha256.Sum256([]byte(id))
		suffix := fmt.Sprintf("_%x", hash[:4])
		id = id[:maxToolCallIDLen-len(suffix)] + suffix
	}

	return id
}

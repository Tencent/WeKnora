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
//   - Distinguished by the original ID when replacement or truncation is needed
func NormalizeToolCallID(id string, toolName string, index int) string {
	id = strings.TrimSpace(id)

	// Generate deterministic ID if empty
	if id == "" {
		hash := sha256.Sum256([]byte(fmt.Sprintf("%s_%d", toolName, index)))
		id = fmt.Sprintf("call_%x", hash[:6])
	}

	// Retain the original identity: replacement alone collapses distinct IDs
	// such as "call/a", "call?a" and the already-safe "call_a".
	originalID := id
	id = validIDChars.ReplaceAllString(id, "_")

	// Hash before sanitization, including for long IDs whose sanitized forms
	// are identical. Already-safe IDs within the limit remain unchanged.
	if id != originalID || len(id) > maxToolCallIDLen {
		hash := sha256.Sum256([]byte(originalID))
		suffix := fmt.Sprintf("_%x", hash[:4])
		id = id[:min(len(id), maxToolCallIDLen-len(suffix))] + suffix
	}

	return id
}

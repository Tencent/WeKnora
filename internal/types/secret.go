// Package types — shared secret redaction helpers.
//
// VectorStore connection configs and tenant KV configs (web search, parser
// engine, storage engine) use the redacted-placeholder round-trip pattern:
//
//   - GET responses replace set secrets with RedactedSecretPlaceholder (or
//     empty string when unset).
//   - PUT requests treat "", or RedactedSecretPlaceholder as "preserve
//     existing"; any other value replaces the stored secret.
//
// MCP / Model / WebSearch provider / DataSource credentials use a dedicated
// /credentials subresource instead; see internal/handler/dto/mcp.go.
package types

// RedactedSecretPlaceholder is the fixed value returned in API responses
// whenever a sensitive field is set but withheld from the client.
const RedactedSecretPlaceholder = "***"

// IsRedactedOrEmpty reports whether s should be treated as "no change
// requested" in an Update request.
func IsRedactedOrEmpty(s string) bool {
	return s == "" || s == RedactedSecretPlaceholder
}

// PreserveIfRedacted returns existing when incoming is empty or the redacted
// placeholder, otherwise returns incoming.
func PreserveIfRedacted(incoming, existing string) string {
	if IsRedactedOrEmpty(incoming) {
		return existing
	}
	return incoming
}

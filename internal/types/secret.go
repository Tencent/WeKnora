// Package types — shared secret redaction helpers for the "write-only secrets"
// pattern applied across MCP services, Models, WebSearch providers,
// DataSources, and VectorStores.
//
// The contract is:
//
//   - API responses replace sensitive values with RedactedSecretPlaceholder
//     when the value is set, and leave it as the empty string when it is not.
//     Clients can therefore distinguish "set (hidden)" from "not set" without
//     ever seeing the secret itself.
//
//   - API Update requests treat an absent field, the empty string, or the
//     RedactedSecretPlaceholder as "no change requested" (preserve), and any
//     other value as an explicit replacement.
//
//   - Explicit removal of a stored secret is expressed with a dedicated
//     write-only boolean flag on the request DTO (e.g. ClearAPIKey) rather
//     than by sending an empty string, because the empty string is already
//     reserved for "preserve".
package types

// RedactedSecretPlaceholder is the fixed value returned in API responses
// whenever a sensitive field is set but withheld from the client. The empty
// string ("") is reserved for the orthogonal "not set" state, which lets the
// frontend distinguish the two states without an extra boolean field.
//
// The value matches the placeholder already used by
// ConnectionConfig.MaskSensitiveFields on VectorStore responses; this package
// promotes it to a shared constant so every resource agrees on the same
// sentinel.
const RedactedSecretPlaceholder = "***"

// IsRedactedOrEmpty reports whether s should be treated as "no change
// requested" in an Update* request. It returns true for:
//
//   - "" — the field was absent from the client payload or explicitly cleared
//     on the form without using the dedicated Clear* flag
//   - RedactedSecretPlaceholder — the client echoed back the value it received
//     in a GET response (for example, a legacy frontend that pre-fills the
//     edit form with the redacted value)
//
// Any other value is an explicit replacement and must be persisted.
func IsRedactedOrEmpty(s string) bool {
	return s == "" || s == RedactedSecretPlaceholder
}

// PreserveIfRedacted returns existing when incoming is empty or the redacted
// placeholder, otherwise returns incoming. Call this from every Update*
// service that accepts secret fields in its request DTO to keep the preserve
// semantics identical across resources.
func PreserveIfRedacted(incoming, existing string) string {
	if IsRedactedOrEmpty(incoming) {
		return existing
	}
	return incoming
}

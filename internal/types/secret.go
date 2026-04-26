package types

// RedactedSecretPlaceholder is the fixed placeholder returned in API responses
// whenever a sensitive field (API key, token, password, etc.) is set.
// This constant value is intentionally short and unambiguous so that callers
// can reliably detect it with IsRedactedOrEmpty.
const RedactedSecretPlaceholder = "***"

// IsRedactedOrEmpty reports whether s is either the empty string or the
// canonical redacted placeholder. Use this to decide whether an incoming
// update request field should be treated as "no change".
func IsRedactedOrEmpty(s string) bool {
	return s == "" || s == RedactedSecretPlaceholder
}

// PreserveIfRedacted returns existing when incoming is empty or the redacted
// placeholder, otherwise it returns incoming. This implements the "preserve by
// default, replace on explicit input" pattern for secret fields in Update
// handlers.
//
// Usage in a service Update method:
//
//	existing.AuthConfig.APIKey = PreserveIfRedacted(req.AuthConfig.APIKey, existing.AuthConfig.APIKey)
func PreserveIfRedacted(incoming, existing string) string {
	if IsRedactedOrEmpty(incoming) {
		return existing
	}
	return incoming
}

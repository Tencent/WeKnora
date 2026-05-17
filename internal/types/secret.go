// Package types — shared secret-handling helpers.
//
// Historically this file also held PreserveIfRedacted / IsRedactedOrEmpty
// to support the old "echo redacted placeholder back, server merges on
// Update" pattern shared by MCP / Model / WebSearch / DataSource. Those
// resources have since moved to the credential-resource pattern (a
// dedicated /credentials subresource), so the merge helpers were removed.
//
// The placeholder constant survives because VectorStore connection configs
// still inline-redact a Password / APIKey on response (see
// types/vectorstore.go → ConnectionConfig.MaskSensitiveFields); migrating
// VectorStore to the credential-resource pattern is left for a future PR
// because it would require introducing a separate connection record (the
// secret currently lives inline on the knowledge base config).
package types

// RedactedSecretPlaceholder is the fixed value returned in API responses
// whenever a sensitive field is set but withheld from the client. Currently
// used only by VectorStore connection responses. New code should NOT
// introduce redacted-placeholder semantics — model the credential as a
// subresource and omit it from the main response shape instead (see
// internal/handler/dto/mcp.go for the template).
const RedactedSecretPlaceholder = "***"

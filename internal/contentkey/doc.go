// Package contentkey defines deterministic, versioned identities for content
// produced by the ingestion pipeline.
//
// Normalization is deliberately conservative. It removes representation-only
// differences in line endings, Unicode composition, and outer whitespace while
// preserving internal Markdown, code, table, spacing, and newline semantics.
//
// The package only computes identities. It does not persist them, replace
// database row IDs, reconcile chunks, or suppress provider computation.
package contentkey

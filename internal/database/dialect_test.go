package database

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// CaseInsensitiveLike returns a SQL fragment that performs a
// case-insensitive pattern match on the given column, shaped for the
// active database dialect.
//
// The fragment choice encodes a real trade-off (see ADR 0001 S2):
// PostgreSQL has pg_trgm GIN indexes on the affected columns that
// ILIKE can hit; switching PG to LOWER() LIKE LOWER() would need
// expression-index rebuilds and could regress search performance. So
// postgres keeps ILIKE, while every other dialect uses the portable
// LOWER() LIKE LOWER() form.
//
// These tests are the contract for that helper. They are written
// before the helper exists (TDD red phase) and will not compile until
// internal/database/dialect.go is created.

func TestCaseInsensitiveLike_Postgres_PreservesILIKE(t *testing.T) {
	got := CaseInsensitiveLike("postgres", "title", "?")
	assert.Equal(t, "title ILIKE ?", got,
		"postgres must keep ILIKE so the existing pg_trgm GIN index stays usable")
}

func TestCaseInsensitiveLike_MySQL_EmitsLowerLike(t *testing.T) {
	got := CaseInsensitiveLike("mysql", "title", "?")
	assert.Equal(t, "LOWER(title) LIKE LOWER(?)", got,
		"mysql has no ILIKE; LOWER() LIKE LOWER() is the case-insensitive equivalent")
}

func TestCaseInsensitiveLike_SQLite_EmitsLowerLike(t *testing.T) {
	got := CaseInsensitiveLike("sqlite", "title", "?")
	assert.Equal(t, "LOWER(title) LIKE LOWER(?)", got)
}

func TestCaseInsensitiveLike_QualifiedColumnIsPreserved(t *testing.T) {
	got := CaseInsensitiveLike("mysql", "messages.content", "?")
	assert.Equal(t, "LOWER(messages.content) LIKE LOWER(?)", got,
		"qualified columns (table.column) must pass through verbatim")
}

// Unknown dialects fall back to the portable form. A future dialect
// that *does* have ILIKE would opt in explicitly; until then the
// safe default is the one every SQL engine understands.
func TestCaseInsensitiveLike_UnknownDialectFallsBackToLowerLike(t *testing.T) {
	got := CaseInsensitiveLike("sqlserver", "title", "?")
	assert.Equal(t, "LOWER(title) LIKE LOWER(?)", got,
		"unknown dialects must fall back to the portable LOWER/LIKE form, not ILIKE")
}

// JSONPathExpr returns a SQL fragment that extracts a scalar value from
// a JSON column. The fragment must match the dialect's native syntax so
// the caller's `= ?` comparison works; the bare-key postgres form
// errors on MySQL (1064 near '? = ?'), and the MySQL $.path form is not
// valid postgres syntax.
//
// JSONPathExpr now returns (string, error): it validates the key against
// [a-zA-Z0-9_-] first and returns an error on invalid input, rather than
// silently stripping characters. Silently stripping turned "a.b" into
// "ab", which queried a different key than the caller intended - a
// subtle correctness bug. Failing fast surfaces the bad caller instead.

func TestJSONPathExpr_Postgres_UsesArrow(t *testing.T) {
	got, err := JSONPathExpr("postgres", "metadata", "external_id")
	assert.NoError(t, err)
	assert.Equal(t, "metadata ->> 'external_id'", got)
}

func TestJSONPathExpr_MySQL_UsesDollarPath(t *testing.T) {
	got, err := JSONPathExpr("mysql", "metadata", "external_id")
	assert.NoError(t, err)
	assert.Equal(t, "metadata ->> '$.\"external_id\"'", got,
		"mysql requires the $.key JSON path prefix; bare-key form errors with 1064")
}

func TestJSONPathExpr_SQLite_UsesJsonExtract(t *testing.T) {
	got, err := JSONPathExpr("sqlite", "metadata", "external_id")
	assert.NoError(t, err)
	assert.Equal(t, "json_extract(metadata, '$.\"external_id\"')", got)
}

func TestJSONPathExpr_QualifiedColumnPreserved(t *testing.T) {
	got, err := JSONPathExpr("mysql", "kb.metadata", "external_id")
	assert.NoError(t, err)
	assert.Equal(t, "kb.metadata ->> '$.\"external_id\"'", got)
}

func TestJSONPathExpr_HyphenatedKeyIsQuoted(t *testing.T) {
	mysqlExpr, err := JSONPathExpr("mysql", "metadata", "source-resource-id")
	assert.NoError(t, err)
	assert.Equal(t, "metadata ->> '$.\"source-resource-id\"'", mysqlExpr)

	sqliteExpr, err := JSONPathExpr("sqlite", "metadata", "source-resource-id")
	assert.NoError(t, err)
	assert.Equal(t, "json_extract(metadata, '$.\"source-resource-id\"')", sqliteExpr)
}

// validateJSONPathKey must reject anything outside [a-zA-Z0-9_-] or
// empty, so a caller-controlled key cannot inject a JSON path
// metacharacter (dot, bracket, quote, wildcard) AND cannot be silently
// rewritten into a different key than the caller intended.
func TestValidateJSONPathKey(t *testing.T) {
	valid := []string{"external_id", "datasource_id", "source-resource-id", "key1", "A_B-C", "abc123"}
	for _, k := range valid {
		if err := validateJSONPathKey(k); err != nil {
			t.Errorf("validateJSONPathKey(%q) returned unexpected error: %v", k, err)
		}
	}
	invalid := []string{
		"",     // empty
		"a.b",  // dot (was silently stripped to "ab" - the bug this guards)
		"a[b]", // brackets
		`a"b`,  // quote
		"a*b",  // wildcard
		"a b",  // space
		"a$b",  // dollar
		"中文",   // non-ascii
	}
	for _, k := range invalid {
		if err := validateJSONPathKey(k); err == nil {
			t.Errorf("validateJSONPathKey(%q) should return an error, got nil", k)
		}
	}
}

// JSONPathExpr must propagate the validation error for an invalid key
// rather than silently stripping and returning a fragment that targets
// the wrong JSON path.
func TestJSONPathExpr_InvalidKeyReturnsError(t *testing.T) {
	got, err := JSONPathExpr("mysql", "metadata", "a.b")
	assert.Error(t, err, "dotted key must error, not silently strip to 'ab'")
	assert.Equal(t, "", got, "no fragment should be returned on validation error")
}

func TestJSONPathExpr_EmptyKeyReturnsError(t *testing.T) {
	got, err := JSONPathExpr("mysql", "metadata", "")
	assert.Error(t, err, "empty key must error")
	assert.Equal(t, "", got)
}

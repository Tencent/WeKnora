package database

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCaseInsensitiveMatch(t *testing.T) {
	assert.Equal(t, "title ILIKE ?", CaseInsensitiveMatch("postgres", "title"))
	assert.Equal(t, "LOWER(title) LIKE LOWER(?)", CaseInsensitiveMatch("mysql", "title"))
	assert.Equal(t, "LOWER(title) LIKE LOWER(?)", CaseInsensitiveMatch("sqlite", "title"))
}

func TestJSONScalarExpression(t *testing.T) {
	pg, err := JSONScalarExpression("postgres", "metadata", "external_id")
	require.NoError(t, err)
	assert.Equal(t, "metadata ->> 'external_id'", pg)

	my, err := JSONScalarExpression("mysql", "metadata", "source-resource-id")
	require.NoError(t, err)
	assert.Equal(t, "JSON_UNQUOTE(JSON_EXTRACT(metadata, '$.\"source-resource-id\"'))", my)

	sq, err := JSONScalarExpression("sqlite", "metadata", "external_id")
	require.NoError(t, err)
	assert.Equal(t, "json_extract(metadata, '$.\"external_id\"')", sq)
}

func TestJSONScalarExpressionRejectsUnsafeKey(t *testing.T) {
	for _, key := range []string{"", "a.b", "a[b]", `a"b`, "a b", "a*b"} {
		_, err := JSONScalarExpression("mysql", "metadata", key)
		assert.Error(t, err, key)
	}
}

func TestDialectCapabilities(t *testing.T) {
	assert.True(t, SupportsRowLock("postgres"))
	assert.True(t, SupportsRowLock("mysql"))
	assert.False(t, SupportsRowLock("sqlite"))

	assert.Equal(t, "REGEXP_LIKE(title, ?, 'i')", CaseInsensitiveRegex("mysql", "title"))
	assert.Equal(t, "title ~* ?", CaseInsensitiveRegex("postgres", "title"))
	assert.Equal(t, "title REGEXP ?", CaseInsensitiveRegex("sqlite", "title"))
}

func TestJSONArrayExpressions(t *testing.T) {
	assert.Equal(t, "COALESCE(JSON_LENGTH(items), 0)", JSONArrayLength("mysql", "items"))
	assert.Equal(t, "COALESCE(jsonb_array_length(items), 0)", JSONArrayLength("postgres", "items"))
	assert.Equal(t, "COALESCE(json_array_length(items), 0)", JSONArrayLength("sqlite", "items"))

	assert.Equal(t, "JSON_CONTAINS(items, ?)", JSONArrayContains("mysql", "items"))
	assert.Equal(t, "items @> ?::jsonb", JSONArrayContains("postgres", "items"))
	assert.Equal(t, "EXISTS (SELECT 1 FROM json_each(items) WHERE value = ?)", JSONArrayContains("sqlite", "items"))
}

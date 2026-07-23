package repository

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestWikiDialectHelpers covers the SQL fragment shape for each
// dialect-aware wiki helper. These are pure-string functions, so the
// test asserts the exact output per dialect. The behavioural MySQL
// round-trip is covered by TestMySQLDatabaseIntegration.

func TestWikiJSONArrayLength(t *testing.T) {
	tests := []struct {
		dialect, want string
	}{
		{"postgres", "COALESCE(jsonb_array_length(in_links), 0)"},
		{"mysql", "COALESCE(JSON_LENGTH(in_links), 0)"},
		{"sqlite", "COALESCE(json_array_length(in_links), 0)"},
		{"unknown", "COALESCE(json_array_length(in_links), 0)"},
	}
	for _, tt := range tests {
		t.Run(tt.dialect, func(t *testing.T) {
			assert.Equal(t, tt.want, wikiJSONArrayLength(tt.dialect, "in_links"))
		})
	}
}

func TestWikiJSONContains(t *testing.T) {
	tests := []struct {
		dialect, want string
	}{
		{"postgres", "source_refs @> ?::jsonb"},
		{"mysql", "JSON_CONTAINS(source_refs, ?)"},
		{"sqlite", "EXISTS (SELECT 1 FROM json_each(source_refs) WHERE value = ?)"},
		{"unknown", "EXISTS (SELECT 1 FROM json_each(source_refs) WHERE value = ?)"},
	}
	for _, tt := range tests {
		t.Run(tt.dialect, func(t *testing.T) {
			assert.Equal(t, tt.want, wikiJSONContains(tt.dialect, "source_refs"))
		})
	}
}

func TestWikiJSONContainsArg(t *testing.T) {
	// Postgres / MySQL take the JSON-encoded array.
	assert.Equal(t, `["abc"]`, wikiJSONContainsArg("postgres", `["abc"]`, "abc"))
	assert.Equal(t, `["abc"]`, wikiJSONContainsArg("mysql", `["abc"]`, "abc"))
	// SQLite takes the bare scalar.
	assert.Equal(t, "abc", wikiJSONContainsArg("sqlite", `["abc"]`, "abc"))
}

func TestWikiJSONAsText(t *testing.T) {
	assert.Equal(t, "source_refs::text", wikiJSONAsText("postgres", "source_refs"))
	assert.Equal(t, "CAST(source_refs AS CHAR)", wikiJSONAsText("mysql", "source_refs"))
	assert.Equal(t, "CAST(source_refs AS TEXT)", wikiJSONAsText("sqlite", "source_refs"))
}

func TestWikiJSONEqual(t *testing.T) {
	assert.Equal(t, "category_path::jsonb = ?::jsonb", wikiJSONEqual("postgres", "category_path"))
	assert.Equal(t, "category_path = CAST(? AS JSON)", wikiJSONEqual("mysql", "category_path"))
	assert.Equal(t, "category_path = ?", wikiJSONEqual("sqlite", "category_path"))
}

func TestWikiCaseInsensitiveRegex(t *testing.T) {
	assert.Equal(t, "title ~* ?", wikiCaseInsensitiveRegex("postgres", "title", "?"))
	// MySQL uses REGEXP_LIKE with the 'i' match-type flag so the
	// case-insensitivity is explicit and the bound pattern is treated as a
	// real regex (the bare `col REGEXP ?` form is case-sensitive under the
	// default utf8mb4_general_ci collation and has inconsistent behaviour
	// across MySQL point releases).
	assert.Equal(t, "REGEXP_LIKE(title, ?, 'i')", wikiCaseInsensitiveRegex("mysql", "title", "?"))
	// SQLite falls back to LIKE (no built-in REGEXP). This is a substring
	// approximation, NOT a superset of regex - it has different matching
	// semantics (e.g. no alternation, no anchors).
	got := wikiCaseInsensitiveRegex("sqlite", "title", "?")
	assert.True(t, strings.HasPrefix(got, "title LIKE"), "sqlite should fall back to LIKE; got %s", got)
}

func TestWikiSimilarityRank(t *testing.T) {
	pg := wikiSimilarityRank("postgres", "title", "?")
	assert.True(t, strings.HasPrefix(pg, "similarity(lower(title)"))
	my := wikiSimilarityRank("mysql", "title", "?")
	assert.Contains(t, my, "LOWER(title) LIKE CONCAT")
	// SQLite / unknown -> LIKE-based.
	got := wikiSimilarityRank("sqlite", "title", "?")
	assert.Contains(t, got, "LOWER(title) LIKE")
}

func TestWikiSimilarityThreshold(t *testing.T) {
	assert.Contains(t, wikiSimilarityThreshold("postgres", "title", "?"), "lower(title) % ?")
	assert.Contains(t, wikiSimilarityThreshold("mysql", "title", "?"), "LOWER(title) LIKE CONCAT")
	assert.Contains(t, wikiSimilarityThreshold("sqlite", "title", "?"), "LOWER(title) LIKE")
}

func TestWikiTextLength(t *testing.T) {
	assert.Equal(t, "CHAR_LENGTH(title)", wikiTextLength("mysql", "title"))
	assert.Equal(t, "LENGTH(title)", wikiTextLength("postgres", "title"))
	assert.Equal(t, "LENGTH(title)", wikiTextLength("sqlite", "title"))
}

func TestWikiFullTextSearch(t *testing.T) {
	frag, n := wikiFullTextSearch("postgres")
	assert.Equal(t, 2, n)
	assert.Contains(t, frag, "to_tsvector")
	assert.Contains(t, frag, "plainto_tsquery")

	frag, n = wikiFullTextSearch("mysql")
	assert.Equal(t, 3, n)
	assert.Contains(t, frag, "LOWER(title)")
	assert.Contains(t, frag, "LOWER(content)")
	assert.Contains(t, frag, "LOWER(aliases)")

	// SQLite: same shape as MySQL (multi-column LIKE).
	frag, n = wikiFullTextSearch("sqlite")
	assert.Equal(t, 3, n)
	assert.Contains(t, frag, "LOWER(title)")
}

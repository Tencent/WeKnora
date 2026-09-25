package sqlite

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSanitizeFTS5QueryScopesToContent(t *testing.T) {
	t.Parallel()
	assert.Equal(t, `content : ("2023")`, sanitizeFTS5Query("2023"))
	// Quotes never reach an FTS string unescaped.
	assert.Equal(t, `content : ("say" OR "hi")`, sanitizeFTS5Query(`say"hi"`))
	// A lone Han character matches bigrams that start with it.
	assert.Equal(t, `content : ("A" OR "股"*)`, sanitizeFTS5Query("A股"))
	assert.Equal(t, "", sanitizeFTS5Query("   "))
}

// Only the content column is searched: IDs indexed alongside it must not
// match a query.
func TestKeywordsRetrieveIgnoresIDColumns(t *testing.T) {
	repository := newSQLiteRetrieverTestRepository(t)
	if !repository.db.Migrator().HasTable("lite_embeddings_fts") {
		t.Skip("FTS5 unavailable (build without sqlite_fts5)")
	}
	info := sqliteTestIndex("chunk-2023", "kb", "knowledge", "", true)
	info.Content = "quarterly report"
	saveSQLiteTestVector(t, repository, info, []float32{1, 0})

	assert.Empty(t, keywordSearch(t, repository, "2023"))
	assert.Len(t, keywordSearch(t, repository, "quarterly"), 1)
}

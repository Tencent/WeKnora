package sqlite

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// Tokenization can remove a non-empty query entirely. Such queries have no
// keyword matches, rather than an invalid empty FTS5 MATCH expression.
func TestKeywordsRetrieveWithoutSearchableTerms(t *testing.T) {
	repository := newSQLiteRetrieverTestRepository(t)
	if !repository.db.Migrator().HasTable("lite_embeddings_fts") {
		t.Skip("FTS5 unavailable (build without sqlite_fts5)")
	}
	info := sqliteTestIndex("chunk", "kb", "knowledge", "", true)
	info.Content = "quarterly report 中文"
	saveSQLiteTestVector(t, repository, info, []float32{1, 0})
	for _, query := range []string{"", "   ", `"`, "...", "!!!", "*", "-", "😃", "，。！？"} {
		t.Run(query, func(t *testing.T) {
			results, err := repository.Retrieve(context.Background(), types.RetrieveParams{
				Query: query, TopK: 10, RetrieverType: types.KeywordsRetrieverType,
			})
			require.NoError(t, err)
			for _, result := range results {
				assert.Empty(t, result.Results)
			}
		})
	}
	assert.Len(t, keywordSearch(t, repository, "report!!!"), 1)
	assert.Len(t, keywordSearch(t, repository, "中文"), 1)
}

func TestRetrieveWithoutKeywordTermsStillReturnsVectors(t *testing.T) {
	repository := newSQLiteRetrieverTestRepository(t)
	if !repository.db.Migrator().HasTable("lite_embeddings_fts") {
		t.Skip("FTS5 unavailable (build without sqlite_fts5)")
	}
	info := sqliteTestIndex("chunk", "kb", "knowledge", "", true)
	saveSQLiteTestVector(t, repository, info, []float32{1, 0})
	results, err := repository.Retrieve(context.Background(), types.RetrieveParams{
		Query: "😃", Embedding: []float32{1, 0}, TopK: 10,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Len(t, results[0].Results, 1)
	assert.Equal(t, info.ChunkID, results[0].Results[0].ChunkID)
}

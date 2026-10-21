package postgres

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/database"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestKeywordTextPreservesChineseFrequency(t *testing.T) {
	text := keywordText("自动售后回复，自动售后回复")
	require.GreaterOrEqual(t, strings.Count(text, "售后"), 2)
	require.Contains(t, strings.Fields(text), "售后")
	require.Equal(t, "AI workflow 2026", keywordText("AI workflow 2026"))
}

// This exercises the real pg_textsearch 1.3.1 dialect, not mocked SQL results.
func TestTextSearchRetrievalAndBackfill(t *testing.T) {
	dsn := os.Getenv("WEKNORA_TEXTSEARCH_TEST_DSN")
	if dsn == "" {
		t.Skip("set WEKNORA_TEXTSEARCH_TEST_DSN to run the pg_textsearch integration test")
	}
	t.Setenv("POSTGRES_SEARCH_EXTENSION", "pg_textsearch")
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	options := u.Query()
	options.Set("options", options.Get("options")+
		" -c app.skip_embedding=false -c app.postgres_search_extension=pg_textsearch")
	u.RawQuery = options.Encode()
	dsn = u.String()
	db, err := gorm.Open(pgdriver.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	var name string
	require.NoError(t, sqlDB.QueryRow("SELECT current_database()").Scan(&name))
	require.Equal(t, "weknora_textsearch_test", name)
	// Migrations are relative to the module root.
	t.Chdir("../../../../..")
	require.NoError(t, database.RunMigrations(dsn))
	require.NoError(t, db.Exec("DELETE FROM embeddings").Error)
	repo := &pgRepository{db: db, textSearch: true}
	ctx := context.Background()
	content := "渠道部门已经落地自动售后回复项目。"
	var indices []*types.IndexInfo
	vectors := map[string][]float32{}
	for _, row := range []struct{ source, kb, document, tag, body string }{
		{"wanted", "kb-a", "doc-a", "tag-a", content},
		{"other-kb", "kb-b", "doc-a", "tag-a", "售后 售后 售后"},
		{"other-doc", "kb-a", "doc-b", "tag-a", "售后 售后"},
		{"other-tag", "kb-a", "doc-a", "tag-b", "售后 售后"},
		{"disabled", "kb-a", "doc-a", "tag-a", "售后 售后 售后"},
	} {
		indices = append(indices, &types.IndexInfo{
			SourceID: row.source, ChunkID: row.source, KnowledgeBaseID: row.kb,
			KnowledgeID: row.document, TagID: row.tag, Content: row.body, IsEnabled: true,
		})
		vectors[row.source] = []float32{1, 0, 0}
	}
	require.NoError(t, repo.Save(ctx, indices[0], map[string]any{"embedding": vectors}))
	require.NoError(t, repo.BatchSave(ctx, indices[1:], map[string]any{"embedding": vectors}))
	require.NoError(t, repo.BatchUpdateChunkEnabledStatus(ctx, map[string]bool{"disabled": false}))
	params := types.RetrieveParams{
		Query: "售后", TopK: 1,
		KnowledgeBaseIDs: []string{"kb-a"}, KnowledgeIDs: []string{"doc-a"}, TagIDs: []string{"tag-a"},
	}
	assertWanted := func() {
		result, err := repo.KeywordsRetrieve(ctx, params)
		require.NoError(t, err)
		require.Len(t, result, 1)
		require.Len(t, result[0].Results, 1)
		require.Equal(t, "wanted", result[0].Results[0].SourceID)
		require.Equal(t, content, result[0].Results[0].Content)
		require.Greater(t, result[0].Results[0].Score, float64(0))
	}
	assertWanted()
	require.NoError(t, repo.Save(ctx, &types.IndexInfo{
		SourceID: "keyword-only", ChunkID: "keyword-only", KnowledgeBaseID: "kb-only",
		Content: "售后关键词检索不需要重新生成向量", IsEnabled: true,
	}, nil))
	var noVector bool
	require.NoError(t, sqlDB.QueryRow(
		"SELECT embedding IS NULL FROM embeddings WHERE source_id = 'keyword-only'").Scan(&noVector))
	require.True(t, noVector)
	only, err := repo.KeywordsRetrieve(ctx, types.RetrieveParams{
		Query: "售后", TopK: 1, KnowledgeBaseIDs: []string{"kb-only"},
	})
	require.NoError(t, err)
	require.Len(t, only[0].Results, 1)
	require.NoError(t, repo.CopyIndices(ctx, "kb-only", map[string]string{"": "copy-only-doc"},
		map[string]string{"keyword-only": "copied-only"}, "kb-copy-only", 0, "document"))
	only, err = repo.KeywordsRetrieve(ctx, types.RetrieveParams{
		Query: "售后", TopK: 1, KnowledgeBaseIDs: []string{"kb-copy-only"},
	})
	require.NoError(t, err)
	require.Len(t, only[0].Results, 1)
	require.Equal(t, "copied-only", only[0].Results[0].SourceID)
	require.NoError(t, db.Model(&pgVector{}).Where("source_id = ?", "wanted").Update("search_content", nil).Error)
	require.NoError(t, BackfillTextSearch(ctx, db))
	require.NoError(t, BackfillTextSearch(ctx, db))
	assertWanted()
	params.Query = "unmatched_unique_word"
	result, err := repo.KeywordsRetrieve(ctx, params)
	require.NoError(t, err)
	require.Empty(t, result[0].Results)
	params.Query = "; '--"
	result, err = repo.KeywordsRetrieve(ctx, params)
	require.NoError(t, err)
	require.Empty(t, result)

	require.NoError(t, repo.CopyIndices(ctx, "kb-a", map[string]string{"doc-a": "copy-doc"},
		map[string]string{"wanted": "copied"}, "kb-copy", 3, "document"))
	params = types.RetrieveParams{Query: "售后", TopK: 3, KnowledgeBaseIDs: []string{"kb-copy"}}
	result, err = repo.KeywordsRetrieve(ctx, params)
	require.NoError(t, err)
	require.Len(t, result[0].Results, 1)
	require.Equal(t, "copied", result[0].Results[0].SourceID)
	params.Embedding = []float32{1, 0, 0}
	result, err = repo.VectorRetrieve(ctx, params)
	require.NoError(t, err)
	require.Len(t, result[0].Results, 1)
	require.Equal(t, content, result[0].Results[0].Content)
	require.NoError(t, repo.DeleteByChunkIDList(ctx, []string{"copied"}, 3, "document"))
	result, err = repo.KeywordsRetrieve(ctx, params)
	require.NoError(t, err)
	require.Empty(t, result[0].Results)
	require.NoError(t, db.Exec("DROP INDEX embeddings_textsearch_idx").Error)
	require.ErrorContains(t, BackfillTextSearch(ctx, db), "valid embeddings_textsearch_idx")
	require.NoError(t, db.Exec("CREATE INDEX embeddings_textsearch_idx ON embeddings "+
		"USING bm25 (search_content) WITH (text_config='simple')").Error)
}

func TestParadeDBRemainsUsable(t *testing.T) {
	dsn := os.Getenv("WEKNORA_PARADEDB_TEST_DSN")
	if dsn == "" {
		t.Skip("set WEKNORA_PARADEDB_TEST_DSN to run the legacy backend regression test")
	}
	t.Setenv("POSTGRES_SEARCH_EXTENSION", "pg_search")
	db, err := gorm.Open(pgdriver.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	var name string
	require.NoError(t, sqlDB.QueryRow("SELECT current_database()").Scan(&name))
	require.Equal(t, "weknora_082_upgrade_test", name)
	t.Chdir("../../../../..")
	require.NoError(t, database.RunMigrations(dsn))
	repo := &pgRepository{db: db}
	ctx := context.Background()
	require.NoError(t, db.Where("source_id = ?", "paradedb-regression").Delete(&pgVector{}).Error)
	require.NoError(t, repo.Save(ctx, &types.IndexInfo{
		SourceID: "paradedb-regression", Content: "渠道部门自动售后回复", KnowledgeBaseID: "legacy-test", IsEnabled: true,
	}, map[string]any{"embedding": map[string][]float32{"paradedb-regression": {1, 0, 0}}}))
	result, err := repo.KeywordsRetrieve(ctx, types.RetrieveParams{
		Query: "售后", TopK: 3, KnowledgeBaseIDs: []string{"legacy-test"},
	})
	require.NoError(t, err)
	require.Len(t, result[0].Results, 1)
	require.Equal(t, "paradedb-regression", result[0].Results[0].SourceID)
}

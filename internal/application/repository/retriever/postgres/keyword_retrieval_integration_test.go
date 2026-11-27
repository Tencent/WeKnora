package postgres

import (
	"context"
	"os"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// A real BM25 index is needed to catch ParadeDB rejecting the scored query.
func TestKeywordsRetrieveWithQuestionGenerationOff(t *testing.T) {
	dsn := os.Getenv("WEKNORA_RETRIEVER_TEST_PARADEDB_DSN")
	if dsn == "" {
		t.Skip("set WEKNORA_RETRIEVER_TEST_PARADEDB_DSN to a disposable database with pg_search installed")
	}
	db, err := gorm.Open(gormpostgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	tx := db.Begin()
	require.NoError(t, tx.Error)
	t.Cleanup(func() { require.NoError(t, tx.Rollback().Error) })
	require.NoError(t, tx.Exec(`CREATE SCHEMA keyword_recall_test`).Error)
	require.NoError(t, tx.Exec(`SET LOCAL search_path = keyword_recall_test, public`).Error)
	require.NoError(t, tx.Exec(`CREATE TABLE embeddings (
		id SERIAL PRIMARY KEY,
		source_id VARCHAR(64) NOT NULL,
		source_type INTEGER NOT NULL,
		chunk_id VARCHAR(64),
		knowledge_id VARCHAR(64),
		knowledge_base_id VARCHAR(64),
		tag_id VARCHAR(64),
		content TEXT,
		is_enabled BOOLEAN
	)`).Error)
	require.NoError(t, tx.Exec(`CREATE INDEX embeddings_search_idx ON embeddings USING bm25
		(id, knowledge_base_id, content, knowledge_id, chunk_id)
		WITH (key_field = 'id', text_fields = '{"content":{"tokenizer":{"type":"chinese_lindera"}}}')`).Error)
	require.NoError(t, tx.Exec(`INSERT INTO embeddings
		(source_id, source_type, chunk_id, knowledge_base_id, content, is_enabled) VALUES
		('off-body', 0, 'off-body', 'kb-off', 'ubuntu installation guide', true),
		('off-body-q1', 0, 'off-body', 'kb-off', 'ubuntu ubuntu ubuntu', true),
		('on-body', 0, 'on-body', 'kb-on', 'ubuntu quick notes', true),
		('on-body-q1', 0, 'on-body', 'kb-on', 'ubuntu ubuntu', true)`).Error)

	repo := &pgRepository{db: tx}
	params := types.RetrieveParams{
		Query: "ubuntu", TopK: 1, KnowledgeBaseIDs: []string{"kb-off"},
	}
	results, err := repo.KeywordsRetrieve(context.Background(), params)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Len(t, results[0].Results, 1)
	require.Equal(t, "off-body-q1", results[0].Results[0].SourceID, "fixture question should outrank the body")

	params.ExcludeGeneratedQuestionKBIDs = []string{"kb-off"}
	results, err = repo.KeywordsRetrieve(context.Background(), params)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Len(t, results[0].Results, 1)
	hit := results[0].Results[0]
	require.Equal(t, "off-body", hit.SourceID)
	require.Equal(t, "ubuntu installation guide", hit.Content)
	require.Positive(t, hit.Score)

	params.KnowledgeBaseIDs = []string{"kb-off", "kb-on"}
	params.TopK = 5
	results, err = repo.KeywordsRetrieve(context.Background(), params)
	require.NoError(t, err)
	require.Len(t, results, 1)
	var sourceIDs []string
	for _, hit := range results[0].Results {
		sourceIDs = append(sourceIDs, hit.SourceID)
	}
	require.ElementsMatch(t, []string{"off-body", "on-body", "on-body-q1"}, sourceIDs)
}

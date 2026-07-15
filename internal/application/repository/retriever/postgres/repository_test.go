package postgres

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestBatchSaveReplacesSameSourceWithoutDelete(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&pgVector{}))
	require.NoError(t, db.Exec(
		"CREATE UNIQUE INDEX embeddings_unique_source ON embeddings(source_id, source_type)",
	).Error)

	deleteCalls := 0
	require.NoError(t, db.Callback().Delete().Before("gorm:delete").Register(
		"test:count_vector_deletes",
		func(*gorm.DB) { deleteCalls++ },
	))

	repo := &pgRepository{db: db}
	first := &types.IndexInfo{
		SourceID:        "stable-source",
		SourceType:      types.ChunkSourceType,
		ChunkID:         "stable-source",
		KnowledgeID:     "knowledge-1",
		KnowledgeBaseID: "kb-1",
		Content:         "old payload",
		IsEnabled:       true,
	}
	second := *first
	second.Content = "new title and context payload"

	require.NoError(t, repo.BatchSave(context.Background(), []*types.IndexInfo{first}, map[string]any{
		"embedding": map[string][]float32{first.SourceID: {1, 2, 3}},
	}))
	require.NoError(t, repo.BatchSave(context.Background(), []*types.IndexInfo{&second}, map[string]any{
		"embedding": map[string][]float32{second.SourceID: {4, 5, 6}},
	}))

	var rows []pgVector
	require.NoError(t, db.Find(&rows).Error)
	require.Len(t, rows, 1)
	assert.Equal(t, second.Content, rows[0].Content)
	assert.Equal(t, []float32{4, 5, 6}, rows[0].Embedding.Slice())
	assert.Zero(t, deleteCalls, "replacement must use one atomic upsert, not a pre-delete")
}

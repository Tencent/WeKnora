package sqlite

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestBatchSaveReplacesSameSource(t *testing.T) {
	sqlite_vec.Auto()
	t.Cleanup(sqlite_vec.Cancel)

	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	repo := NewSQLiteRetrieveEngineRepository(db).(*sqliteRepository)

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

	var rows []sqliteEmbedding
	require.NoError(t, db.Find(&rows).Error)
	require.Len(t, rows, 1)
	assert.Equal(t, second.Content, rows[0].Content)
	assert.Equal(t, 3, rows[0].Dimension)

	var vectorJSON string
	require.NoError(t, db.Raw(
		"SELECT vec_to_json(embedding) FROM vec_embeddings_3 WHERE rowid = ?",
		rows[0].ID,
	).Scan(&vectorJSON).Error)
	assert.JSONEq(t, `[4, 5, 6]`, vectorJSON)
}

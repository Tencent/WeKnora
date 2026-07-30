package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	"github.com/stretchr/testify/require"
	sqlitedriver "gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSQLiteSaveReplacesVectorWhenDimensionChanges(t *testing.T) {
	ctx := context.Background()
	sqlite_vec.Auto()
	dbPath := filepath.Join(t.TempDir(), "retriever.db")
	db, err := gorm.Open(sqlitedriver.Open(dbPath), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	repo := NewSQLiteRetrieveEngineRepository(db).(*sqliteRepository)

	info := &types.IndexInfo{
		SourceID:        "source-1",
		SourceType:      types.ChunkSourceType,
		ChunkID:         "chunk-1",
		KnowledgeID:     "knowledge-1",
		KnowledgeBaseID: "kb-1",
		Content:         "old content",
		IsEnabled:       true,
	}
	require.NoError(t, repo.Save(ctx, info, map[string]any{
		"embedding": map[string][]float32{"source-1": {1, 0}},
	}))

	var row sqliteEmbedding
	require.NoError(t, db.Where("source_id = ? AND source_type = ?", "source-1", types.ChunkSourceType).First(&row).Error)
	require.Equal(t, 2, row.Dimension)
	require.Equal(t, int64(1), vecRowCount(t, db, 2, row.ID))

	info.Content = "new content"
	require.NoError(t, repo.Save(ctx, info, map[string]any{
		"embedding": map[string][]float32{"source-1": {1, 0, 0}},
	}))

	var updated sqliteEmbedding
	require.NoError(t, db.Where("source_id = ? AND source_type = ?", "source-1", types.ChunkSourceType).First(&updated).Error)
	require.Equal(t, row.ID, updated.ID)
	require.Equal(t, "new content", updated.Content)
	require.Equal(t, 3, updated.Dimension)
	require.Equal(t, int64(0), vecRowCount(t, db, 2, updated.ID))
	require.Equal(t, int64(1), vecRowCount(t, db, 3, updated.ID))
}

func TestSQLiteRepositoryReopenReusesExistingVecTables(t *testing.T) {
	ctx := context.Background()
	sqlite_vec.Auto()
	dbPath := filepath.Join(t.TempDir(), "retriever.db")
	db, err := gorm.Open(sqlitedriver.Open(dbPath), &gorm.Config{})
	require.NoError(t, err)
	repo := NewSQLiteRetrieveEngineRepository(db).(*sqliteRepository)

	info := &types.IndexInfo{
		SourceID:        "source-reopen",
		SourceType:      types.ChunkSourceType,
		ChunkID:         "chunk-reopen",
		KnowledgeID:     "knowledge-1",
		KnowledgeBaseID: "kb-1",
		Content:         "before reopen",
		IsEnabled:       true,
	}
	require.NoError(t, repo.Save(ctx, info, map[string]any{
		"embedding": map[string][]float32{"source-reopen": {1, 0}},
	}))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	reopenedDB, err := gorm.Open(sqlitedriver.Open(dbPath), &gorm.Config{})
	require.NoError(t, err)
	reopenedSQLDB, err := reopenedDB.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = reopenedSQLDB.Close() })
	reopenedRepo := NewSQLiteRetrieveEngineRepository(reopenedDB).(*sqliteRepository)
	require.True(t, reopenedRepo.vecTables[2], "reopened repository must discover existing vec table")

	info.Content = "after reopen"
	require.NoError(t, reopenedRepo.Save(ctx, info, map[string]any{
		"embedding": map[string][]float32{"source-reopen": {1, 0, 0}},
	}))
	var row sqliteEmbedding
	require.NoError(t, reopenedDB.Where("source_id = ? AND source_type = ?", "source-reopen", types.ChunkSourceType).First(&row).Error)
	require.Equal(t, 3, row.Dimension)
	require.Equal(t, int64(0), vecRowCount(t, reopenedDB, 2, row.ID))
	require.Equal(t, int64(1), vecRowCount(t, reopenedDB, 3, row.ID))
}

func vecRowCount(t *testing.T, db *gorm.DB, dim int, rowID uint) int64 {
	t.Helper()
	var count int64
	require.NoError(t, db.Raw("SELECT COUNT(*) FROM "+vecTableName(dim)+" WHERE rowid = ?", rowID).Scan(&count).Error)
	return count
}

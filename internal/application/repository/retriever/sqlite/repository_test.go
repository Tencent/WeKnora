package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gormsqlite "gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var registerSQLiteVec sync.Once

func newSQLiteRetrieverTestRepository(t *testing.T) *sqliteRepository {
	t.Helper()
	registerSQLiteVec.Do(sqlite_vec.Auto)

	dbName := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(gormsqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)), &gorm.Config{})
	require.NoError(t, err)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, sqlDB.Close())
	})

	repository, ok := NewSQLiteRetrieveEngineRepository(db).(*sqliteRepository)
	require.True(t, ok)
	return repository
}

func saveSQLiteTestVector(t *testing.T, repository *sqliteRepository, info *types.IndexInfo, embedding []float32) {
	t.Helper()
	require.NoError(t, repository.Save(context.Background(), info, map[string]any{
		"embedding": map[string][]float32{info.SourceID: embedding},
	}))
}

func sqliteTestIndex(chunkID, knowledgeBaseID, knowledgeID, tagID string, enabled bool) *types.IndexInfo {
	return &types.IndexInfo{
		Content:         chunkID,
		SourceID:        "source-" + chunkID,
		SourceType:      types.ChunkSourceType,
		ChunkID:         chunkID,
		KnowledgeID:     knowledgeID,
		KnowledgeBaseID: knowledgeBaseID,
		TagID:           tagID,
		IsEnabled:       enabled,
	}
}

func TestVectorRetrieveFiltersBeforeTopK(t *testing.T) {
	testCases := []struct {
		name      string
		blocker   *types.IndexInfo
		configure func(*types.RetrieveParams)
	}{
		{
			name:    "knowledge base",
			blocker: sqliteTestIndex("blocker", "kb-other", "knowledge-target", "tag-target", true),
			configure: func(params *types.RetrieveParams) {
				params.KnowledgeBaseIDs = []string{"kb-target"}
			},
		},
		{
			name:    "knowledge",
			blocker: sqliteTestIndex("blocker", "kb-target", "knowledge-other", "tag-target", true),
			configure: func(params *types.RetrieveParams) {
				params.KnowledgeIDs = []string{"knowledge-target"}
			},
		},
		{
			name:    "tag",
			blocker: sqliteTestIndex("blocker", "kb-target", "knowledge-target", "tag-other", true),
			configure: func(params *types.RetrieveParams) {
				params.TagIDs = []string{"tag-target"}
			},
		},
		{
			name:      "enabled status",
			blocker:   sqliteTestIndex("blocker", "kb-target", "knowledge-target", "tag-target", false),
			configure: func(_ *types.RetrieveParams) {},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			repository := newSQLiteRetrieverTestRepository(t)
			saveSQLiteTestVector(t, repository, testCase.blocker, []float32{1, 0})
			saveSQLiteTestVector(t, repository,
				sqliteTestIndex("target", "kb-target", "knowledge-target", "tag-target", true),
				[]float32{0.8, 0.6},
			)

			params := types.RetrieveParams{
				Embedding:     []float32{1, 0},
				TopK:          1,
				RetrieverType: types.VectorRetrieverType,
			}
			testCase.configure(&params)

			results, err := repository.vectorRetrieve(context.Background(), params)
			require.NoError(t, err)
			require.Len(t, results, 1)
			require.Len(t, results[0].Results, 1)
			assert.Equal(t, "target", results[0].Results[0].ChunkID)
		})
	}
}

func TestVectorRetrieveZeroThresholdDoesNotFilter(t *testing.T) {
	repository := newSQLiteRetrieverTestRepository(t)
	saveSQLiteTestVector(t, repository,
		sqliteTestIndex("anti-correlated", "kb-target", "knowledge-target", "tag-target", true),
		[]float32{-1, 0},
	)

	results, err := repository.vectorRetrieve(context.Background(), types.RetrieveParams{
		Embedding:        []float32{1, 0},
		KnowledgeBaseIDs: []string{"kb-target"},
		TopK:             1,
		Threshold:        0,
		RetrieverType:    types.VectorRetrieverType,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Len(t, results[0].Results, 1)
	assert.Equal(t, "anti-correlated", results[0].Results[0].ChunkID)
	assert.Less(t, results[0].Results[0].Score, 0.0)
}

func TestVectorRetrieveAppliesSimilarityThreshold(t *testing.T) {
	repository := newSQLiteRetrieverTestRepository(t)
	saveSQLiteTestVector(t, repository,
		sqliteTestIndex("below-threshold", "kb-target", "knowledge-target", "tag-target", true),
		[]float32{0.5, 0.8660254},
	)

	results, err := repository.vectorRetrieve(context.Background(), types.RetrieveParams{
		Embedding:        []float32{1, 0},
		KnowledgeBaseIDs: []string{"kb-target"},
		TopK:             1,
		Threshold:        0.75,
		RetrieverType:    types.VectorRetrieverType,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Empty(t, results[0].Results)
}

func TestRetrieveReturnsVectorQueryError(t *testing.T) {
	repository := newSQLiteRetrieverTestRepository(t)
	saveSQLiteTestVector(t, repository,
		sqliteTestIndex("chunk", "kb-target", "knowledge-target", "tag-target", true),
		[]float32{1, 0},
	)
	require.NoError(t, repository.db.Exec("DROP TABLE "+vecTableName(2)).Error)

	results, err := repository.Retrieve(context.Background(), types.RetrieveParams{
		Embedding:     []float32{1, 0},
		TopK:          1,
		RetrieverType: types.VectorRetrieverType,
	})
	require.Error(t, err)
	assert.Nil(t, results)
	assert.Contains(t, err.Error(), "sqlite-vec query failed")
}

func TestRetrieveReturnsKeywordQueryError(t *testing.T) {
	repository := newSQLiteRetrieverTestRepository(t)
	require.NoError(t, repository.db.Exec("DROP TABLE IF EXISTS lite_embeddings_fts").Error)

	results, err := repository.Retrieve(context.Background(), types.RetrieveParams{
		Query:         "missing fts table",
		TopK:          1,
		RetrieverType: types.KeywordsRetrieverType,
	})
	require.Error(t, err)
	assert.Nil(t, results)
	assert.Contains(t, err.Error(), "FTS5 query failed")
}

func TestSQLiteSaveReplacesVectorWhenDimensionChanges(t *testing.T) {
	ctx := context.Background()
	repository := newSQLiteRetrieverTestRepository(t)

	info := &types.IndexInfo{
		SourceID:        "source-1",
		SourceType:      types.ChunkSourceType,
		ChunkID:         "chunk-1",
		KnowledgeID:     "knowledge-1",
		KnowledgeBaseID: "kb-1",
		Content:         "old content",
		IsEnabled:       true,
	}
	require.NoError(t, repository.Save(ctx, info, map[string]any{
		"embedding": map[string][]float32{"source-1": {1, 0}},
	}))

	var row sqliteEmbedding
	require.NoError(t, repository.db.Where("source_id = ? AND source_type = ?", "source-1", types.ChunkSourceType).First(&row).Error)
	require.Equal(t, 2, row.Dimension)
	require.Equal(t, int64(1), vecRowCount(t, repository.db, 2, row.ID))

	info.Content = "new content"
	require.NoError(t, repository.Save(ctx, info, map[string]any{
		"embedding": map[string][]float32{"source-1": {1, 0, 0}},
	}))

	var updated sqliteEmbedding
	require.NoError(t, repository.db.Where("source_id = ? AND source_type = ?", "source-1", types.ChunkSourceType).First(&updated).Error)
	require.Equal(t, row.ID, updated.ID)
	require.Equal(t, "new content", updated.Content)
	require.Equal(t, 3, updated.Dimension)
	require.Equal(t, int64(0), vecRowCount(t, repository.db, 2, updated.ID))
	require.Equal(t, int64(1), vecRowCount(t, repository.db, 3, updated.ID))
}

func TestSQLiteRepositoryReopenReusesExistingVecTables(t *testing.T) {
	ctx := context.Background()
	registerSQLiteVec.Do(sqlite_vec.Auto)

	dbPath := filepath.Join(t.TempDir(), "retriever.db")
	db, err := gorm.Open(gormsqlite.Open(dbPath), &gorm.Config{})
	require.NoError(t, err)
	repository := NewSQLiteRetrieveEngineRepository(db).(*sqliteRepository)

	info := &types.IndexInfo{
		SourceID:        "source-reopen",
		SourceType:      types.ChunkSourceType,
		ChunkID:         "chunk-reopen",
		KnowledgeID:     "knowledge-1",
		KnowledgeBaseID: "kb-1",
		Content:         "before reopen",
		IsEnabled:       true,
	}
	require.NoError(t, repository.Save(ctx, info, map[string]any{
		"embedding": map[string][]float32{"source-reopen": {1, 0}},
	}))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	reopenedDB, err := gorm.Open(gormsqlite.Open(dbPath), &gorm.Config{})
	require.NoError(t, err)
	reopenedSQLDB, err := reopenedDB.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopenedSQLDB.Close()) })
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

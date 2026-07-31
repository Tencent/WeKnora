package sqlite

import (
	"context"
	"fmt"
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

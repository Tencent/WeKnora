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

	retrieveRepository, err := NewSQLiteRetrieveEngineRepository(db)
	require.NoError(t, err)
	repository, ok := retrieveRepository.(*sqliteRepository)
	require.True(t, ok)
	return repository
}

func TestNewSQLiteRetrieveEngineRepositoryReturnsMigrationError(t *testing.T) {
	registerSQLiteVec.Do(sqlite_vec.Auto)
	dbPath := t.TempDir() + "/readonly.db"

	writable, err := gorm.Open(gormsqlite.Open(dbPath), &gorm.Config{})
	require.NoError(t, err)
	writableSQL, err := writable.DB()
	require.NoError(t, err)
	require.NoError(t, writableSQL.Close())

	readOnly, err := gorm.Open(
		gormsqlite.Open("file:"+dbPath+"?mode=ro"),
		&gorm.Config{},
	)
	require.NoError(t, err)
	readOnlySQL, err := readOnly.DB()
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, readOnlySQL.Close())
	})

	repository, err := NewSQLiteRetrieveEngineRepository(readOnly)

	require.Nil(t, repository)
	require.ErrorContains(t, err, "auto-migrate SQLite retriever metadata")
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

func sqliteTestIndexInFolder(
	chunkID, knowledgeBaseID, knowledgeID, tagID, folderID string,
	enabled bool,
) *types.IndexInfo {
	info := sqliteTestIndex(chunkID, knowledgeBaseID, knowledgeID, tagID, enabled)
	info.FolderID = folderID
	return info
}

func TestVectorRetrieveFiltersBeforeTopK(t *testing.T) {
	testCases := []struct {
		name           string
		blocker        *types.IndexInfo
		targetFolderID string
		configure      func(*types.RetrieveParams)
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
		{
			name: "folder",
			blocker: sqliteTestIndexInFolder(
				"blocker", "kb-target", "knowledge-target", "tag-target", "sibling-folder", true,
			),
			targetFolderID: "target-folder",
			configure: func(params *types.RetrieveParams) {
				params.FolderIDs = []string{"target-folder"}
			},
		},
		{
			name: "root folder",
			blocker: sqliteTestIndexInFolder(
				"blocker", "kb-target", "knowledge-target", "tag-target", "child-folder", true,
			),
			configure: func(params *types.RetrieveParams) {
				params.FolderIDs = []string{""}
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			repository := newSQLiteRetrieverTestRepository(t)
			saveSQLiteTestVector(t, repository, testCase.blocker, []float32{1, 0})
			target := sqliteTestIndexInFolder(
				"target",
				"kb-target",
				"knowledge-target",
				"tag-target",
				testCase.targetFolderID,
				true,
			)
			saveSQLiteTestVector(t, repository, target, []float32{0.8, 0.6})

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
			assert.Equal(t, testCase.targetFolderID, results[0].Results[0].FolderID)
		})
	}
}

func TestCopyIndicesResetsFolderIDForTargetKnowledgeBase(t *testing.T) {
	repository := newSQLiteRetrieverTestRepository(t)
	enabled := true
	source := &sqliteEmbedding{
		SourceID:        "source-chunk",
		SourceType:      int(types.ChunkSourceType),
		ChunkID:         "source-chunk",
		KnowledgeID:     "source-knowledge",
		KnowledgeBaseID: "source-kb",
		FolderID:        "source-folder",
		Content:         "source content",
		IsEnabled:       &enabled,
	}
	require.NoError(t, repository.db.Create(source).Error)

	require.NoError(t, repository.CopyIndices(
		context.Background(),
		"source-kb",
		map[string]string{"source-knowledge": "target-knowledge"},
		map[string]string{"source-chunk": "target-chunk"},
		"target-kb",
		0,
		"",
	))

	var copied sqliteEmbedding
	require.NoError(t, repository.db.Where("chunk_id = ?", "target-chunk").First(&copied).Error)
	assert.Equal(t, "", copied.FolderID)
	assert.Equal(t, "target-knowledge", copied.KnowledgeID)
	assert.Equal(t, "target-kb", copied.KnowledgeBaseID)
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

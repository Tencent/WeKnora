package sqlite

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func vectorSearch(t *testing.T, repository *sqliteRepository, embedding []float32) []*types.IndexWithScore {
	t.Helper()
	results, err := repository.vectorRetrieve(context.Background(), types.RetrieveParams{
		Embedding:     embedding,
		TopK:          10,
		RetrieverType: types.VectorRetrieverType,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	return results[0].Results
}

func keywordSearch(t *testing.T, repository *sqliteRepository, query string) []*types.IndexWithScore {
	t.Helper()
	results, err := repository.keywordsRetrieve(context.Background(), types.RetrieveParams{
		Query:         query,
		TopK:          10,
		RetrieverType: types.KeywordsRetrieverType,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	return results[0].Results
}

func TestBatchSaveReplacesExistingSource(t *testing.T) {
	repository := newSQLiteRetrieverTestRepository(t)
	info := sqliteTestIndex("faq", "kb", "knowledge", "", true)
	info.Content = "oldanswer"
	saveSQLiteTestVector(t, repository, info, []float32{1, 0})

	edited := *info
	edited.Content = "newanswer"
	saveSQLiteTestVector(t, repository, &edited, []float32{0, 1})

	var count int64
	require.NoError(t, repository.db.Model(&sqliteEmbedding{}).Count(&count).Error)
	assert.Equal(t, int64(1), count, "re-indexing a source must replace its row")

	hits := vectorSearch(t, repository, []float32{0, 1})
	require.Len(t, hits, 1)
	assert.Equal(t, "newanswer", hits[0].Content)
	assert.InDelta(t, 1.0, hits[0].Score, 1e-3, "the vector must be the re-indexed one")

	if !repository.db.Migrator().HasTable("lite_embeddings_fts") {
		t.Log("FTS5 unavailable (build without sqlite_fts5); skipping keyword checks")
		return
	}
	assert.Empty(t, keywordSearch(t, repository, "oldanswer"), "stale FTS entry must be gone")
	require.Len(t, keywordSearch(t, repository, "newanswer"), 1)
}

func TestBatchSaveKeepsVectorsAlignedWhenSomeSourcesExist(t *testing.T) {
	repository := newSQLiteRetrieverTestRepository(t)
	existing := sqliteTestIndex("existing", "kb", "knowledge", "", true)
	saveSQLiteTestVector(t, repository, existing, []float32{1, 0})

	fresh := sqliteTestIndex("fresh", "kb", "knowledge", "", true)
	require.NoError(t, repository.BatchSave(context.Background(), []*types.IndexInfo{existing, fresh}, map[string]any{
		"embedding": map[string][]float32{
			existing.SourceID: {1, 0},
			fresh.SourceID:    {0, 1},
		},
	}))

	hits := vectorSearch(t, repository, []float32{0, 1})
	require.Len(t, hits, 2)
	assert.Equal(t, "fresh", hits[0].ChunkID, "the fresh row must carry its own vector")
	assert.InDelta(t, 1.0, hits[0].Score, 1e-3)
}

func TestBatchSaveKeepsLastDuplicateInBatch(t *testing.T) {
	repository := newSQLiteRetrieverTestRepository(t)
	first := sqliteTestIndex("chunk", "kb", "knowledge", "", true)
	first.Content = "firstversion"
	second := *first
	second.Content = "secondversion"
	require.NoError(t, repository.BatchSave(context.Background(), []*types.IndexInfo{first, &second}, map[string]any{
		"embedding": map[string][]float32{first.SourceID: {1, 0}},
	}))

	hits := vectorSearch(t, repository, []float32{1, 0})
	require.Len(t, hits, 1)
	assert.Equal(t, "secondversion", hits[0].Content)
}

func TestConcurrentBatchSaveNewDimensions(t *testing.T) {
	repository := newSQLiteRetrieverTestRepository(t)
	sqlDB, err := repository.db.DB()
	require.NoError(t, err)
	// Production runs SQLite on a single connection.
	sqlDB.SetMaxOpenConns(1)

	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			dim := 2 + i%4
			info := sqliteTestIndex(fmt.Sprintf("chunk-%d", i), "kb", "knowledge", "", true)
			embedding := make([]float32, dim)
			embedding[0] = 1
			errs <- repository.BatchSave(context.Background(), []*types.IndexInfo{info}, map[string]any{
				"embedding": map[string][]float32{info.SourceID: embedding},
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	for dim := 2; dim < 6; dim++ {
		assert.True(t, repository.hasVecTable(dim))
	}
}

func TestCopyIndicesCopiesEveryRowOfAChunk(t *testing.T) {
	repository := newSQLiteRetrieverTestRepository(t)
	chunk := sqliteTestIndex("src-chunk", "kb-src", "knowledge-src", "", true)
	chunk.SourceID = "src-chunk"
	question := sqliteTestIndex("src-chunk", "kb-src", "knowledge-src", "", false)
	question.SourceID = "src-chunk-q1"
	question.Content = "generated question"
	require.NoError(t, repository.BatchSave(context.Background(), []*types.IndexInfo{chunk, question}, map[string]any{
		"embedding": map[string][]float32{
			chunk.SourceID:    {1, 0},
			question.SourceID: {0, 1},
		},
	}))

	require.NoError(t, repository.CopyIndices(context.Background(), "kb-src",
		map[string]string{"knowledge-src": "knowledge-dst"},
		map[string]string{"src-chunk": "dst-chunk"},
		"kb-dst", 2, string(types.KnowledgeTypeManual),
	))

	var copied []sqliteEmbedding
	require.NoError(t, repository.db.Where("knowledge_base_id = ?", "kb-dst").Order("source_id").Find(&copied).Error)
	require.Len(t, copied, 2)
	assert.Equal(t, "dst-chunk", copied[0].SourceID)
	assert.True(t, *copied[0].IsEnabled)
	assert.Equal(t, "dst-chunk-q1", copied[1].SourceID)
	assert.False(t, *copied[1].IsEnabled, "enabled state must be copied")
	for _, row := range copied {
		assert.Equal(t, "dst-chunk", row.ChunkID)
		assert.Equal(t, "knowledge-dst", row.KnowledgeID)
	}
}

func TestEnableImageChunkIndexesRepairsOnlyEnabledImageChunks(t *testing.T) {
	repository := newSQLiteRetrieverTestRepository(t)
	require.NoError(t, repository.db.Exec(
		`CREATE TABLE chunks (id TEXT PRIMARY KEY, chunk_type TEXT, is_enabled BOOLEAN)`).Error)
	require.NoError(t, repository.db.Exec(`INSERT INTO chunks (id, chunk_type, is_enabled) VALUES
		('ocr', 'image_ocr', 1), ('caption', 'image_caption', 1),
		('ocr-off', 'image_ocr', 0), ('text-off', 'text', 1)`).Error)
	for _, id := range []string{"ocr", "caption", "ocr-off", "text-off"} {
		saveSQLiteTestVector(t, repository, sqliteTestIndex(id, "kb", "knowledge", "", false), []float32{1, 0})
	}

	enableImageChunkIndexes(repository.db)

	enabled := map[string]bool{}
	var rows []sqliteEmbedding
	require.NoError(t, repository.db.Find(&rows).Error)
	for _, row := range rows {
		enabled[row.ChunkID] = *row.IsEnabled
	}
	assert.Equal(t, map[string]bool{
		"ocr": true, "caption": true, "ocr-off": false, "text-off": false,
	}, enabled)
}

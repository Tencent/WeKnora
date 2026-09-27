package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BatchUpdateChunkEnabledStatus / BatchUpdateChunkTagID only touch the index
// copy (lite_embeddings) that retrieval reads, while the authoritative chunks
// row is written by the service layer beforehand. Swallowing an UPDATE error
// therefore makes the call look successful while retrieval keeps filtering on
// the stale is_enabled / tag_id value, so the error has to reach the caller.

func TestBatchUpdateChunkEnabledStatusReturnsUpdateError(t *testing.T) {
	repository := newSQLiteRetrieverTestRepository(t)
	saveSQLiteTestVector(t, repository,
		sqliteTestIndex("chunk-1", "kb-1", "knowledge-1", "tag-1", true),
		[]float32{1, 0},
	)
	require.NoError(t, repository.db.Exec("DROP TABLE lite_embeddings").Error)

	err := repository.BatchUpdateChunkEnabledStatus(context.Background(), map[string]bool{"chunk-1": false})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "chunk-1")
	assert.Contains(t, err.Error(), "is_enabled")
}

func TestBatchUpdateChunkTagIDReturnsUpdateError(t *testing.T) {
	repository := newSQLiteRetrieverTestRepository(t)
	saveSQLiteTestVector(t, repository,
		sqliteTestIndex("chunk-1", "kb-1", "knowledge-1", "tag-1", true),
		[]float32{1, 0},
	)
	require.NoError(t, repository.db.Exec("DROP TABLE lite_embeddings").Error)

	err := repository.BatchUpdateChunkTagID(context.Background(), map[string]string{"chunk-1": "tag-2"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "chunk-1")
	assert.Contains(t, err.Error(), "tag_id")
}

// A statement that fails inside a batch must not be masked by the remaining
// statements of the same batch.
func TestBatchUpdateChunkTagIDReportsFailingStatementInBatch(t *testing.T) {
	repository := newSQLiteRetrieverTestRepository(t)
	for _, chunkID := range []string{"chunk-ok", "chunk-fail"} {
		saveSQLiteTestVector(t, repository,
			sqliteTestIndex(chunkID, "kb-1", "knowledge-1", "tag-1", true),
			[]float32{1, 0},
		)
	}
	require.NoError(t, repository.db.Exec(`CREATE TRIGGER fail_chunk_tag_update
		BEFORE UPDATE ON lite_embeddings
		WHEN OLD.chunk_id = 'chunk-fail'
		BEGIN SELECT RAISE(ABORT, 'simulated index write failure'); END`).Error)

	err := repository.BatchUpdateChunkTagID(context.Background(), map[string]string{
		"chunk-ok":   "tag-2",
		"chunk-fail": "tag-2",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "chunk-fail")
	assert.Contains(t, err.Error(), "simulated index write failure")
}

// Happy path: the updates still apply and report success.
func TestBatchUpdateChunkStatusAndTagApply(t *testing.T) {
	repository := newSQLiteRetrieverTestRepository(t)
	saveSQLiteTestVector(t, repository,
		sqliteTestIndex("chunk-1", "kb-1", "knowledge-1", "tag-1", true),
		[]float32{1, 0},
	)

	require.NoError(t, repository.BatchUpdateChunkEnabledStatus(
		context.Background(), map[string]bool{"chunk-1": false}))
	require.NoError(t, repository.BatchUpdateChunkTagID(
		context.Background(), map[string]string{"chunk-1": "tag-2"}))

	var row sqliteEmbedding
	require.NoError(t, repository.db.Where("chunk_id = ?", "chunk-1").First(&row).Error)
	require.NotNil(t, row.IsEnabled)
	assert.False(t, *row.IsEnabled)
	assert.Equal(t, "tag-2", row.TagID)
}

package sqlite

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sqliteDeleteCases() []struct {
	name   string
	delete func(context.Context, *sqliteRepository) error
} {
	return []struct {
		name   string
		delete func(context.Context, *sqliteRepository) error
	}{
		{"chunks", func(ctx context.Context, r *sqliteRepository) error {
			return r.DeleteByChunkIDList(ctx, []string{"target-a", "target-b"}, 2, "")
		}},
		{"sources", func(ctx context.Context, r *sqliteRepository) error {
			return r.DeleteBySourceIDList(ctx, []string{"source-target-a", "source-target-b"}, 2, "")
		}},
		{"knowledge", func(ctx context.Context, r *sqliteRepository) error {
			return r.DeleteByKnowledgeIDList(ctx, []string{"knowledge-target"}, 2, "")
		}},
	}
}

func populateSQLiteDeleteTest(t *testing.T, r *sqliteRepository) {
	t.Helper()
	for _, chunkID := range []string{"target-a", "target-b", "control"} {
		knowledgeID := "knowledge-target"
		if chunkID == "control" {
			knowledgeID = "knowledge-control"
		}
		saveSQLiteTestVector(t, r, sqliteTestIndex(chunkID, "kb", knowledgeID, "", true), []float32{1, 0})
	}
}

func TestSQLiteDeleteRollsBackWhenMetadataDeleteFails(t *testing.T) {
	for _, tc := range sqliteDeleteCases() {
		t.Run(tc.name, func(t *testing.T) {
			r := newSQLiteRetrieverTestRepository(t)
			populateSQLiteDeleteTest(t, r)
			// The real database rejects the final statement, after vector/FTS
			// cleanup has already run. A failed deletion must keep all three
			// representations searchable, rather than preserving only metadata.
			require.NoError(t, r.db.Exec(`CREATE TRIGGER reject_metadata_delete
				BEFORE DELETE ON lite_embeddings WHEN OLD.chunk_id = 'target-b'
				BEGIN SELECT RAISE(ABORT, 'metadata deletion rejected'); END`).Error)

			err := tc.delete(context.Background(), r)
			require.ErrorContains(t, err, "metadata deletion rejected")
			assert.EqualValues(t, 3, countSQLiteRows(t, r, "SELECT count(*) FROM lite_embeddings"))
			assert.EqualValues(t, 3, countSQLiteRows(t, r, "SELECT count(*) FROM vec_embeddings_2"))
			if !r.ftsUnavailable {
				assert.EqualValues(t, 3, countSQLiteRows(t, r, "SELECT count(*) FROM lite_embeddings_fts"))
			}
			results, err := r.vectorRetrieve(context.Background(), types.RetrieveParams{
				KnowledgeBaseIDs: []string{"kb"}, Embedding: []float32{1, 0},
				RetrieverType: types.VectorRetrieverType, TopK: 5,
			})
			require.NoError(t, err)
			require.Len(t, results, 1)
			assert.Len(t, results[0].Results, 3, "failed cleanup must leave the original rows retrievable")

			require.NoError(t, r.db.Exec("DROP TRIGGER reject_metadata_delete").Error)
			require.NoError(t, tc.delete(context.Background(), r))
			require.NoError(t, tc.delete(context.Background(), r), "retry remains idempotent")
			assert.EqualValues(t, 1, countSQLiteRows(t, r, "SELECT count(*) FROM lite_embeddings"))
			assert.EqualValues(t, 1, countSQLiteRows(t, r, "SELECT count(*) FROM vec_embeddings_2"))
			if !r.ftsUnavailable {
				assert.EqualValues(t, 1, countSQLiteRows(t, r, "SELECT count(*) FROM lite_embeddings_fts"))
			}
		})
	}
}

func TestSQLiteDeleteRollsBackWhenFTSDeleteFails(t *testing.T) {
	for _, tc := range sqliteDeleteCases() {
		t.Run(tc.name, func(t *testing.T) {
			r := newSQLiteRetrieverTestRepository(t)
			if r.ftsUnavailable {
				t.Skip("requires sqlite_fts5")
			}
			populateSQLiteDeleteTest(t, r)
			require.NoError(t, r.db.Exec("DROP TABLE lite_embeddings_fts").Error)
			require.ErrorContains(t, tc.delete(context.Background(), r), "lite_embeddings_fts")
			assert.EqualValues(t, 3, countSQLiteRows(t, r, "SELECT count(*) FROM lite_embeddings"))
			assert.EqualValues(t, 3, countSQLiteRows(t, r, "SELECT count(*) FROM vec_embeddings_2"))
			require.NoError(t, r.initFTS5())
			require.NoError(t, tc.delete(context.Background(), r))
			assert.EqualValues(t, 1, countSQLiteRows(t, r, "SELECT count(*) FROM lite_embeddings_fts"))
		})
	}
}

func TestSQLiteDeleteRollsBackAcrossVectorDimensions(t *testing.T) {
	r := newSQLiteRetrieverTestRepository(t)
	populateSQLiteDeleteTest(t, r)
	saveSQLiteTestVector(t, r, sqliteTestIndex("target-b", "kb", "knowledge-target", "", true), []float32{1, 0, 0})
	require.NoError(t, r.db.Exec(`CREATE TRIGGER reject_metadata_delete
		BEFORE DELETE ON lite_embeddings WHEN OLD.chunk_id = 'target-b'
		BEGIN SELECT RAISE(ABORT, 'metadata deletion rejected'); END`).Error)
	require.ErrorContains(t, r.DeleteByKnowledgeIDList(context.Background(), []string{"knowledge-target"}, 2, ""),
		"metadata deletion rejected")
	assert.EqualValues(t, 2, countSQLiteRows(t, r, "SELECT count(*) FROM vec_embeddings_2"))
	assert.EqualValues(t, 1, countSQLiteRows(t, r, "SELECT count(*) FROM vec_embeddings_3"))
}

func TestSQLiteDeleteCanceledContextKeepsIndexes(t *testing.T) {
	for _, tc := range sqliteDeleteCases() {
		t.Run(tc.name, func(t *testing.T) {
			r := newSQLiteRetrieverTestRepository(t)
			populateSQLiteDeleteTest(t, r)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			require.ErrorIs(t, tc.delete(ctx, r), context.Canceled)
			assert.EqualValues(t, 3, countSQLiteRows(t, r, "SELECT count(*) FROM lite_embeddings"))
			assert.EqualValues(t, 3, countSQLiteRows(t, r, "SELECT count(*) FROM vec_embeddings_2"))
		})
	}
}

func TestSQLiteDeleteEmptyListsKeepIndexes(t *testing.T) {
	r := newSQLiteRetrieverTestRepository(t)
	populateSQLiteDeleteTest(t, r)
	for _, delete := range []func(context.Context, []string, int, string) error{
		r.DeleteByChunkIDList, r.DeleteBySourceIDList, r.DeleteByKnowledgeIDList,
	} {
		for _, ids := range [][]string{nil, {}} {
			require.NoError(t, delete(context.Background(), ids, 2, ""))
		}
	}
	assert.EqualValues(t, 3, countSQLiteRows(t, r, "SELECT count(*) FROM lite_embeddings"))
	assert.EqualValues(t, 3, countSQLiteRows(t, r, "SELECT count(*) FROM vec_embeddings_2"))
}

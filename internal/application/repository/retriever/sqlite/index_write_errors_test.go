package sqlite

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A chunk row is only useful when its vec0 and FTS rows exist too: retrieval
// joins both index tables on lite_embeddings.id. A discarded index write used
// to commit the chunk row while Save still returned nil, so these tests pin the
// error and the rollback, not just the error.

func countSQLiteRows(t *testing.T, repository *sqliteRepository, sql string, args ...any) int64 {
	t.Helper()
	var count int64
	require.NoError(t, repository.db.Raw(sql, args...).Scan(&count).Error, "count query: %s", sql)
	return count
}

func TestBatchSaveRollsBackWhenVecTableIsGone(t *testing.T) {
	repository := newSQLiteRetrieverTestRepository(t)
	ctx := context.Background()

	control := sqliteTestIndex("ctl", "kb", "knowledge", "", true)
	saveSQLiteTestVector(t, repository, control, []float32{1, 0})
	require.EqualValues(t, 1, countSQLiteRows(t, repository, "SELECT count(*) FROM vec_embeddings_2"))

	require.NoError(t, repository.db.Exec("DROP TABLE vec_embeddings_2").Error)

	victim := sqliteTestIndex("victim", "kb", "knowledge", "", true)
	err := repository.Save(ctx, victim, map[string]any{
		"embedding": map[string][]float32{victim.SourceID: {0, 1}},
	})

	require.Error(t, err, "a dropped vec0 table must not be reported as a successful save")
	assert.Contains(t, err.Error(), "vec_embeddings_2")
	assert.EqualValues(t, 0, countSQLiteRows(t, repository,
		"SELECT count(*) FROM lite_embeddings WHERE chunk_id = ?", victim.ChunkID),
		"the chunk row must roll back together with its vector row")

	// The failed write drops the cached "ready" state, so the next save
	// recreates the table instead of failing forever.
	require.NoError(t, repository.Save(ctx, victim, map[string]any{
		"embedding": map[string][]float32{victim.SourceID: {0, 1}},
	}))
	assert.EqualValues(t, 1, countSQLiteRows(t, repository,
		"SELECT count(*) FROM lite_embeddings WHERE chunk_id = ?", victim.ChunkID))
	assert.EqualValues(t, 1, countSQLiteRows(t, repository, "SELECT count(*) FROM vec_embeddings_2"))
}

func TestBatchSaveRollsBackWhenFTSTableIsGone(t *testing.T) {
	repository := newSQLiteRetrieverTestRepository(t)
	if !repository.db.Migrator().HasTable("lite_embeddings_fts") {
		t.Skip("FTS5 unavailable (build without sqlite_fts5)")
	}
	ctx := context.Background()

	control := sqliteTestIndex("ctl", "kb", "knowledge", "", true)
	saveSQLiteTestVector(t, repository, control, []float32{1, 0})
	require.EqualValues(t, 1, countSQLiteRows(t, repository, "SELECT count(*) FROM lite_embeddings_fts"))

	require.NoError(t, repository.db.Exec("DROP TABLE lite_embeddings_fts").Error)

	victim := sqliteTestIndex("victim", "kb", "knowledge", "", true)
	err := repository.Save(ctx, victim, map[string]any{
		"embedding": map[string][]float32{victim.SourceID: {0, 1}},
	})

	require.Error(t, err, "a dropped FTS table must not be reported as a successful save")
	assert.Contains(t, err.Error(), "lite_embeddings_fts")
	assert.EqualValues(t, 0, countSQLiteRows(t, repository,
		"SELECT count(*) FROM lite_embeddings WHERE chunk_id = ?", victim.ChunkID),
		"the chunk row must roll back together with its keyword index row")
	assert.EqualValues(t, 0, countSQLiteRows(t, repository, `SELECT count(*) FROM vec_embeddings_2
		WHERE rowid IN (SELECT id FROM lite_embeddings WHERE chunk_id = ?)`, victim.ChunkID))
}

func TestBatchSaveRollsBackWhenVecInsertIsRejected(t *testing.T) {
	repository := newSQLiteRetrieverTestRepository(t)
	ctx := context.Background()

	// A vec0 table of another dimension occupies the name: the table is real,
	// so only the insert itself can fail. A 2-dimension vector into float[3]
	// is rejected by vec0.
	require.NoError(t, repository.db.Exec(`CREATE VIRTUAL TABLE vec_embeddings_2
		USING vec0(embedding float[3] distance_metric=cosine)`).Error)

	victim := sqliteTestIndex("victim", "kb", "knowledge", "", true)
	err := repository.Save(ctx, victim, map[string]any{
		"embedding": map[string][]float32{victim.SourceID: {0, 1}},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "vec_embeddings_2")
	assert.EqualValues(t, 0, countSQLiteRows(t, repository,
		"SELECT count(*) FROM lite_embeddings WHERE chunk_id = ?", victim.ChunkID),
		"a rejected vector insert must roll the chunk row back")
}

// An object that only occupies the vec0 table name used to be cached as a
// ready dimension, which kept the whole process from ever creating the table.
func TestSaveFailsWhenAnotherObjectOwnsTheVecTableName(t *testing.T) {
	repository := newSQLiteRetrieverTestRepository(t)
	ctx := context.Background()

	require.NoError(t, repository.db.Exec(`CREATE VIEW vec_embeddings_2 AS SELECT 1 AS x WHERE 0`).Error)

	victim := sqliteTestIndex("victim", "kb", "knowledge", "", true)
	err := repository.Save(ctx, victim, map[string]any{
		"embedding": map[string][]float32{victim.SourceID: {1, 0}},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "vec_embeddings_2")
	assert.False(t, repository.hasVecTable(2), "a failed prepare must not be cached as ready")
	assert.EqualValues(t, 0, countSQLiteRows(t, repository,
		"SELECT count(*) FROM lite_embeddings WHERE chunk_id = ?", victim.ChunkID))

	// Once the obstruction is gone the very next save must retry the table.
	require.NoError(t, repository.db.Exec("DROP VIEW vec_embeddings_2").Error)
	require.NoError(t, repository.Save(ctx, victim, map[string]any{
		"embedding": map[string][]float32{victim.SourceID: {1, 0}},
	}))
	assert.EqualValues(t, 1, countSQLiteRows(t, repository,
		"SELECT count(*) FROM vec_embeddings_2"))
}

func TestDeleteByChunkIDListReportsIndexDeleteFailure(t *testing.T) {
	repository := newSQLiteRetrieverTestRepository(t)
	info := sqliteTestIndex("chunk", "kb", "knowledge", "", true)
	saveSQLiteTestVector(t, repository, info, []float32{1, 0})

	require.NoError(t, repository.db.Exec("DROP TABLE vec_embeddings_2").Error)

	err := repository.DeleteByChunkIDList(context.Background(), []string{info.ChunkID}, 2,
		string(types.KnowledgeTypeManual))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "vec_embeddings_2")
	assert.EqualValues(t, 1, countSQLiteRows(t, repository,
		"SELECT count(*) FROM lite_embeddings WHERE chunk_id = ?", info.ChunkID),
		"the chunk row must survive an index delete that failed")
}

func TestCopyIndicesReportsVectorCopyFailure(t *testing.T) {
	repository := newSQLiteRetrieverTestRepository(t)
	source := sqliteTestIndex("chunk", "kb-src", "knowledge-src", "", true)
	saveSQLiteTestVector(t, repository, source, []float32{1, 0})

	require.NoError(t, repository.db.Exec("DROP TABLE vec_embeddings_2").Error)

	err := repository.CopyIndices(context.Background(), "kb-src",
		map[string]string{"knowledge-src": "knowledge-dst"},
		map[string]string{"chunk": "chunk-dst"},
		"kb-dst", 2, string(types.KnowledgeTypeManual),
	)

	require.Error(t, err, "a copy without its vector row must not be reported as a success")
	assert.Contains(t, err.Error(), "vec_embeddings_2")
}

func TestSaveStillWritesEveryIndexRowOnHealthyTables(t *testing.T) {
	repository := newSQLiteRetrieverTestRepository(t)
	info := sqliteTestIndex("chunk", "kb", "knowledge", "", true)
	saveSQLiteTestVector(t, repository, info, []float32{1, 0})

	assert.EqualValues(t, 1, countSQLiteRows(t, repository,
		"SELECT count(*) FROM lite_embeddings WHERE chunk_id = ?", info.ChunkID))
	assert.EqualValues(t, 1, countSQLiteRows(t, repository, "SELECT count(*) FROM vec_embeddings_2"))
	if repository.db.Migrator().HasTable("lite_embeddings_fts") {
		assert.EqualValues(t, 1, countSQLiteRows(t, repository,
			"SELECT count(*) FROM lite_embeddings_fts"))
		assert.Len(t, keywordSearch(t, repository, "content of chunk"), 1)
	}
}

// A driver built without the fts5 module cannot host a keyword index at all.
// That is a property of the build, so saving keeps working and the keyword
// index is simply absent; a table that goes missing while fts5 is compiled in
// is a real failure and is covered by TestBatchSaveRollsBackWhenFTSTableIsGone.
func TestSaveWorksWhenTheFTS5ModuleIsNotCompiledIn(t *testing.T) {
	repository := newSQLiteRetrieverTestRepository(t)
	if repository.db.Migrator().HasTable("lite_embeddings_fts") {
		t.Skip("fts5 is compiled in; the keyword index is checked by the other tests")
	}

	info := sqliteTestIndex("chunk", "kb", "knowledge", "", true)
	saveSQLiteTestVector(t, repository, info, []float32{1, 0})

	assert.EqualValues(t, 1, countSQLiteRows(t, repository,
		"SELECT count(*) FROM lite_embeddings WHERE chunk_id = ?", info.ChunkID))
	assert.EqualValues(t, 1, countSQLiteRows(t, repository, "SELECT count(*) FROM vec_embeddings_2"))
}

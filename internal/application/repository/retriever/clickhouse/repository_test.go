package clickhouse

import (
	"context"
	"math"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestRepo 构造一个共享 sqlmock 的 clickhouseRepository，
// 默认绕过 ensureTable（initializedTables 已置位），便于专注测试 SQL 形态。
func newTestRepo(t *testing.T) (*clickhouseRepository, sqlmock.Sqlmock, func()) {
	t.Helper()

	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)

	repo := &clickhouseRepository{
		db:            db,
		database:      "weknora",
		tableBaseName: "weknora_embeddings",
	}
	// 默认绕过 ensureTable：调用方在需要时自行清除该标记。
	repo.initializedTables.Store(3, true)
	repo.initializedTables.Store(1536, true)

	cleanup := func() { _ = db.Close() }
	return repo, mock, cleanup
}

func testIndexInfo() *types.IndexInfo {
	return &types.IndexInfo{
		ID:              "row-1",
		Content:         "hello world",
		SourceID:        "chunk-1",
		SourceType:      types.SourceType(1),
		ChunkID:         "chunk-1",
		KnowledgeID:     "kb-1",
		KnowledgeBaseID: "base-1",
		TagID:           "tag-1",
		IsEnabled:       true,
	}
}

func testParams(embedding []float32) map[string]any {
	return map[string]any{
		fieldEmbedding: map[string][]float32{
			"chunk-1": embedding,
		},
	}
}

// ---------------------------------------------------------------------------
// BuildDSN / DDL golden
// ---------------------------------------------------------------------------

func TestBuildDSN(t *testing.T) {
	assert.Equal(t, "clickhouse://clickhouse:9000/weknora",
		BuildDSN("clickhouse:9000", "", "", "weknora"))
	assert.Equal(t, "clickhouse://default:secret@clickhouse:9000/weknora",
		BuildDSN("clickhouse:9000", "default", "secret", "weknora"))
	// 密码含特殊字符时必须做 URL 编码，否则 DSN 解析会断。
	assert.Equal(t, "clickhouse://default:p%40ss%3Aw%2Frd@clickhouse:9000/weknora",
		BuildDSN("clickhouse:9000", "default", "p@ss:w/rd", "weknora"))
}

func TestBuildCreateTableDDL(t *testing.T) {
	ddl := buildCreateTableDDL("weknora_embeddings_1536", 1536)
	assert.Contains(t, ddl, "CREATE TABLE IF NOT EXISTS `weknora_embeddings_1536`")
	assert.Contains(t, ddl, "ENGINE = ReplacingMergeTree()")
	assert.Contains(t, ddl, "ORDER BY id")
	assert.Contains(t, ddl, "embedding         Array(Float32)")
	assert.Contains(t, ddl, "INDEX idx_emb embedding TYPE vector_similarity('hnsw', 'cosineDistance', 1536)")
	// 维度只出现在索引参数里，不拼进列类型。
}

func TestEmbeddingLiteralRoundTrip(t *testing.T) {
	assert.Equal(t, "[]", embeddingLiteral(nil))
	assert.Equal(t, "[0.1,0.2,0.3]", embeddingLiteral([]float32{0.1, 0.2, 0.3}))
	assert.Equal(t, "[1,-2.5,1e+06]", embeddingLiteral([]float32{1, -2.5, 1e6}))

	vec, err := parseEmbeddingLiteral([]byte("[0.1, 0.2, 0.3]"))
	require.NoError(t, err)
	assert.InDeltaSlice(t, []float32{0.1, 0.2, 0.3}, vec, 1e-6)

	empty, err := parseEmbeddingLiteral([]byte("[]"))
	require.NoError(t, err)
	assert.Nil(t, empty)

	_, err = parseEmbeddingLiteral([]byte("[0.1,abc]"))
	require.Error(t, err)
}

func TestValidateEmbedding(t *testing.T) {
	require.NoError(t, validateEmbedding([]float32{0.1, -2}))
	require.Error(t, validateEmbedding([]float32{0.1, float32(math.NaN())}))
	require.Error(t, validateEmbedding([]float32{float32(math.Inf(1))}))
}

func TestParseEmbeddingValue(t *testing.T) {
	vec, err := parseEmbeddingValue([]float32{0.1, 0.2})
	require.NoError(t, err)
	assert.InDeltaSlice(t, []float32{0.1, 0.2}, vec, 1e-6)

	vec, err = parseEmbeddingValue("[0.1,0.2]")
	require.NoError(t, err)
	assert.InDeltaSlice(t, []float32{0.1, 0.2}, vec, 1e-6)

	vec, err = parseEmbeddingValue(nil)
	require.NoError(t, err)
	assert.Nil(t, vec)

	_, err = parseEmbeddingValue(42)
	require.Error(t, err)
}

func TestEngineTypeAndSupport(t *testing.T) {
	repo := &clickhouseRepository{}
	assert.Equal(t, types.ClickhouseRetrieverEngineType, repo.EngineType())
	// PR1 只支持 dense 向量检索。
	assert.Equal(t, []types.RetrieverType{types.VectorRetrieverType}, repo.Support())
}

// ---------------------------------------------------------------------------
// BatchSave
// ---------------------------------------------------------------------------

func TestBatchSave_InsertsRows(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()
	// 本用例走 ensureTable：清除 3 维的已初始化标记。
	repo.initializedTables.Delete(3)

	mock.ExpectQuery(regexp.QuoteMeta(
		"SELECT count() FROM system.tables WHERE database = ? AND name = ?")).
		WithArgs("weknora", "weknora_embeddings_3").
		WillReturnRows(sqlmock.NewRows([]string{"count()"}).AddRow(0))
	mock.ExpectExec(regexp.QuoteMeta(buildCreateTableDDL("weknora_embeddings_3", 3))).
		WillReturnResult(sqlmock.NewResult(0, 0))

	info := testIndexInfo()
	stmt := "INSERT INTO `weknora_embeddings_3` (" + strings.Join(columns, ", ") + ") " +
		"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, [0.1,0.2,0.3])"
	mock.ExpectExec(regexp.QuoteMeta(stmt)).
		WithArgs("row-1", "hello world", "chunk-1", 1, "chunk-1", "kb-1", "base-1", "tag-1", uint8(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.BatchSave(context.Background(), []*types.IndexInfo{info}, testParams([]float32{0.1, 0.2, 0.3}))
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBatchSave_SkipsEmptyEmbedding(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	err := repo.BatchSave(context.Background(), []*types.IndexInfo{testIndexInfo()}, testParams(nil))
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBatchSave_RejectsNonFiniteEmbedding(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	err := repo.BatchSave(context.Background(),
		[]*types.IndexInfo{testIndexInfo()}, testParams([]float32{0.1, float32(math.NaN())}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid embedding")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBatchSave_EmptyListIsNoop(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	require.NoError(t, repo.BatchSave(context.Background(), nil, nil))
	require.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// VectorRetrieve
// ---------------------------------------------------------------------------

func TestVectorRetrieve_MapsRows(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta(
		"SELECT count() FROM system.tables WHERE database = ? AND name = ?")).
		WithArgs("weknora", "weknora_embeddings_3").
		WillReturnRows(sqlmock.NewRows([]string{"count()"}).AddRow(1))

	cols := append(append([]string{}, columnsForRetrieve...), "score")
	mock.ExpectQuery("SELECT (.+) FROM `weknora_embeddings_3` FINAL WHERE (.+)").
		WithArgs(1, "base-1", 0.5).
		WillReturnRows(sqlmock.NewRows(cols).
			AddRow("row-1", "hello world", "chunk-1", 1, "chunk-1", "kb-1", "base-1", "tag-1", int64(1), 0.9).
			AddRow("row-2", "second", "chunk-2", 1, "chunk-2", "kb-2", "base-1", "", int64(0), 0.7))

	results, err := repo.Retrieve(context.Background(), types.RetrieveParams{
		RetrieverType:    types.VectorRetrieverType,
		Embedding:        []float32{0.1, 0.2, 0.3},
		KnowledgeBaseIDs: []string{"base-1"},
		TopK:             10,
		Threshold:        0.5,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Len(t, results[0].Results, 2)
	assert.Equal(t, types.ClickhouseRetrieverEngineType, results[0].RetrieverEngineType)
	assert.Equal(t, types.VectorRetrieverType, results[0].RetrieverType)
	assert.Equal(t, "row-1", results[0].Results[0].ID)
	assert.InDelta(t, 0.9, results[0].Results[0].Score, 1e-9)
	assert.Equal(t, types.MatchTypeEmbedding, results[0].Results[0].MatchType)
	assert.True(t, results[0].Results[0].IsEnabled)
	assert.False(t, results[0].Results[1].IsEnabled)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestVectorRetrieve_MissingTableReturnsEmpty(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta(
		"SELECT count() FROM system.tables WHERE database = ? AND name = ?")).
		WithArgs("weknora", "weknora_embeddings_3").
		WillReturnRows(sqlmock.NewRows([]string{"count()"}).AddRow(0))

	results, err := repo.Retrieve(context.Background(), types.RetrieveParams{
		RetrieverType: types.VectorRetrieverType,
		Embedding:     []float32{0.1, 0.2, 0.3},
		TopK:          10,
		Threshold:     0.5,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Empty(t, results[0].Results)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestVectorRetrieve_InvalidEmbedding(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	_, err := repo.Retrieve(context.Background(), types.RetrieveParams{
		RetrieverType: types.VectorRetrieverType,
		Embedding:     []float32{float32(math.NaN())},
		TopK:          10,
	})
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRetrieve_KeywordsTypeReturnsError(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	_, err := repo.Retrieve(context.Background(), types.RetrieveParams{
		RetrieverType: types.KeywordsRetrieverType,
		Query:         "hello",
		TopK:          10,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not support retriever type")
	require.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// Delete*
// ---------------------------------------------------------------------------

func TestDeleteByChunkIDList(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	mock.ExpectExec(regexp.QuoteMeta(
		"ALTER TABLE `weknora_embeddings_3` DELETE WHERE chunk_id IN (?, ?)")).
		WithArgs("chunk-1", "chunk-2").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := repo.DeleteByChunkIDList(context.Background(), []string{"chunk-1", "chunk-2"}, 3, "")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteByKnowledgeIDList(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	mock.ExpectExec(regexp.QuoteMeta(
		"ALTER TABLE `weknora_embeddings_3` DELETE WHERE knowledge_id IN (?)")).
		WithArgs("kb-1").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := repo.DeleteByKnowledgeIDList(context.Background(), []string{"kb-1"}, 3, "")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteBySourceIDList(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	mock.ExpectExec(regexp.QuoteMeta(
		"ALTER TABLE `weknora_embeddings_3` DELETE WHERE source_id IN (?)")).
		WithArgs("chunk-1").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := repo.DeleteBySourceIDList(context.Background(), []string{"chunk-1"}, 3, "")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDelete_EmptyListIsNoop(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	require.NoError(t, repo.DeleteByChunkIDList(context.Background(), nil, 3, ""))
	require.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// BatchUpdate*
// ---------------------------------------------------------------------------

func TestBatchUpdateChunkEnabledStatus_TrueOnly(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta(
		"SELECT name FROM system.tables WHERE database = ? AND name LIKE ?")).
		WithArgs("weknora", "weknora_embeddings\\_%").
		WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("weknora_embeddings_3"))
	mock.ExpectExec(regexp.QuoteMeta(
		"ALTER TABLE `weknora_embeddings_3` UPDATE is_enabled = 1 WHERE chunk_id IN (?)")).
		WithArgs("chunk-1").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := repo.BatchUpdateChunkEnabledStatus(context.Background(), map[string]bool{"chunk-1": true})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBatchUpdateChunkTagID(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta(
		"SELECT name FROM system.tables WHERE database = ? AND name LIKE ?")).
		WithArgs("weknora", "weknora_embeddings\\_%").
		WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("weknora_embeddings_3"))
	mock.ExpectExec(regexp.QuoteMeta(
		"ALTER TABLE `weknora_embeddings_3` UPDATE tag_id = ? WHERE chunk_id IN (?)")).
		WithArgs("tag-9", "chunk-1").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := repo.BatchUpdateChunkTagID(context.Background(), map[string]string{"chunk-1": "tag-9"})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// CopyIndices / EstimateStorageSize
// ---------------------------------------------------------------------------

func TestCopyIndices(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	// ensureTable：3 维已在 newTestRepo 里置位，直接跳过。
	cols := append([]string{}, columnsForCopy...)
	mock.ExpectQuery("SELECT (.+) FROM `weknora_embeddings_3` FINAL WHERE knowledge_base_id = \\? ORDER BY id LIMIT 64 OFFSET 0").
		WithArgs("base-src").
		WillReturnRows(sqlmock.NewRows(cols).
			AddRow("row-1", "hello world", "chunk-1", 1, "chunk-1", "kb-src", "base-src", "tag-1", int64(1), "[0.1,0.2,0.3]"))

	stmt := "INSERT INTO `weknora_embeddings_3` (" + strings.Join(columns, ", ") + ") " +
		"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, [0.1,0.2,0.3])"
	mock.ExpectExec(regexp.QuoteMeta(stmt)).
		WithArgs(sqlmock.AnyArg(), "hello world", "chunk-2", 1, "chunk-2", "kb-dst", "base-dst", "tag-1", uint8(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.CopyIndices(context.Background(),
		"base-src",
		map[string]string{"kb-src": "kb-dst"},
		map[string]string{"chunk-1": "chunk-2"},
		"base-dst", 3, "")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEstimateStorageSize(t *testing.T) {
	repo := &clickhouseRepository{}
	size := repo.EstimateStorageSize(context.Background(),
		[]*types.IndexInfo{testIndexInfo()}, testParams([]float32{0.1, 0.2, 0.3}))
	// payload: content(11)+source(7)+chunk(7)+knowledge(4)+kb(6)+tag(5)+8 = 48
	// vector: 3*4 = 12；HNSW M=32: 32*2*8 = 512；meta 24。合计 596。
	assert.Equal(t, int64(596), size)
}

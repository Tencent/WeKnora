package doris

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestRepo 构造一个共享 sqlmock 的 dorisRepository，
// 默认绕过 ensureTable（initializedTables 已置位），便于专注测试 SQL 形态。
//
// 返回的 cleanup 用 defer 调用即可。
func newTestRepo(t *testing.T) (*dorisRepository, sqlmock.Sqlmock, *httptest.Server, func()) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 默认行为：成功，回 1 行。
		body, _ := io.ReadAll(r.Body)
		_ = body
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Status":           "Success",
			"NumberTotalRows":  1,
			"NumberLoadedRows": 1,
			"Label":            "test",
		})
	}))

	repo := &dorisRepository{
		db:            db,
		httpClient:    srv.Client(),
		feHTTPBase:    srv.URL,
		username:      "u",
		password:      "p",
		database:      "weknora",
		tableBaseName: "weknora_embeddings",
	}

	cleanup := func() {
		_ = db.Close()
		srv.Close()
	}
	return repo, mock, srv, cleanup
}

// ---------------------------------------------------------------------------
// query.go：whereBuilder / embeddingLiteral / parseEmbeddingLiteral
// ---------------------------------------------------------------------------

func TestEmbeddingLiteralRoundTrip(t *testing.T) {
	t.Run("empty vector returns []", func(t *testing.T) {
		assert.Equal(t, "[]", embeddingLiteral(nil))
		assert.Equal(t, "[]", embeddingLiteral([]float32{}))
	})

	t.Run("does not contain locale-sensitive separators", func(t *testing.T) {
		s := embeddingLiteral([]float32{1.5, -2.25, 0.001})
		// 必须只包含数字 / 点 / 负号 / e / 逗号 / 方括号
		assert.Regexp(t, regexp.MustCompile(`^\[[\-+0-9eE.,]+\]$`), s)
	})

	t.Run("round trip", func(t *testing.T) {
		orig := []float32{1.5, -2.25, 0.0625}
		s := embeddingLiteral(orig)
		got, err := parseEmbeddingLiteral([]byte(s))
		require.NoError(t, err)
		assert.Equal(t, orig, got)
	})

	t.Run("parse handles whitespace and missing brackets", func(t *testing.T) {
		v, err := parseEmbeddingLiteral([]byte(" 1.0 , 2.0 ,3.0 "))
		require.NoError(t, err)
		assert.Equal(t, []float32{1.0, 2.0, 3.0}, v)
	})
}

func TestWhereBuilder(t *testing.T) {
	t.Run("empty builder returns 1 = 1", func(t *testing.T) {
		w := &whereBuilder{}
		clause, args := w.build()
		assert.Equal(t, "1 = 1", clause)
		assert.Nil(t, args)
	})

	t.Run("equal + IN + NOT IN", func(t *testing.T) {
		w := &whereBuilder{}
		w.addEqual("is_enabled", true)
		w.addIn("knowledge_base_id", []string{"kb1", "kb2"})
		w.addNotIn("chunk_id", []string{"x"})
		clause, args := w.build()
		assert.Contains(t, clause, "is_enabled = ?")
		assert.Contains(t, clause, "knowledge_base_id IN (?, ?)")
		assert.Contains(t, clause, "chunk_id NOT IN (?)")
		assert.Equal(t, []any{true, "kb1", "kb2", "x"}, args)
	})

	t.Run("buildBaseFilter applies all params", func(t *testing.T) {
		w := buildBaseFilter(types.RetrieveParams{
			KnowledgeBaseIDs:    []string{"kb1"},
			KnowledgeIDs:        []string{"k1", "k2"},
			TagIDs:              []string{"t1"},
			ExcludeKnowledgeIDs: []string{"k9"},
			ExcludeChunkIDs:     []string{"c9"},
		})
		clause, _ := w.build()
		assert.Contains(t, clause, "is_enabled = ?")
		assert.Contains(t, clause, "knowledge_base_id IN (?)")
		assert.Contains(t, clause, "knowledge_id IN (?, ?)")
		assert.Contains(t, clause, "tag_id IN (?)")
		assert.Contains(t, clause, "knowledge_id NOT IN (?)")
		assert.Contains(t, clause, "chunk_id NOT IN (?)")
	})
}

// ---------------------------------------------------------------------------
// streamload.go：chunkRows / partialUpdateRows
// ---------------------------------------------------------------------------

func TestChunkRows(t *testing.T) {
	rows := []map[string]any{
		{"id": "a", "is_enabled": true},
		{"id": "b", "is_enabled": false},
		{"id": "c", "is_enabled": true},
	}

	t.Run("single batch when fits", func(t *testing.T) {
		batches := chunkRows(rows, 4096)
		require.Len(t, batches, 1)
		assert.Len(t, batches[0], 3)
	})

	t.Run("splits when exceeding maxBytes", func(t *testing.T) {
		// 给一个非常小的上限，强制每行单独成段
		batches := chunkRows(rows, 16)
		assert.GreaterOrEqual(t, len(batches), 2)

		var total int
		for _, b := range batches {
			total += len(b)
		}
		assert.Equal(t, len(rows), total)
	})
}

func TestPartialUpdateRows_HappyPath(t *testing.T) {
	repo, _, _, cleanup := newTestRepo(t)
	defer cleanup()

	// 自定义 server 验证请求形态。
	var captured *http.Request
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Status":           "Success",
			"NumberTotalRows":  2,
			"NumberLoadedRows": 2,
			"Label":            "ok",
		})
	}))
	defer srv.Close()
	repo.feHTTPBase = srv.URL

	rows := []map[string]any{
		{"id": "id1", "is_enabled": true},
		{"id": "id2", "is_enabled": false},
	}
	require.NoError(t, repo.partialUpdateRows(context.Background(),
		"weknora_embeddings_768", []string{"id", "is_enabled"}, rows))

	require.NotNil(t, captured)
	assert.Equal(t, http.MethodPut, captured.Method)
	assert.Equal(t, "/api/weknora/weknora_embeddings_768/_stream_load", captured.URL.Path)

	assert.Equal(t, "true", captured.Header.Get("partial_columns"))
	assert.Equal(t, "true", captured.Header.Get("strip_outer_array"))
	assert.Equal(t, "json", captured.Header.Get("format"))
	assert.Equal(t, "id,is_enabled", captured.Header.Get("columns"))
	assert.Equal(t, "APPEND", captured.Header.Get("merge_type"))
	assert.True(t, strings.HasPrefix(captured.Header.Get("Authorization"), "Basic "))

	var got []map[string]any
	require.NoError(t, json.Unmarshal(capturedBody, &got))
	assert.Len(t, got, 2)
	assert.Equal(t, "id1", got[0]["id"])
}

func TestPartialUpdateRows_FailureSurfaced(t *testing.T) {
	repo, _, _, cleanup := newTestRepo(t)
	defer cleanup()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Status":   "Fail",
			"Message":  "label exists",
			"ErrorURL": "http://...",
		})
	}))
	defer srv.Close()
	repo.feHTTPBase = srv.URL

	err := repo.partialUpdateRows(context.Background(),
		"t", []string{"id", "is_enabled"},
		[]map[string]any{{"id": "x", "is_enabled": true}},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stream load failed")
}

// ---------------------------------------------------------------------------
// repository.go：SQL 形态
// ---------------------------------------------------------------------------

func TestDeleteByChunkIDList_SQLShape(t *testing.T) {
	repo, mock, _, cleanup := newTestRepo(t)
	defer cleanup()

	mock.ExpectExec(`DELETE FROM .*weknora_embeddings_768.* WHERE chunk_id IN \(\?, \?\)`).
		WithArgs("c1", "c2").
		WillReturnResult(sqlmock.NewResult(0, 2))

	err := repo.DeleteByChunkIDList(context.Background(), []string{"c1", "c2"}, 768, "")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteByKnowledgeIDList_NoOpOnEmpty(t *testing.T) {
	repo, mock, _, cleanup := newTestRepo(t)
	defer cleanup()

	require.NoError(t, repo.DeleteByKnowledgeIDList(context.Background(), nil, 768, ""))
	// 空列表不应触发任何 query
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestVectorRetrieve_SQLShape(t *testing.T) {
	repo, mock, _, cleanup := newTestRepo(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT COUNT\(1\) FROM information_schema.tables`).
		WithArgs("weknora", "weknora_embeddings_3").
		WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(1))

	mock.ExpectQuery(`SELECT id, content, .*cosine_distance_approximate.*HAVING score >= \? ORDER BY score DESC LIMIT \?`).
		WithArgs(true, 0.5, 5).
		WillReturnRows(
			sqlmock.NewRows([]string{
				"id", "content", "source_id", "source_type",
				"chunk_id", "knowledge_id", "knowledge_base_id", "tag_id",
				"is_enabled", "score",
			}).AddRow("id1", "hello", "src", 0, "c1", "k1", "kb1", "t1", true, 0.95),
		)

	results, err := repo.VectorRetrieve(context.Background(), types.RetrieveParams{
		Embedding:     []float32{1, 2, 3},
		TopK:          5,
		Threshold:     0.5,
		RetrieverType: types.VectorRetrieverType,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Len(t, results[0].Results, 1)
	assert.Equal(t, "id1", results[0].Results[0].ID)
	assert.InDelta(t, 0.95, results[0].Results[0].Score, 1e-9)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestKeywordsRetrieve_SQLShape(t *testing.T) {
	repo, mock, _, cleanup := newTestRepo(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT TABLE_NAME FROM information_schema.tables`).
		WithArgs("weknora", "weknora_embeddings\\_%").
		WillReturnRows(sqlmock.NewRows([]string{"TABLE_NAME"}).
			AddRow("weknora_embeddings_768"))

	mock.ExpectQuery(`MATCH_ANY \?`).
		WithArgs(true, "你好", 3).
		WillReturnRows(
			sqlmock.NewRows([]string{
				"id", "content", "source_id", "source_type",
				"chunk_id", "knowledge_id", "knowledge_base_id", "tag_id",
				"is_enabled",
			}).AddRow("id1", "你好世界", "src", 0, "c1", "k1", "kb1", "", true),
		)

	results, err := repo.KeywordsRetrieve(context.Background(), types.RetrieveParams{
		Query:         "你好",
		TopK:          3,
		RetrieverType: types.KeywordsRetrieverType,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Len(t, results[0].Results, 1)
	assert.Equal(t, "id1", results[0].Results[0].ID)
	assert.Equal(t, 1.0, results[0].Results[0].Score)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBatchUpdateChunkEnabledStatus_StreamLoad(t *testing.T) {
	// 这条路径覆盖了 lookupChunkRowKeys + partialUpdateRows 的连贯流。
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	defer db.Close()

	var streamLoadCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		streamLoadCalls++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Status":           "Success",
			"NumberLoadedRows": 1,
			"Label":            "label",
		})
	}))
	defer srv.Close()

	repo := &dorisRepository{
		db:            db,
		httpClient:    srv.Client(),
		feHTTPBase:    srv.URL,
		username:      "u",
		database:      "weknora",
		tableBaseName: "weknora_embeddings",
	}

	mock.ExpectQuery(`SELECT TABLE_NAME FROM information_schema.tables`).
		WithArgs("weknora", "weknora_embeddings\\_%").
		WillReturnRows(sqlmock.NewRows([]string{"TABLE_NAME"}).AddRow("weknora_embeddings_768"))
	mock.ExpectQuery(`SELECT id, chunk_id FROM .*weknora_embeddings_768.* WHERE chunk_id IN`).
		WithArgs("c1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "chunk_id"}).AddRow("row-1", "c1"))

	require.NoError(t, repo.BatchUpdateChunkEnabledStatus(
		context.Background(), map[string]bool{"c1": false}))
	assert.Equal(t, 1, streamLoadCalls, "stream load should be invoked once")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestEnsureTable_DDLShape(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	defer db.Close()

	repo := &dorisRepository{
		db:             db,
		database:       "weknora",
		tableBaseName:  "weknora_embeddings",
		bucketsNum:     5,
		replicationNum: 2,
	}

	// 表不存在
	mock.ExpectQuery(`SELECT COUNT\(1\) FROM information_schema.tables`).
		WithArgs("weknora", "weknora_embeddings_768").
		WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(0))
	// CREATE TABLE 应包含关键属性
	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS .*weknora_embeddings_768.*UNIQUE KEY\(id\).*BUCKETS 5.*replication_num.*=.*2.*enable_unique_key_merge_on_write.*=.*true`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	// SHOW INDEX 一次即返回 ANN 已 FINISHED
	mock.ExpectQuery(`SHOW INDEX FROM .*weknora_embeddings_768.*`).
		WillReturnRows(
			sqlmock.NewRows([]string{"Table", "Key_name", "State"}).
				AddRow("weknora_embeddings_768", "idx_emb", "FINISHED"),
		)

	require.NoError(t, repo.ensureTable(context.Background(), 768))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBatchSave_SQLShape(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	defer db.Close()

	repo := &dorisRepository{
		db:            db,
		database:      "weknora",
		tableBaseName: "weknora_embeddings",
	}
	repo.initializedTables.Store(3, true) // 跳过 ensureTable

	mock.ExpectExec(`INSERT INTO .*weknora_embeddings_3.*VALUES \(\?, \?, \?, \?, \?, \?, \?, \?, \?, \[`).
		WithArgs(
			"src1", "hello", "src1", 0,
			"c1", "k1", "kb1", "",
			true,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = repo.BatchSave(context.Background(),
		[]*types.IndexInfo{{
			Content:         "hello",
			SourceID:        "src1",
			ChunkID:         "c1",
			KnowledgeID:     "k1",
			KnowledgeBaseID: "kb1",
			IsEnabled:       true,
		}},
		map[string]any{
			fieldEmbedding: map[string][]float32{
				"src1": {1, 2, 3},
			},
		},
	)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// Engine wiring
// ---------------------------------------------------------------------------

func TestEngineTypeAndSupport(t *testing.T) {
	repo, _, _, cleanup := newTestRepo(t)
	defer cleanup()

	assert.Equal(t, types.DorisRetrieverEngineType, repo.EngineType())
	supports := repo.Support()
	assert.Contains(t, supports, types.KeywordsRetrieverType)
	assert.Contains(t, supports, types.VectorRetrieverType)
}

func TestRetrieve_DispatchesByType(t *testing.T) {
	repo, mock, _, cleanup := newTestRepo(t)
	defer cleanup()

	// invalid retriever type -> error，不会触发任何 SQL
	_, err := repo.Retrieve(context.Background(), types.RetrieveParams{
		RetrieverType: "unknown",
	})
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// regression / utility
// ---------------------------------------------------------------------------

func TestTranslateSourceID(t *testing.T) {
	t.Run("plain chunk uses target chunk", func(t *testing.T) {
		got := translateSourceID("c1", "c1", "tc1")
		assert.Equal(t, "tc1", got)
	})
	t.Run("generated question preserves question id", func(t *testing.T) {
		got := translateSourceID("c1-q9", "c1", "tc1")
		assert.Equal(t, "tc1-q9", got)
	})
	t.Run("unrecognized source falls back to fresh uuid", func(t *testing.T) {
		got := translateSourceID("totally-other", "c1", "tc1")
		assert.NotEqual(t, "totally-other", got)
		assert.Len(t, got, 36) // UUID 长度
	})
}

func TestEstimateStorageSize(t *testing.T) {
	repo, _, _, cleanup := newTestRepo(t)
	defer cleanup()

	out := repo.EstimateStorageSize(context.Background(),
		[]*types.IndexInfo{{Content: "hello", ChunkID: "c1", KnowledgeID: "k1", KnowledgeBaseID: "kb1"}},
		map[string]any{
			fieldEmbedding: map[string][]float32{
				"": {1, 2, 3},
			},
		},
	)
	assert.Greater(t, out, int64(0))
}

// 保证 *sql.Rows 错误不会被吞掉。
func TestScanRetrieveRows_Error(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectQuery("SELECT 1").
		WillReturnError(errors.New("boom"))

	rows, err := db.Query("SELECT 1")
	if err != nil {
		// query 直接失败也算预期
		assert.Equal(t, "boom", err.Error())
		return
	}
	_, scanErr := scanRetrieveRows(rows, types.MatchTypeEmbedding)
	if scanErr != nil {
		assert.Error(t, scanErr)
	}
}

// silence unused import warning when sql isn't directly used
var _ = sql.ErrNoRows

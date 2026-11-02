package postgres

import (
	"context"
	"errors"
	"regexp"
	"sync"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newKeywordTestRepository(t *testing.T) (*pgRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, mock.ExpectationsWereMet()); _ = db.Close() })
	gdb, err := gorm.Open(gormpostgres.New(gormpostgres.Config{Conn: db}), &gorm.Config{
		DisableAutomaticPing: true,
		Logger:               logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	repo := NewPostgresRetrieveEngineRepository(gdb).(*pgRepository)
	// These tests isolate score-function detection; HNSW scheduling has its
	// own tests and must not add asynchronous SQL to this mock connection.
	repo.autoIndex = false
	return repo, mock
}

// Exercise the actual query path for both backends, including scope filters,
// bound query text, score mapping, and reuse of the per-connection detection.
func TestKeywordsRetrieveScoreFunctionCompatibility(t *testing.T) {
	for _, scoreFunction := range []string{"paradedb.score", "pgsearch.score"} {
		t.Run(scoreFunction, func(t *testing.T) {
			repo, mock := newKeywordTestRepository(t)
			mock.ExpectQuery(regexp.QuoteMeta(keywordScoreFunctionSQL)).
				WillReturnRows(sqlmock.NewRows([]string{"score_function"}).AddRow(scoreFunction))
			params := types.RetrieveParams{
				RetrieverType: types.KeywordsRetrieverType,
				Query:         "关键词测试 ' OR 1=1 --", TopK: 5,
				KnowledgeBaseIDs: []string{"kb-test"}, KnowledgeIDs: []string{"doc-test"},
				TagIDs: []string{"tag-test"},
			}
			query := `SELECT ` + regexp.QuoteMeta(scoreFunction) + `\(id\) as score,.*` +
				`FROM "embeddings" WHERE "knowledge_base_id" = \$1.*` +
				`"knowledge_id" = \$2.*"tag_id" = \$3.*` +
				`content \|\|\| \$4.*is_enabled IS NULL OR is_enabled = \$5.*` +
				`ORDER BY "score" DESC LIMIT \$6`
			for range 2 {
				mock.ExpectQuery(query).
					WithArgs("kb-test", "doc-test", "tag-test", params.Query, true, 5).
					WillReturnRows(sqlmock.NewRows([]string{
						"id", "chunk_id", "knowledge_id", "knowledge_base_id", "tag_id", "score",
					}).
						AddRow(1, "chunk-test", "doc-test", "kb-test", "tag-test", 31.15))
				results, err := repo.Retrieve(context.Background(), params)
				require.NoError(t, err)
				require.Len(t, results, 1)
				require.Len(t, results[0].Results, 1)
				require.Equal(t, "chunk-test", results[0].Results[0].ChunkID)
				require.Equal(t, 31.15, results[0].Results[0].Score)
				require.Equal(t, types.MatchTypeKeywords, results[0].Results[0].MatchType)
			}
		})
	}
}

func TestKeywordScoreDetectionFailuresDoNotPoisonCache(t *testing.T) {
	for _, kind := range []string{"missing", "unexpected", "database_error"} {
		t.Run(kind, func(t *testing.T) {
			repo, mock := newKeywordTestRepository(t)
			probe := mock.ExpectQuery(regexp.QuoteMeta(keywordScoreFunctionSQL))
			cause := errors.New("catalog unavailable")
			switch kind {
			case "missing":
				probe.WillReturnRows(sqlmock.NewRows([]string{"score_function"}))
			case "unexpected":
				probe.WillReturnRows(sqlmock.NewRows([]string{"score_function"}).AddRow("untrusted.score"))
			default:
				probe.WillReturnError(cause)
			}
			results, err := repo.KeywordsRetrieve(context.Background(), types.RetrieveParams{Query: "关键词测试", TopK: 5})
			require.Nil(t, results)
			if kind == "database_error" {
				require.ErrorIs(t, err, cause)
			} else {
				require.ErrorContains(t, err, "check pg_search extension compatibility")
			}
			mock.ExpectQuery(regexp.QuoteMeta(keywordScoreFunctionSQL)).
				WillReturnRows(sqlmock.NewRows([]string{"score_function"}).AddRow("pgsearch.score"))
			function, err := repo.resolveKeywordScoreFunction(context.Background())
			require.NoError(t, err)
			require.Equal(t, "pgsearch.score", function)
		})
	}
}

func TestKeywordScoreDetectionConcurrentAndPerRepository(t *testing.T) {
	repo, mock := newKeywordTestRepository(t)
	mock.ExpectQuery(regexp.QuoteMeta(keywordScoreFunctionSQL)).
		WillReturnRows(sqlmock.NewRows([]string{"score_function"}).AddRow("paradedb.score"))
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			function, err := repo.resolveKeywordScoreFunction(context.Background())
			if err != nil || function != "paradedb.score" {
				t.Errorf("resolve score function = %q, %v", function, err)
			}
		})
	}
	wg.Wait()
	other, otherMock := newKeywordTestRepository(t)
	otherMock.ExpectQuery(regexp.QuoteMeta(keywordScoreFunctionSQL)).
		WillReturnRows(sqlmock.NewRows([]string{"score_function"}).AddRow("pgsearch.score"))
	function, err := other.resolveKeywordScoreFunction(context.Background())
	require.NoError(t, err)
	require.Equal(t, "pgsearch.score", function)
}

func TestKeywordScoreDetectionCanceledContextCanRetry(t *testing.T) {
	repo, mock := newKeywordTestRepository(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// A canceled caller must not cache a failed detection or block later calls.
	_, err := repo.resolveKeywordScoreFunction(ctx)
	require.ErrorIs(t, err, context.Canceled)
	mock.ExpectQuery(regexp.QuoteMeta(keywordScoreFunctionSQL)).
		WillReturnRows(sqlmock.NewRows([]string{"score_function"}).AddRow("paradedb.score"))
	function, err := repo.resolveKeywordScoreFunction(context.Background())
	require.NoError(t, err)
	require.Equal(t, "paradedb.score", function)
}

func TestKeywordsRetrieveQueryFailurePreservesResolvedFunction(t *testing.T) {
	repo, mock := newKeywordTestRepository(t)
	mock.ExpectQuery(regexp.QuoteMeta(keywordScoreFunctionSQL)).
		WillReturnRows(sqlmock.NewRows([]string{"score_function"}).AddRow("pgsearch.score"))
	query := `SELECT pgsearch\.score\(id\) as score,.*FROM "embeddings".*`
	cause := errors.New("keyword index unavailable")
	mock.ExpectQuery(query).WithArgs("关键词测试", true, 5).WillReturnError(cause)
	params := types.RetrieveParams{Query: "关键词测试", TopK: 5}
	results, err := repo.KeywordsRetrieve(context.Background(), params)
	require.Nil(t, results)
	require.ErrorIs(t, err, cause)

	// The catalog probe succeeded: retry only the retrieval, not detection.
	mock.ExpectQuery(query).WithArgs("关键词测试", true, 5).
		WillReturnRows(sqlmock.NewRows([]string{"id", "score"}))
	results, err = repo.KeywordsRetrieve(context.Background(), params)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Empty(t, results[0].Results)
}

func TestVectorRetrieveDoesNotRequireKeywordScoreFunction(t *testing.T) {
	for _, keywordFailed := range []bool{false, true} {
		name := "without_keyword_probe"
		if keywordFailed {
			name = "after_unsupported_keyword_probe"
		}
		t.Run(name, func(t *testing.T) {
			repo, mock := newKeywordTestRepository(t)
			if keywordFailed {
				mock.ExpectQuery(regexp.QuoteMeta(keywordScoreFunctionSQL)).
					WillReturnRows(sqlmock.NewRows([]string{"score_function"}))
				_, err := repo.KeywordsRetrieve(context.Background(), types.RetrieveParams{Query: "关键词测试", TopK: 5})
				require.Error(t, err)
			}

			// Expect only vector SQL: any score-function probe fails this test.
			mock.ExpectBegin()
			mock.ExpectExec("SET LOCAL hnsw.ef_search = 100").WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectExec("SET LOCAL hnsw.iterative_scan = strict_order").WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectQuery(`SELECT .*FROM embeddings.*`).
				WithArgs(sqlmock.AnyArg(), 2, true, 100, 0.5, 3).
				WillReturnRows(sqlmock.NewRows([]string{"id", "chunk_id", "score"}).AddRow(1, "vector-chunk", 0.9))
			mock.ExpectCommit()
			results, err := repo.Retrieve(context.Background(), types.RetrieveParams{
				RetrieverType: types.VectorRetrieverType,
				Embedding:     []float32{0.1, 0.2},
				Threshold:     0.5,
				TopK:          3,
			})
			require.NoError(t, err)
			require.Len(t, results, 1)
			require.Len(t, results[0].Results, 1)
			require.Equal(t, "vector-chunk", results[0].Results[0].ChunkID)
			require.Equal(t, types.MatchTypeEmbedding, results[0].Results[0].MatchType)
		})
	}
}

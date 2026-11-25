package postgres

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newVectorFallbackTestRepository(t *testing.T) (*pgRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		require.NoError(t, mock.ExpectationsWereMet())
		_ = db.Close()
	})
	gdb, err := gorm.Open(gormpostgres.New(gormpostgres.Config{Conn: db}), &gorm.Config{
		DisableAutomaticPing: true,
		Logger:               logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	repo := NewPostgresRetrieveEngineRepository(gdb).(*pgRepository)
	// Index scheduling has separate tests and must not add background SQL.
	repo.autoIndex = false
	return repo, mock
}

func fallbackTestParams() types.RetrieveParams {
	return types.RetrieveParams{Embedding: []float32{1, 0, 0}, TopK: 10}
}

func expectFallbackVectorQuery(mock sqlmock.Sqlmock) *sqlmock.ExpectedQuery {
	return mock.ExpectQuery(`SELECT .* FROM \(.* FROM embeddings `).
		WithArgs("[1,0,0]", 3, true, 100, 1.0, 10)
}

func emptyFallbackVectorRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "chunk_id", "score"})
}

func expectVectorSettingFailure(mock sqlmock.Sqlmock, setting string, failure error) {
	mock.ExpectBegin()
	if setting == "hnsw.ef_search" {
		mock.ExpectExec("SET LOCAL hnsw.ef_search = 100").WillReturnError(failure)
		return
	}
	mock.ExpectExec("SET LOCAL hnsw.ef_search = 100").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("SET LOCAL hnsw.iterative_scan = strict_order").WillReturnError(failure)
}

func expectVectorSettingSuccess(mock sqlmock.Sqlmock) {
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL hnsw.ef_search = 100").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("SET LOCAL hnsw.iterative_scan = strict_order").WillReturnResult(sqlmock.NewResult(0, 0))
}

func TestVectorRetrieveRollsBackSettingFailureBeforeRetry(t *testing.T) {
	for _, setting := range []string{"hnsw.ef_search", "hnsw.iterative_scan"} {
		t.Run(setting, func(t *testing.T) {
			repo, mock := newVectorFallbackTestRepository(t)
			failure := fmt.Errorf("unrecognized configuration parameter %q", setting)
			expectVectorSettingFailure(mock, setting, failure)
			mock.ExpectRollback()
			expectFallbackVectorQuery(mock).WillReturnRows(emptyFallbackVectorRows())
			_, err := repo.VectorRetrieve(context.Background(), fallbackTestParams())
			require.NoError(t, err)

			// A later request on the same connection establishes its own settings.
			expectVectorSettingSuccess(mock)
			expectFallbackVectorQuery(mock).WillReturnRows(emptyFallbackVectorRows())
			mock.ExpectCommit()
			_, err = repo.VectorRetrieve(context.Background(), fallbackTestParams())
			require.NoError(t, err)
		})
	}
}

func TestVectorRetrieveDoesNotRetryOtherTransactionFailures(t *testing.T) {
	for _, stage := range []string{"begin", "query", "commit"} {
		for _, setting := range []string{"hnsw.ef_search", "hnsw.iterative_scan"} {
			t.Run(stage+"/"+setting, func(t *testing.T) {
				repo, mock := newVectorFallbackTestRepository(t)
				// Fault injection: naming a GUC does not make this a SET failure.
				failure := fmt.Errorf("%s failure mentioning %s", stage, setting)
				if stage == "begin" {
					mock.ExpectBegin().WillReturnError(failure)
				} else {
					expectVectorSettingSuccess(mock)
					query := expectFallbackVectorQuery(mock)
					if stage == "query" {
						query.WillReturnError(failure)
						mock.ExpectRollback()
					} else {
						query.WillReturnRows(emptyFallbackVectorRows())
						mock.ExpectCommit().WillReturnError(failure)
					}
				}
				// No compatibility query is expected after the failed transaction.
				got, err := repo.VectorRetrieve(context.Background(), fallbackTestParams())
				require.ErrorIs(t, err, failure)
				require.Nil(t, got)
			})
		}
	}
}

func TestVectorRetrieveReturnsUnrelatedSettingFailure(t *testing.T) {
	for _, setting := range []string{"hnsw.ef_search", "hnsw.iterative_scan"} {
		t.Run(setting, func(t *testing.T) {
			repo, mock := newVectorFallbackTestRepository(t)
			failure := errors.New("connection interrupted")
			expectVectorSettingFailure(mock, setting, failure)
			mock.ExpectRollback()
			got, err := repo.VectorRetrieve(context.Background(), fallbackTestParams())
			require.ErrorIs(t, err, failure)
			require.Nil(t, got)
		})
	}
}

func TestVectorRetrieveReturnsCompatibilityQueryFailure(t *testing.T) {
	repo, mock := newVectorFallbackTestRepository(t)
	expectVectorSettingFailure(mock, "hnsw.ef_search",
		errors.New("unrecognized configuration parameter hnsw.ef_search"))
	mock.ExpectRollback()
	failure := errors.New("query connection unavailable")
	expectFallbackVectorQuery(mock).WillReturnError(failure)
	got, err := repo.VectorRetrieve(context.Background(), fallbackTestParams())
	require.ErrorIs(t, err, failure)
	require.Nil(t, got)
}

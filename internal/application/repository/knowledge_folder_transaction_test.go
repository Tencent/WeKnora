package repository

import (
	"time"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestKnowledgeFolderAdvisoryLockKeyIsStableAndScoped(t *testing.T) {
	first := knowledgeFolderAdvisoryLockKey(7, "kb-1")
	assert.Equal(t, first, knowledgeFolderAdvisoryLockKey(7, "kb-1"))
	assert.NotEqual(t, first, knowledgeFolderAdvisoryLockKey(7, "kb-2"))
	assert.NotEqual(t, first, knowledgeFolderAdvisoryLockKey(8, "kb-1"))
}

func TestKnowledgeFolderPostgresTransactionBindsAdvisoryLockKey(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	require.NoError(t, err)
	repo := NewKnowledgeFolderRepository(db)
	lockKey := knowledgeFolderAdvisoryLockKey(7, "kb-1")

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_xact_lock($1)")).
		WithArgs(lockKey).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT .* FROM "knowledge_bases".*FOR UPDATE`).
		WithArgs(uint64(7), "kb-1", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("kb-1"))
	mock.ExpectCommit()

	called := false
	err = repo.RunTreeWriteTransaction(
		context.Background(),
		7,
		"kb-1",
		func(interfaces.KnowledgeFolderTreeRepository) error {
			called = true
			return nil
		},
	)
	require.NoError(t, err)
	assert.True(t, called)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestKnowledgeFolderSQLiteTransactionLocksScopedKnowledgeBase(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	repo := NewKnowledgeFolderRepository(db)
	require.NoError(t, db.Exec(
		`INSERT INTO knowledge_bases (id, tenant_id) VALUES ('kb-1', 1)`,
	).Error)

	called := 0
	err := repo.RunTreeWriteTransaction(
		context.Background(),
		1,
		"kb-1",
		func(interfaces.KnowledgeFolderTreeRepository) error {
			called++
			return nil
		},
	)
	require.NoError(t, err)
	assert.Equal(t, 1, called)

	err = repo.RunTreeWriteTransaction(
		context.Background(),
		2,
		"kb-1",
		func(interfaces.KnowledgeFolderTreeRepository) error {
			called++
			return nil
		},
	)
	require.ErrorIs(t, err, ErrKnowledgeFolderKnowledgeBaseNotFound)
	assert.Equal(t, 1, called)
}

func TestKnowledgeFolderTreeTransactionRejectsEmptyScope(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	repo := NewKnowledgeFolderRepository(db)
	callbackCalls := 0
	callback := func(interfaces.KnowledgeFolderTreeRepository) error {
		callbackCalls++
		return nil
	}

	err := repo.RunTreeWriteTransaction(context.Background(), 0, "kb-1", callback)
	require.ErrorIs(t, err, ErrKnowledgeFolderInvalid)
	err = repo.RunTreeWriteTransaction(context.Background(), 1, "", callback)
	require.ErrorIs(t, err, ErrKnowledgeFolderInvalid)
	assert.Zero(t, callbackCalls)
}

func TestKnowledgeFolderRepositorySeparatesOuterAndTreeCapabilities(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	repo := NewKnowledgeFolderRepository(db)
	require.NoError(t, db.Exec(
		`INSERT INTO knowledge_bases (id, tenant_id) VALUES ('kb-1', 1)`,
	).Error)

	_, outerExposesTreeWrites := repo.(interfaces.KnowledgeFolderTreeRepository)
	assert.False(t, outerExposesTreeWrites)

	treeExposesOuterTransaction := false
	err := repo.RunTreeWriteTransaction(
		context.Background(),
		1,
		"kb-1",
		func(txRepo interfaces.KnowledgeFolderTreeRepository) error {
			_, treeExposesOuterTransaction = txRepo.(interfaces.KnowledgeFolderRepository)
			return nil
		},
	)
	require.NoError(t, err)
	assert.False(t, treeExposesOuterTransaction)
}

func TestKnowledgeFolderSQLiteBusyClassificationAndBoundedWait(t *testing.T) {
	assert.True(t, isSQLiteBusyOrLocked(sqlite3.Error{Code: sqlite3.ErrBusy}))
	assert.True(t, isSQLiteBusyOrLocked(fmt.Errorf(
		"wrapped: %w",
		sqlite3.Error{Code: sqlite3.ErrLocked},
	)))
	assert.False(t, isSQLiteBusyOrLocked(sqlite3.Error{Code: sqlite3.ErrConstraint}))
	assert.False(t, isSQLiteBusyOrLocked(context.Canceled))
	assert.Equal(t, 3, knowledgeFolderSQLiteMaxAttempts)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, waitKnowledgeFolderSQLiteRetry(ctx, 0), context.Canceled)
}

func TestKnowledgeFolderSQLiteTransactionReplaysCallbackBusyErrors(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	repo := NewKnowledgeFolderRepository(db)
	require.NoError(t, db.Exec(
		`INSERT INTO knowledge_bases (id, tenant_id) VALUES ('kb-1', 1)`,
	).Error)

	callbackCalls := 0
	err := repo.RunTreeWriteTransaction(
		context.Background(),
		1,
		"kb-1",
		func(interfaces.KnowledgeFolderTreeRepository) error {
			callbackCalls++
			switch callbackCalls {
			case 1:
				return sqlite3.Error{Code: sqlite3.ErrBusy}
			case 2:
				return sqlite3.Error{Code: sqlite3.ErrLocked}
			default:
				return nil
			}
		},
	)
	require.NoError(t, err)
	assert.Equal(t, 3, callbackCalls)
}

func TestKnowledgeFolderSQLiteTransactionStopsRetryForBusinessErrorAndCancellation(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	repo := NewKnowledgeFolderRepository(db)
	require.NoError(t, db.Exec(
		`INSERT INTO knowledge_bases (id, tenant_id) VALUES ('kb-1', 1)`,
	).Error)

	t.Run("business error", func(t *testing.T) {
		businessErr := errors.New("business validation failed")
		callbackCalls := 0
		err := repo.RunTreeWriteTransaction(
			context.Background(),
			1,
			"kb-1",
			func(interfaces.KnowledgeFolderTreeRepository) error {
				callbackCalls++
				return businessErr
			},
		)
		require.ErrorIs(t, err, businessErr)
		assert.Equal(t, 1, callbackCalls)
	})

	t.Run("context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		callbackCalls := 0
		err := repo.RunTreeWriteTransaction(
			ctx,
			1,
			"kb-1",
			func(interfaces.KnowledgeFolderTreeRepository) error {
				callbackCalls++
				cancel()
				return sqlite3.Error{Code: sqlite3.ErrBusy}
			},
		)
		require.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, 1, callbackCalls)
	})
}

func TestKnowledgeFolderSQLiteTransactionRetriesSameRunAfterWriterLockRelease(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "tree-lock.db")
	dsn := dbPath + "?_journal_mode=WAL&_busy_timeout=1"
	dbA, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	dbB, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDBA, err := dbA.DB()
	require.NoError(t, err)
	sqlDBB, err := dbB.DB()
	require.NoError(t, err)
	sqlDBA.SetMaxOpenConns(1)
	sqlDBB.SetMaxOpenConns(1)
	t.Cleanup(func() {
		_ = sqlDBA.Close()
		_ = sqlDBB.Close()
	})

	require.NoError(t, dbA.Exec(knowledgeFolderTestDDL).Error)
	require.NoError(t, dbA.Exec(
		`INSERT INTO knowledge_bases (id, tenant_id) VALUES (?, ?)`,
		"kb-1",
		1,
	).Error)

	lockingTransaction := dbA.Begin()
	require.NoError(t, lockingTransaction.Error)
	require.NoError(t, lockingTransaction.
		Model(&types.KnowledgeBase{}).
		Where("tenant_id = ? AND id = ?", 1, "kb-1").
		UpdateColumn("id", gorm.Expr("id")).Error)
	t.Cleanup(func() { _ = lockingTransaction.Rollback().Error })

	repoB := newKnowledgeFolderRepository(dbB)
	waitCalls := 0
	repoB.sqliteRetryWait = func(ctx context.Context, attempt int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		waitCalls++
		if attempt != 0 {
			return fmt.Errorf("unexpected SQLite retry wait attempt: %d", attempt)
		}
		return lockingTransaction.Rollback().Error
	}

	callbackCalls := 0
	err = repoB.RunTreeWriteTransaction(
		context.Background(),
		1,
		"kb-1",
		func(interfaces.KnowledgeFolderTreeRepository) error {
			callbackCalls++
			return nil
		},
	)
	require.NoError(t, err)
	assert.Equal(t, 1, waitCalls)
	assert.Equal(t, 1, callbackCalls)
}

func TestKnowledgeFolderSQLiteTransactionExhaustsRealWriterContention(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "tree-lock.db")
	dsn := dbPath + "?_journal_mode=WAL&_busy_timeout=1"
	dbA, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	dbB, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDBA, err := dbA.DB()
	require.NoError(t, err)
	sqlDBB, err := dbB.DB()
	require.NoError(t, err)
	sqlDBA.SetMaxOpenConns(1)
	sqlDBB.SetMaxOpenConns(1)
	t.Cleanup(func() {
		_ = sqlDBA.Close()
		_ = sqlDBB.Close()
	})

	require.NoError(t, dbA.Exec(knowledgeFolderTestDDL).Error)
	require.NoError(t, dbA.Exec(
		`INSERT INTO knowledge_bases (id, tenant_id) VALUES (?, ?)`,
		"kb-1",
		1,
	).Error)

	lockingTransaction := dbA.Begin()
	require.NoError(t, lockingTransaction.Error)
	require.NoError(t, lockingTransaction.
		Model(&types.KnowledgeBase{}).
		Where("tenant_id = ? AND id = ?", 1, "kb-1").
		UpdateColumn("id", gorm.Expr("id")).Error)
	t.Cleanup(func() { _ = lockingTransaction.Rollback().Error })

	repoB := NewKnowledgeFolderRepository(dbB)
	callbackCalls := 0
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err = repoB.RunTreeWriteTransaction(
		ctx,
		1,
		"kb-1",
		func(interfaces.KnowledgeFolderTreeRepository) error {
			callbackCalls++
			return nil
		},
	)
	assert.True(t, isSQLiteBusyOrLocked(err), "expected typed SQLite busy/locked error, got %v", err)
	assert.Zero(t, callbackCalls)
}

func TestKnowledgeFolderSQLiteRetryExecutor(t *testing.T) {
	t.Run("bounded busy retries", func(t *testing.T) {
		attempts := 0
		waitAttempts := make([]int, 0, knowledgeFolderSQLiteMaxAttempts-1)
		err := runKnowledgeFolderSQLiteRetry(
			context.Background(),
			func() error {
				attempts++
				return sqlite3.Error{Code: sqlite3.ErrBusy}
			},
			func(_ context.Context, attempt int) error {
				waitAttempts = append(waitAttempts, attempt)
				return nil
			},
		)
		assert.True(t, isSQLiteBusyOrLocked(err))
		assert.Equal(t, knowledgeFolderSQLiteMaxAttempts, attempts)
		assert.Equal(t, []int{0, 1}, waitAttempts)
	})

	t.Run("success stops retry", func(t *testing.T) {
		attempts := 0
		waits := 0
		err := runKnowledgeFolderSQLiteRetry(
			context.Background(),
			func() error {
				attempts++
				if attempts == 1 {
					return sqlite3.Error{Code: sqlite3.ErrLocked}
				}
				return nil
			},
			func(context.Context, int) error {
				waits++
				return nil
			},
		)
		require.NoError(t, err)
		assert.Equal(t, 2, attempts)
		assert.Equal(t, 1, waits)
	})

	t.Run("context cancellation stops wait", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		attempts := 0
		waits := 0
		err := runKnowledgeFolderSQLiteRetry(
			ctx,
			func() error {
				attempts++
				cancel()
				return sqlite3.Error{Code: sqlite3.ErrBusy}
			},
			func(ctx context.Context, _ int) error {
				waits++
				return ctx.Err()
			},
		)
		require.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, 1, attempts)
		assert.Equal(t, 1, waits)
	})

	t.Run("business error is not retried", func(t *testing.T) {
		businessErr := errors.New("business validation failed")
		attempts := 0
		waits := 0
		err := runKnowledgeFolderSQLiteRetry(
			context.Background(),
			func() error {
				attempts++
				return businessErr
			},
			func(context.Context, int) error {
				waits++
				return nil
			},
		)
		require.ErrorIs(t, err, businessErr)
		assert.Equal(t, 1, attempts)
		assert.Zero(t, waits)
	})
}

func TestIsKnowledgeFolderUniqueViolationUsesTypedDriverErrors(t *testing.T) {
	assert.True(t, IsKnowledgeFolderUniqueViolation(&pgconn.PgError{Code: "23505"}))
	assert.False(t, IsKnowledgeFolderUniqueViolation(&pgconn.PgError{Code: "23503"}))
	assert.True(t, IsKnowledgeFolderUniqueViolation(sqlite3.Error{
		Code:         sqlite3.ErrConstraint,
		ExtendedCode: sqlite3.ErrConstraintUnique,
	}))
	assert.True(t, IsKnowledgeFolderUniqueViolation(sqlite3.Error{
		Code:         sqlite3.ErrConstraint,
		ExtendedCode: sqlite3.ErrConstraintPrimaryKey,
	}))
	assert.False(t, IsKnowledgeFolderUniqueViolation(sqlite3.Error{
		Code:         sqlite3.ErrConstraint,
		ExtendedCode: sqlite3.ErrConstraintForeignKey,
	}))
}

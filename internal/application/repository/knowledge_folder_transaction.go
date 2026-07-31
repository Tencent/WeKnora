package repository

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/mattn/go-sqlite3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	knowledgeFolderSQLiteMaxAttempts = 3
	knowledgeFolderSQLiteRetryDelay  = 10 * time.Millisecond
)

var (
	ErrKnowledgeFolderKnowledgeBaseNotFound = errors.New("knowledge folder knowledge base not found")
	ErrKnowledgeFolderUnsupportedDialect    = errors.New("unsupported knowledge folder database dialect")
)

type knowledgeFolderSQLiteWaitFunc func(context.Context, int) error

// RunTreeWriteTransaction serializes folder tree writes within a knowledge base.
func (r *knowledgeFolderRepository) RunTreeWriteTransaction(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	fn interfaces.KnowledgeFolderTreeWriteFunc,
) error {
	if fn == nil {
		return fmt.Errorf("%w: transaction callback is nil", ErrKnowledgeFolderInvalid)
	}
	if ctx == nil {
		return fmt.Errorf("%w: context is nil", ErrKnowledgeFolderInvalid)
	}
	return r.runKnowledgeFolderScopedWriteTransaction(
		ctx,
		tenantID,
		kbID,
		func(tx *gorm.DB) error {
			return fn(newKnowledgeFolderTreeRepository(tx))
		},
	)
}

// RunKnowledgeFolderMoveTransaction serializes knowledge placement changes
// with folder tree writes in the same knowledge-base scope.
func (r *knowledgeFolderRepository) RunKnowledgeFolderMoveTransaction(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	fn interfaces.KnowledgeFolderMoveWriteFunc,
) error {
	if fn == nil {
		return fmt.Errorf("%w: transaction callback is nil", ErrKnowledgeFolderMoveInvalid)
	}
	if ctx == nil {
		return fmt.Errorf("%w: context is nil", ErrKnowledgeFolderMoveInvalid)
	}
	if tenantID == 0 || kbID == "" {
		return fmt.Errorf("%w: knowledge folder scope is empty", ErrKnowledgeFolderMoveInvalid)
	}
	return r.runKnowledgeFolderScopedWriteTransaction(
		ctx,
		tenantID,
		kbID,
		func(tx *gorm.DB) error {
			return fn(newKnowledgeFolderMoveWriteRepository(tx))
		},
	)
}

type knowledgeBaseLockMode uint8

const (
	knowledgeBaseLockActiveOnly knowledgeBaseLockMode = iota
	knowledgeBaseLockIncludeSoftDeleted
)

type knowledgeBaseScopedWriteOptions struct {
	lockMode        knowledgeBaseLockMode
	sqliteRetryWait knowledgeFolderSQLiteWaitFunc
}

type knowledgeBaseScopedWriteFunc func(tx *gorm.DB) error

func (r *knowledgeFolderRepository) runKnowledgeFolderScopedWriteTransaction(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	fn knowledgeBaseScopedWriteFunc,
) error {
	var db *gorm.DB
	var sqliteRetryWait knowledgeFolderSQLiteWaitFunc
	if r != nil && r.knowledgeFolderReader != nil {
		db = r.db
		sqliteRetryWait = r.sqliteRetryWait
	}

	return runKnowledgeBaseScopedWriteTransaction(
		ctx,
		db,
		tenantID,
		kbID,
		knowledgeBaseScopedWriteOptions{
			lockMode:        knowledgeBaseLockActiveOnly,
			sqliteRetryWait: sqliteRetryWait,
		},
		fn,
	)
}

func runKnowledgeBaseScopedWriteTransaction(
	ctx context.Context,
	db *gorm.DB,
	tenantID uint64,
	kbID string,
	options knowledgeBaseScopedWriteOptions,
	fn knowledgeBaseScopedWriteFunc,
) error {
	if fn == nil {
		return fmt.Errorf("%w: scoped transaction callback is nil", ErrKnowledgeFolderInvalid)
	}
	if ctx == nil {
		return fmt.Errorf("%w: context is nil", ErrKnowledgeFolderInvalid)
	}
	if tenantID == 0 || kbID == "" {
		return fmt.Errorf("%w: knowledge folder scope is empty", ErrKnowledgeFolderInvalid)
	}
	if options.lockMode != knowledgeBaseLockActiveOnly &&
		options.lockMode != knowledgeBaseLockIncludeSoftDeleted {
		return fmt.Errorf("%w: invalid knowledge base lock mode", ErrKnowledgeFolderInvalid)
	}
	if db == nil || db.Config == nil || db.Dialector == nil {
		return ErrKnowledgeFolderUnsupportedDialect
	}

	switch db.Dialector.Name() {
	case "postgres":
		return runPostgresKnowledgeBaseScopedWriteTransaction(
			ctx,
			db,
			tenantID,
			kbID,
			options.lockMode,
			fn,
		)
	case "sqlite":
		return runSQLiteKnowledgeBaseScopedWriteTransaction(
			ctx,
			db,
			tenantID,
			kbID,
			options,
			fn,
		)
	default:
		return fmt.Errorf(
			"%w: %s",
			ErrKnowledgeFolderUnsupportedDialect,
			db.Dialector.Name(),
		)
	}
}

func runPostgresKnowledgeBaseScopedWriteTransaction(
	ctx context.Context,
	db *gorm.DB,
	tenantID uint64,
	kbID string,
	lockMode knowledgeBaseLockMode,
	fn knowledgeBaseScopedWriteFunc,
) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		lockKey := knowledgeFolderAdvisoryLockKey(tenantID, kbID)
		if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", lockKey).Error; err != nil {
			return err
		}
		if err := lockKnowledgeBaseForScopedWrite(
			ctx,
			tx,
			tenantID,
			kbID,
			lockMode,
		); err != nil {
			return err
		}
		return fn(tx)
	})
}

func runSQLiteKnowledgeBaseScopedWriteTransaction(
	ctx context.Context,
	db *gorm.DB,
	tenantID uint64,
	kbID string,
	options knowledgeBaseScopedWriteOptions,
	fn knowledgeBaseScopedWriteFunc,
) error {
	waitFn := options.sqliteRetryWait
	if waitFn == nil {
		waitFn = waitKnowledgeFolderSQLiteRetry
	}
	return runKnowledgeFolderSQLiteRetry(
		ctx,
		func() error {
			return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				lockQuery := tx.Model(&types.KnowledgeBase{})
				if options.lockMode == knowledgeBaseLockIncludeSoftDeleted {
					lockQuery = lockQuery.Unscoped()
				}
				result := lockQuery.
					Where("tenant_id = ? AND id = ?", tenantID, kbID).
					UpdateColumn("id", gorm.Expr("id"))
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected != 1 {
					return ErrKnowledgeFolderKnowledgeBaseNotFound
				}
				return fn(tx)
			})
		},
		waitFn,
	)
}

func runKnowledgeFolderSQLiteRetry(
	ctx context.Context,
	attemptFn func() error,
	waitFn func(context.Context, int) error,
) error {
	var lastErr error
	for attempt := 0; attempt < knowledgeFolderSQLiteMaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		lastErr = attemptFn()
		if lastErr == nil || !isSQLiteBusyOrLocked(lastErr) {
			return lastErr
		}
		if attempt+1 == knowledgeFolderSQLiteMaxAttempts {
			break
		}
		if err := waitFn(ctx, attempt); err != nil {
			return err
		}
	}
	return lastErr
}

func lockKnowledgeBaseForScopedWrite(
	ctx context.Context,
	tx *gorm.DB,
	tenantID uint64,
	kbID string,
	lockMode knowledgeBaseLockMode,
) error {
	var row struct {
		ID string
	}
	lockQuery := tx.WithContext(ctx).Model(&types.KnowledgeBase{})
	if lockMode == knowledgeBaseLockIncludeSoftDeleted {
		lockQuery = lockQuery.Unscoped()
	}
	err := lockQuery.
		Select("id").
		Where("tenant_id = ? AND id = ?", tenantID, kbID).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrKnowledgeFolderKnowledgeBaseNotFound
	}
	return err
}

func knowledgeFolderAdvisoryLockKey(tenantID uint64, kbID string) int64 {
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte("weknora:knowledge-folder-tree:v1\x00"))
	var tenantBytes [8]byte
	binary.BigEndian.PutUint64(tenantBytes[:], tenantID)
	_, _ = hasher.Write(tenantBytes[:])
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write([]byte(kbID))
	return int64(hasher.Sum64())
}

func isSQLiteBusyOrLocked(err error) bool {
	var sqliteErr sqlite3.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	return sqliteErr.Code == sqlite3.ErrBusy || sqliteErr.Code == sqlite3.ErrLocked
}

func waitKnowledgeFolderSQLiteRetry(ctx context.Context, attempt int) error {
	delay := time.Duration(attempt+1) * knowledgeFolderSQLiteRetryDelay
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

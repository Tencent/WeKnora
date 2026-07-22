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
	if tenantID == 0 || kbID == "" {
		return fmt.Errorf("%w: knowledge folder scope is empty", ErrKnowledgeFolderInvalid)
	}
	if r == nil ||
		r.knowledgeFolderReader == nil ||
		r.db == nil ||
		r.db.Dialector == nil {
		return ErrKnowledgeFolderUnsupportedDialect
	}

	switch r.db.Dialector.Name() {
	case "postgres":
		return r.runPostgresTreeWriteTransaction(ctx, tenantID, kbID, fn)
	case "sqlite":
		return r.runSQLiteTreeWriteTransaction(ctx, tenantID, kbID, fn)
	default:
		return fmt.Errorf(
			"%w: %s",
			ErrKnowledgeFolderUnsupportedDialect,
			r.db.Dialector.Name(),
		)
	}
}

func (r *knowledgeFolderRepository) runPostgresTreeWriteTransaction(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	fn interfaces.KnowledgeFolderTreeWriteFunc,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		lockKey := knowledgeFolderAdvisoryLockKey(tenantID, kbID)
		if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", lockKey).Error; err != nil {
			return err
		}
		if err := lockKnowledgeFolderKnowledgeBase(ctx, tx, tenantID, kbID); err != nil {
			return err
		}
		return fn(newKnowledgeFolderTreeRepository(tx))
	})
}

func (r *knowledgeFolderRepository) runSQLiteTreeWriteTransaction(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	fn interfaces.KnowledgeFolderTreeWriteFunc,
) error {
	waitFn := r.sqliteRetryWait
	if waitFn == nil {
		waitFn = waitKnowledgeFolderSQLiteRetry
	}
	return runKnowledgeFolderSQLiteRetry(
		ctx,
		func() error {
			return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				result := tx.Model(&types.KnowledgeBase{}).
					Where("tenant_id = ? AND id = ?", tenantID, kbID).
					UpdateColumn("id", gorm.Expr("id"))
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected == 0 {
					return ErrKnowledgeFolderKnowledgeBaseNotFound
				}
				return fn(newKnowledgeFolderTreeRepository(tx))
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

func lockKnowledgeFolderKnowledgeBase(
	ctx context.Context,
	tx *gorm.DB,
	tenantID uint64,
	kbID string,
) error {
	var row struct {
		ID string
	}
	err := tx.WithContext(ctx).
		Model(&types.KnowledgeBase{}).
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

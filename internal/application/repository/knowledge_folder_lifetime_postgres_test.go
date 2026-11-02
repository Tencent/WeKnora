package repository

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type folderLifetimePauseCreateKey struct{}

// Observe actual PostgreSQL row-lock waits, not scheduler timing: creation must
// hold its live-KB check until commit, while a deletion that already holds the
// row must make a delayed creator re-evaluate the soft-delete predicate.
func TestKnowledgeFolderPostgresCreateSerializesWithKnowledgeBaseDeletion(t *testing.T) {
	for _, createFirst := range []bool{true, false} {
		name := "delete waits for create"
		if !createFirst {
			name = "create waits for delete"
		}
		t.Run(name, func(t *testing.T) {
			db := folderV3TransferPGDB(t)
			repo := NewKnowledgeRepository(db)
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			held, release := make(chan struct{}), make(chan struct{})
			var heldOnce, releaseOnce sync.Once
			releaseLock := func() { releaseOnce.Do(func() { close(release) }) }
			defer releaseLock()
			pause := func() error {
				heldOnce.Do(func() { close(held) })
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			createResult, deleteResult := make(chan error, 1), make(chan error, 1)
			if createFirst {
				callback := "folder_lifetime_pause_live_kb"
				require.NoError(t, db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
					if tx.Statement.Context.Value(folderLifetimePauseCreateKey{}) == true &&
						tx.Statement.Table == "knowledge_bases" && tx.Error == nil {
						_ = tx.AddError(pause())
					}
				}))
				defer func() { require.NoError(t, db.Callback().Query().Remove(callback)) }()
				go func() {
					createResult <- repo.CreateKnowledgeFolder(
						context.WithValue(ctx, folderLifetimePauseCreateKey{}, true), 7, "source", "empty",
					)
				}()
			} else {
				go func() {
					deleteResult <- db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
						if err := tx.Where("id = ? AND tenant_id = ?", "source", 7).
							Delete(&types.KnowledgeBase{}).Error; err != nil {
							return err
						}
						return pause()
					})
				}()
			}
			select {
			case <-held:
			case <-ctx.Done():
				t.Fatal("first operation did not reach its held knowledge-base row lock")
			}
			if createFirst {
				go func() {
					deleteResult <- db.WithContext(ctx).Where("id = ? AND tenant_id = ?", "source", 7).
						Delete(&types.KnowledgeBase{}).Error
				}()
				folderLifetimeRequireRowWait(ctx, t, db, deleteResult)
			} else {
				go func() { createResult <- repo.CreateKnowledgeFolder(ctx, 7, "source", "empty") }()
				folderLifetimeRequireRowWait(ctx, t, db, createResult)
			}
			releaseLock()
			require.NoError(t, <-deleteResult)
			createErr := <-createResult
			var beforeCleanup int64
			require.NoError(t, db.Model(&types.KnowledgeFolder{}).
				Where("knowledge_base_id = ?", "source").Count(&beforeCleanup).Error)
			if createFirst {
				require.NoError(t, createErr)
				require.EqualValues(t, 1, beforeCleanup)
			} else {
				require.ErrorIs(t, createErr, ErrKnowledgeFolderNotFound)
				require.Zero(t, beforeCleanup)
			}
			// Production cleanup runs only after the soft-delete has committed.
			require.NoError(t, repo.DeleteKnowledgeFolders(ctx, 7, "source"))
			require.ErrorIs(t, repo.CreateKnowledgeFolder(ctx, 7, "source", "late"), ErrKnowledgeFolderNotFound)
			var afterCleanup int64
			require.NoError(t, db.Model(&types.KnowledgeFolder{}).
				Where("knowledge_base_id = ?", "source").Count(&afterCleanup).Error)
			require.Zero(t, afterCleanup)
			require.NoError(t, repo.CreateKnowledgeFolder(ctx, 7, "target", "empty"))
		})
	}
}

func folderLifetimeRequireRowWait(ctx context.Context, t *testing.T, db *gorm.DB, result <-chan error) {
	t.Helper()
	var application string
	require.NoError(t, db.Raw("SELECT current_setting('application_name')").Scan(&application).Error)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-result:
			t.Fatalf("second operation finished before the knowledge-base row lock was released: %v", err)
		case <-ticker.C:
			var waiting bool
			require.NoError(t, db.WithContext(ctx).Raw(`SELECT EXISTS (SELECT 1 FROM pg_stat_activity
				WHERE datname = current_database() AND application_name = ? AND pid <> pg_backend_pid()
				AND state = 'active' AND wait_event_type = 'Lock'
				AND wait_event IN ('transactionid', 'tuple') AND query LIKE '%knowledge_bases%')`, application).
				Scan(&waiting).Error)
			if waiting {
				return
			}
		case <-ctx.Done():
			t.Fatal("second operation did not reach an observable PostgreSQL knowledge-base row-lock wait")
		}
	}
}

package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const wikiSourceAttemptWaitPollInterval = 10 * time.Millisecond

type wikiSourceAttemptWaiterBackend struct {
	pid int
	err error
}

// Observe an actual ungranted exclusive advisory lock blocked by this writer.
// Polling controls readiness; elapsed time alone never authorizes the commit.
func waitWikiSourceAttemptExclusiveGate(
	ctx context.Context, db *gorm.DB, waiterPID, writerPID int, exited <-chan struct{},
) error {
	ticker := time.NewTicker(wikiSourceAttemptWaitPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-exited:
			return errors.New(
				"authorization returned before its exclusive source gate was observed waiting",
			)
		default:
		}
		var blocked bool
		if err := db.WithContext(ctx).Raw(`SELECT EXISTS (
			SELECT 1 FROM pg_locks WHERE pid = ? AND locktype = 'advisory'
			AND mode = 'ExclusiveLock' AND NOT granted
		) AND ? = ANY(pg_blocking_pids(?))`, waiterPID, writerPID, waiterPID).
			Scan(&blocked).Error; err != nil {
			return err
		}
		if blocked {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("observe exclusive source gate wait: %w", ctx.Err())
		case <-exited:
			return errors.New("authorization returned without waiting for the source writer")
		case <-ticker.C:
		}
	}
}

// The inverse interleaving must not reuse a snapshot taken before a gate wait:
// writer INSERT -> Wiki waits -> writer COMMIT -> fresh MAX rejects the old claim.
func TestWikiSourceAttemptWaiterReadsFreshSnapshotAfterWriterCommit(t *testing.T) {
	for _, operation := range []string{"publication", "handoff"} {
		t.Run(operation, func(t *testing.T) {
			db := wikiSourceAttemptAuthorizationDB(t, "postgres")
			claim, page, publication := seedWikiSourceAttemptAuthorization(t, db)
			ctx, cancel := context.WithTimeout(context.Background(), wikiAuthorizationTestTimeout)
			defer cancel()
			writer := db.WithContext(ctx).Begin()
			require.NoError(t, writer.Error)
			defer func() { _ = writer.Rollback().Error }()
			var writerPID int
			require.NoError(t, writer.Raw("SELECT pg_backend_pid()").Scan(&writerPID).Error)
			require.NotZero(t, writerPID)
			require.NoError(t, appendWikiSourceAttempt(ctx, writer, claim, wikiSourceAttemptRawInsert))

			// The independent observer cannot see the writer's uncommitted attempt.
			var beforeCommit int
			require.NoError(t, db.WithContext(ctx).Raw(
				"SELECT MAX(attempt) FROM knowledge_processing_spans WHERE knowledge_id = ?",
				claim.KnowledgeID).Scan(&beforeCommit).Error)
			require.Equal(t, claim.Attempt, beforeCommit)

			ready := make(chan wikiSourceAttemptWaiterBackend, 1)
			var capture sync.Once
			callback := "test:source-attempt-waiter:" + uuid.NewString()
			callbacks := db.Callback().Raw().Before("gorm:raw")
			require.NoError(t, callbacks.Register(callback, func(tx *gorm.DB) {
				statement := tx.Statement.SQL.String()
				if !strings.Contains(statement, "pg_advisory_xact_lock(") {
					return
				}
				capture.Do(func() {
					observed := wikiSourceAttemptWaiterBackend{}
					transaction, ok := tx.Statement.ConnPool.(*sql.Tx)
					if !ok {
						observed.err = errors.New(
							"exclusive source gate is outside a SQL transaction",
						)
					} else {
						observed.err = transaction.QueryRowContext(
							ctx, "SELECT pg_backend_pid()",
						).Scan(&observed.pid)
					}
					if observed.err != nil {
						_ = tx.AddError(observed.err)
					}
					ready <- observed
				})
			}))
			t.Cleanup(func() { require.NoError(t, db.Callback().Raw().Remove(callback)) })
			result := make(chan error, 1)
			exited := make(chan struct{})
			go func() {
				result <- runWikiSourceAttemptAuthorizedWrite(
					ctx, db, operation, claim, page, publication,
				)
				close(exited)
			}()
			defer func() {
				// Always release the writer and cancel the API before joining the goroutine,
				// including failure paths while observing the blocked lock.
				cancel()
				_ = writer.Rollback().Error
				select {
				case <-exited:
				case <-time.After(wikiAuthorizationTestTimeout):
					t.Error("authorization goroutine did not stop after " +
						"cancellation and writer release")
				}
			}()

			var observed wikiSourceAttemptWaiterBackend
			select {
			case observed = <-ready:
			case <-exited:
				t.Fatal("public authorization API did not reach the exclusive source gate")
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			require.NoError(t, observed.err)
			require.NotZero(t, observed.pid)
			require.NotEqual(t, writerPID, observed.pid)
			require.NoError(t, waitWikiSourceAttemptExclusiveGate(ctx, db, observed.pid, writerPID, exited))
			t.Logf("operation=%s writer=%d waiter=%d observed advisory granted=false",
				operation, writerPID, observed.pid)
			require.NoError(t, writer.Commit().Error)

			select {
			case err := <-result:
				require.ErrorIs(t, err, types.ErrWikiProgressOwnerGone)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			assertWikiSourceAttemptAuthorizationUnchanged(ctx, t, db, operation, claim, page)
			var afterCommit int
			require.NoError(t, db.WithContext(ctx).Raw(
				"SELECT MAX(attempt) FROM knowledge_processing_spans WHERE knowledge_id = ?",
				claim.KnowledgeID).Scan(&afterCommit).Error)
			require.Equal(t, claim.Attempt+1, afterCommit)
		})
	}
}

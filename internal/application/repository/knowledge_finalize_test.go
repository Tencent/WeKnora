package repository

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// knowledgesTestDDL mirrors the columns of `knowledges` that
// SetFinalizing / FinalizeSubtask / UpdateKnowledge actually read or write.
// We inline the DDL (instead of AutoMigrate) so the schema is explicit,
// and we include pending_subtasks_count from migration 000056 plus the
// processing/finalizing/completed columns the helpers care about.
const knowledgesTestDDL = `
CREATE TABLE IF NOT EXISTS knowledges (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    type VARCHAR(50) NOT NULL DEFAULT '',
    title VARCHAR(255) NOT NULL DEFAULT '',
    description TEXT,
    source VARCHAR(2048) NOT NULL DEFAULT '',
    parse_status VARCHAR(50) NOT NULL DEFAULT 'unprocessed',
    enable_status VARCHAR(50) NOT NULL DEFAULT 'enabled',
    embedding_model_id VARCHAR(64),
    file_name VARCHAR(255),
    file_type VARCHAR(50),
    file_size BIGINT,
    file_path TEXT,
    file_hash VARCHAR(64),
    storage_size BIGINT NOT NULL DEFAULT 0,
    metadata TEXT,
    tag_id VARCHAR(36),
    summary_status VARCHAR(32) DEFAULT 'none',
    last_faq_import_result TEXT DEFAULT NULL,
    channel VARCHAR(50) NOT NULL DEFAULT 'web',
    pending_subtasks_count INT NOT NULL DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    processed_at DATETIME,
    error_message TEXT,
    deleted_at DATETIME
);
`

// setupKnowledgeTestDB returns an in-memory SQLite db with the knowledges
// table. SQLite has a single-writer constraint, so we cap MaxOpenConns at 1
// and set a busy timeout: concurrent goroutines line up on the same
// connection (just like production write workloads serialize at the row
// level). This is enough to exercise the atomic semantics of the helpers
// without flaking on "database table is locked".
func setupKnowledgeTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + uuid.New().String() + "?mode=memory&cache=shared&_busy_timeout=5000"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.Exec(knowledgesTestDDL).Error)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

// insertProcessingKnowledge seeds a row in `processing` state ready for a
// SetFinalizing transition.
func insertProcessingKnowledge(t *testing.T, db *gorm.DB) string {
	t.Helper()
	id := uuid.New().String()
	require.NoError(t, db.Exec(`
		INSERT INTO knowledges (id, tenant_id, knowledge_base_id, type, title, source, parse_status, pending_subtasks_count)
		VALUES (?, 1, ?, 'document', 'finalize-test', 'manual', 'processing', 0)
	`, id, uuid.New().String()).Error)
	return id
}

// reloadKnowledgeRow returns the parse_status and pending_subtasks_count of
// a row directly via raw SQL — bypasses any GORM hook noise.
func reloadKnowledgeRow(t *testing.T, db *gorm.DB, id string) (status string, count int) {
	t.Helper()
	row := db.Raw(`SELECT parse_status, pending_subtasks_count FROM knowledges WHERE id = ?`, id).Row()
	require.NoError(t, row.Scan(&status, &count))
	return status, count
}

// TestFinalizeSubtask_Concurrent_ExactlyOnePromote spawns N goroutines that
// each call FinalizeSubtask after SetFinalizing(N), and asserts:
//   - the counter ends at zero,
//   - parse_status is "completed",
//   - exactly one caller observed promoted=true.
//
// This is the behavior the original "stuck pending_subtasks_count" bug
// violated: clobbered counters meant some callers saw a non-zero value
// after the true count had reached zero, and none of them promoted.
func TestFinalizeSubtask_Concurrent_ExactlyOnePromote(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	ctx := context.Background()

	const n = 20
	id := insertProcessingKnowledge(t, db)

	transitioned, err := repo.SetFinalizing(ctx, id, n)
	require.NoError(t, err)
	require.True(t, transitioned, "SetFinalizing should transition processing -> finalizing")

	var promoteWins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, promoted, ferr := repo.FinalizeSubtask(ctx, id)
			if ferr != nil {
				t.Errorf("FinalizeSubtask: %v", ferr)
				return
			}
			if promoted {
				promoteWins.Add(1)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int32(1), promoteWins.Load(),
		"exactly one caller must observe promoted=true even under concurrent decrements")

	status, count := reloadKnowledgeRow(t, db, id)
	assert.Equal(t, types.ParseStatusCompleted, status)
	assert.Equal(t, 0, count)
}

// TestFinalizeSubtask_PartialDecrement_StaysFinalizing verifies the row
// remains in "finalizing" with the expected residual count when fewer
// callers decrement than were seeded — the promote guard must not fire
// early.
func TestFinalizeSubtask_PartialDecrement_StaysFinalizing(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	ctx := context.Background()

	id := insertProcessingKnowledge(t, db)
	_, err := repo.SetFinalizing(ctx, id, 3)
	require.NoError(t, err)

	for i := 0; i < 2; i++ {
		_, promoted, ferr := repo.FinalizeSubtask(ctx, id)
		require.NoError(t, ferr)
		assert.False(t, promoted, "promote must not fire while count > 0")
	}

	status, count := reloadKnowledgeRow(t, db, id)
	assert.Equal(t, types.ParseStatusFinalizing, status)
	assert.Equal(t, 1, count)
}

// TestFinalizeSubtask_DecrementClampedAtZero verifies the safety-net
// clamp on the decrement: extra calls past the seeded count must not
// underflow pending_subtasks_count below zero. (Reconciliation's
// shortfall-release loop relies on this.)
func TestFinalizeSubtask_DecrementClampedAtZero(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	ctx := context.Background()

	id := insertProcessingKnowledge(t, db)
	_, err := repo.SetFinalizing(ctx, id, 1)
	require.NoError(t, err)

	// First decrement drains the only slot and promotes.
	_, promoted, err := repo.FinalizeSubtask(ctx, id)
	require.NoError(t, err)
	assert.True(t, promoted)

	// Subsequent decrements must be no-ops, not underflow.
	for i := 0; i < 3; i++ {
		_, promoted, err := repo.FinalizeSubtask(ctx, id)
		require.NoError(t, err)
		assert.False(t, promoted)
	}

	status, count := reloadKnowledgeRow(t, db, id)
	assert.Equal(t, types.ParseStatusCompleted, status)
	assert.Equal(t, 0, count, "pending_subtasks_count must be clamped at zero")
}

// TestUpdateKnowledge_DoesNotClobberPendingCounter is the regression test
// for the original bug: a full-row Save with a stale in-memory counter
// must not write that stale value back, otherwise it overwrites atomic
// decrements made by other goroutines.
//
// Sequence:
//  1. SetFinalizing(N=5) -> counter=5
//  2. Caller A loads the row (sees counter=5)
//  3. FinalizeSubtask runs concurrently and decrements to counter=4
//  4. Caller A modifies an unrelated field (Title) and calls UpdateKnowledge
//  5. Counter must still be 4 (not 5).
func TestUpdateKnowledge_DoesNotClobberPendingCounter(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	ctx := context.Background()

	id := insertProcessingKnowledge(t, db)
	_, err := repo.SetFinalizing(ctx, id, 5)
	require.NoError(t, err)

	// Step 2: caller A snapshots the row with counter=5 in memory.
	loaded, err := repo.GetKnowledgeByID(ctx, 1, id)
	require.NoError(t, err)
	require.Equal(t, 5, loaded.PendingSubtasksCount)

	// Step 3: an enrichment subtask decrements concurrently.
	_, _, err = repo.FinalizeSubtask(ctx, id)
	require.NoError(t, err)

	// Step 4: caller A persists an unrelated change. The in-memory copy
	// of PendingSubtasksCount is the STALE 5 — Save must NOT write it.
	loaded.Title = "renamed-after-stale-load"
	require.NoError(t, repo.UpdateKnowledge(ctx, loaded))

	// Step 5: the live counter is still 4, not clobbered back to 5.
	status, count := reloadKnowledgeRow(t, db, id)
	assert.Equal(t, types.ParseStatusFinalizing, status)
	assert.Equal(t, 4, count,
		"UpdateKnowledge must omit pending_subtasks_count so a stale in-memory value cannot clobber atomic decrements")

	// And the unrelated field WAS persisted.
	reloaded, err := repo.GetKnowledgeByID(ctx, 1, id)
	require.NoError(t, err)
	assert.Equal(t, "renamed-after-stale-load", reloaded.Title)
}

// TestUpdateKnowledge_PendingCounterOmittedOnReset verifies the inverse
// case the reparse paths rely on: even setting PendingSubtasksCount=0
// in memory and calling UpdateKnowledge does NOT persist that value.
// Reparse must use UpdateKnowledgeColumn explicitly.
func TestUpdateKnowledge_PendingCounterOmittedOnReset(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	ctx := context.Background()

	id := insertProcessingKnowledge(t, db)
	_, err := repo.SetFinalizing(ctx, id, 7)
	require.NoError(t, err)

	loaded, err := repo.GetKnowledgeByID(ctx, 1, id)
	require.NoError(t, err)

	// Caller tries to reset the counter via Save — this must be a no-op
	// for that column. The dedicated UpdateKnowledgeColumn is the only
	// path that actually writes pending_subtasks_count.
	loaded.PendingSubtasksCount = 0
	require.NoError(t, repo.UpdateKnowledge(ctx, loaded))

	_, count := reloadKnowledgeRow(t, db, id)
	assert.Equal(t, 7, count, "UpdateKnowledge with PendingSubtasksCount=0 must NOT persist the reset")

	// The explicit column write IS the supported path.
	require.NoError(t, repo.UpdateKnowledgeColumn(ctx, id, "pending_subtasks_count", 0))
	_, count = reloadKnowledgeRow(t, db, id)
	assert.Equal(t, 0, count)
}

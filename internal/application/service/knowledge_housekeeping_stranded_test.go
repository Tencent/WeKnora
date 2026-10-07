package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func insertKnowledgeWithSummary(t *testing.T, db *gorm.DB, id, status, summary string, updatedAt time.Time) {
	t.Helper()
	require.NoError(t, db.Exec(
		`INSERT INTO knowledges (id, parse_status, summary_status, updated_at) VALUES (?, ?, ?, ?)`,
		id, status, summary, updatedAt,
	).Error)
}

func readSummaryStatus(t *testing.T, db *gorm.DB, id string) string {
	t.Helper()
	var summary string
	require.NoError(t, db.Raw(`SELECT summary_status FROM knowledges WHERE id = ?`, id).Scan(&summary).Error)
	return summary
}

func countWikiOps(t *testing.T, db *gorm.DB, knowledgeID string) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Raw(`SELECT COUNT(*) FROM task_pending_ops WHERE dedup_key = ?`, knowledgeID).
		Scan(&n).Error)
	return n
}

// A stuck row failed by the sweep has lost its summary task with the rest of
// the run; an unfinished summary must fail with it, a finished one is kept.
func TestHousekeepingRecoverStalledFailsUnfinishedSummary(t *testing.T) {
	db := setupHousekeepingDB(t)
	svc := newHousekeepingSvcForTest(db)
	stale := time.Now().Add(-3 * time.Hour)
	insertKnowledgeWithSummary(t, db, "k-pending", types.ParseStatusFinalizing, types.SummaryStatusPending, stale)
	insertKnowledgeWithSummary(t, db, "k-running", types.ParseStatusFinalizing, types.SummaryStatusProcessing, stale)
	insertKnowledgeWithSummary(t, db, "k-done", types.ParseStatusFinalizing, types.SummaryStatusCompleted, stale)

	svc.runSweep(context.Background())

	for id, want := range map[string]string{
		"k-pending": types.SummaryStatusFailed,
		"k-running": types.SummaryStatusFailed,
		"k-done":    types.SummaryStatusCompleted,
	} {
		status, _ := readKnowledgeStatus(t, db, id)
		assert.Equal(t, types.ParseStatusFailed, status, id)
		assert.Equal(t, want, readSummaryStatus(t, db, id), id)
	}
}

// Only the summary task moves a summary out of "pending"; when it gives up
// without writing a status the row keeps a spinner forever. Finished rows
// with nothing queued are failed; a queued summary task, a run still in
// flight and a recent row are left alone.
func TestHousekeepingRecoversStrandedPendingSummaries(t *testing.T) {
	db := setupHousekeepingDB(t)
	svc := newHousekeepingSvcWithInspector(db, fakeTaskInspector{
		queued: map[string]bool{"k-queued": true},
	})
	stale := time.Now().Add(-2 * time.Hour)
	cutoff := time.Now().Add(-time.Hour)
	for _, status := range []string{types.ParseStatusCompleted, types.ParseStatusFailed, types.ParseStatusCancelled} {
		insertKnowledgeWithSummary(t, db, "k-"+status, status, types.SummaryStatusPending, stale)
	}
	insertKnowledgeWithSummary(t, db, "k-queued", types.ParseStatusCompleted, types.SummaryStatusPending, stale)
	insertKnowledgeWithSummary(t, db, "k-finalizing", types.ParseStatusFinalizing, types.SummaryStatusPending, stale)
	insertKnowledgeWithSummary(t, db, "k-recent", types.ParseStatusCompleted, types.SummaryStatusPending, time.Now())

	svc.recoverStrandedPendingSummaries(context.Background(), cutoff)

	for id, want := range map[string]string{
		"k-" + types.ParseStatusCompleted: types.SummaryStatusFailed,
		"k-" + types.ParseStatusFailed:    types.SummaryStatusFailed,
		"k-" + types.ParseStatusCancelled: types.SummaryStatusFailed,
		"k-queued":                        types.SummaryStatusPending,
		"k-finalizing":                    types.SummaryStatusPending,
		"k-recent":                        types.SummaryStatusPending,
	} {
		assert.Equal(t, want, readSummaryStatus(t, db, id), id)
	}
}

// A durable wiki op protects its row only up to wikiHoldLimit: a consumer that
// fails before claiming never consumes the op, and the row would otherwise sit
// in "finalizing" forever. Past the limit the row is failed and its op
// dropped; a younger hold is still honoured.
func TestHousekeepingFailsRowsHeldByWikiPastHoldLimit(t *testing.T) {
	db := setupHousekeepingDB(t)
	queue := &wikiGuardTaskQueue{}
	svc := newHousekeepingSvcForTest(db)
	svc.task = queue
	for id, updatedAt := range map[string]time.Time{
		"k-expired": time.Now().Add(-wikiHoldLimit - time.Hour),
		"k-held":    time.Now().Add(-3 * time.Hour),
	} {
		kbID := "kb-" + id
		require.NoError(t, db.Exec(
			`INSERT INTO knowledges (id, tenant_id, knowledge_base_id, parse_status, updated_at)
			 VALUES (?, 7, ?, ?, ?)`, id, kbID, types.ParseStatusFinalizing, updatedAt,
		).Error)
		insertWikiPendingOp(t, db, kbID, id)
	}

	svc.runSweep(context.Background())

	status, msg := readKnowledgeStatus(t, db, "k-expired")
	assert.Equal(t, types.ParseStatusFailed, status)
	assert.NotEmpty(t, msg)
	assert.Zero(t, countWikiOps(t, db, "k-expired"), "the expired op is dropped with its row")

	status, _ = readKnowledgeStatus(t, db, "k-held")
	assert.Equal(t, types.ParseStatusFinalizing, status)
	assert.Equal(t, int64(1), countWikiOps(t, db, "k-held"))
	require.Len(t, queue.tasks, 1, "only the live hold gets its trigger re-armed")
	var payload WikiIngestPayload
	require.NoError(t, json.Unmarshal(queue.tasks[0].Payload(), &payload))
	assert.Equal(t, "kb-k-held", payload.KnowledgeBaseID)
}

// A span heartbeat newer than the limit keeps the hold even when the row
// itself has not been written for longer.
func TestSplitExpiredWikiHoldsUsesLatestActivity(t *testing.T) {
	now := time.Now()
	cutoff := now.Add(-wikiHoldLimit)
	held := []types.Knowledge{
		{ID: "row-old-no-beat", UpdatedAt: now.Add(-wikiHoldLimit - time.Hour)},
		{ID: "row-old-recent-beat", UpdatedAt: now.Add(-wikiHoldLimit - time.Hour)},
		{ID: "row-recent", UpdatedAt: now.Add(-time.Hour)},
	}
	heartbeat := map[string]time.Time{"row-old-recent-beat": now.Add(-10 * time.Hour)}

	kept, expired := splitExpiredWikiHolds(held, heartbeat, cutoff)

	require.Len(t, expired, 1)
	assert.Equal(t, "row-old-no-beat", expired[0].ID)
	require.Len(t, kept, 2)
	assert.Equal(t, "row-old-recent-beat", kept[0].ID)
	assert.Equal(t, "row-recent", kept[1].ID)
}

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newRetentionTask(
	tenantID uint64,
	id string,
	status types.EvaluationStatue,
	endTime *time.Time,
) *types.EvaluationTaskEntity {
	startTime := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	leaseExpiresAt := startTime.Add(2 * time.Hour)
	return &types.EvaluationTaskEntity{
		ID:                       id,
		TenantID:                 tenantID,
		DatasetID:                "default",
		Status:                   status,
		StartTime:                startTime,
		EndTime:                  endTime,
		TemporaryKnowledgeBaseID: "kb-" + id,
		OwnerID:                  "owner-" + id,
		LeaseExpiresAt:           &leaseExpiresAt,
		HeartbeatAt:              startTime,
		CreatedAt:                startTime,
		UpdatedAt:                startTime,
	}
}

func retentionTaskCount(t *testing.T, dbCount func() int64) int64 {
	t.Helper()
	return dbCount()
}

func TestEvaluationTaskRepositoryDeleteExpiredTerminalTasksRespectsBoundaries(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	repo := NewEvaluationTaskRepository(db)
	ctx := context.Background()
	cutoff := time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)
	before := cutoff.Add(-time.Second)
	at := cutoff
	after := cutoff.Add(time.Second)

	expired := newRetentionTask(97, "expired", types.EvaluationStatueSuccess, &before)
	require.NoError(t, db.Create(expired).Error)
	atBoundary := newRetentionTask(97, "at-boundary", types.EvaluationStatueFailed, &at)
	require.NoError(t, db.Create(atBoundary).Error)
	fresh := newRetentionTask(97, "fresh", types.EvaluationStatueSuccess, &after)
	require.NoError(t, db.Create(fresh).Error)
	active := newRetentionTask(97, "active-running", types.EvaluationStatueRunning, &before)
	require.NoError(t, db.Create(active).Error)
	noEndTime := newRetentionTask(97, "no-end-time", types.EvaluationStatueCanceled, nil)
	noEndTime.CancelRequestedAt = &before
	require.NoError(t, db.Create(noEndTime).Error)
	softDeleted := newRetentionTask(97, "soft-deleted", types.EvaluationStatueSuccess, &before)
	require.NoError(t, db.Create(softDeleted).Error)
	require.NoError(t, db.Exec(
		"UPDATE evaluation_tasks SET deleted_at = ? WHERE id = ?", before, softDeleted.ID,
	).Error)

	deleted, err := repo.DeleteExpiredTerminalTasks(ctx, cutoff, 500)
	require.NoError(t, err)
	assert.Equal(t, int64(2), deleted, "only the expired terminal row and its soft-deleted peer")

	countTasks := func() int64 {
		var count int64
		require.NoError(t, db.Raw("SELECT COUNT(*) FROM evaluation_tasks").Scan(&count).Error)
		return count
	}
	assert.Equal(t, int64(4), retentionTaskCount(t, countTasks), "boundary, fresh, active, and no-end-time rows stay")

	// A second round finds nothing left to delete.
	deleted, err = repo.DeleteExpiredTerminalTasks(ctx, cutoff, 500)
	require.NoError(t, err)
	assert.Zero(t, deleted)
}

func TestEvaluationTaskRepositoryDeleteExpiredTerminalTasksBatchesOldestFirst(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	repo := NewEvaluationTaskRepository(db)
	ctx := context.Background()
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	cutoff := time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)

	for i := 0; i < 3; i++ {
		endTime := base.Add(time.Duration(i) * time.Hour)
		task := newRetentionTask(98, fmt.Sprintf("batch-%c", 'a'+i), types.EvaluationStatueSuccess, &endTime)
		require.NoError(t, db.Create(task).Error)
	}

	deleted, err := repo.DeleteExpiredTerminalTasks(ctx, cutoff, 2)
	require.NoError(t, err)
	assert.Equal(t, int64(2), deleted)

	var remaining []string
	require.NoError(t, db.Raw("SELECT id FROM evaluation_tasks").Scan(&remaining).Error)
	assert.Equal(t, []string{"batch-c"}, remaining, "the two oldest rows are removed first")

	deleted, err = repo.DeleteExpiredTerminalTasks(ctx, cutoff, 2)
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)

	_, err = repo.DeleteExpiredTerminalTasks(ctx, time.Time{}, 1)
	require.Error(t, err)
	_, err = repo.DeleteExpiredTerminalTasks(ctx, cutoff, 0)
	require.Error(t, err)
}

func TestEvaluationTaskRepositoryRetentionCascadesQuestionResults(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	repo := NewEvaluationTaskRepository(db)
	ctx := context.Background()
	cutoff := time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)
	endTime := cutoff.Add(-time.Hour)
	task := newRetentionTask(99, "retention-question-results", types.EvaluationStatueSuccess, &endTime)
	require.NoError(t, db.Create(task).Error)
	require.NoError(t, db.Create(&types.EvaluationQuestionResultEntity{
		TenantID:           task.TenantID,
		TaskID:             task.ID,
		SampleIndex:        0,
		QID:                "q1",
		Question:           "question?",
		GroundTruthPIDs:    types.JSON(`[]`),
		SearchResults:      types.JSON(`[]`),
		RerankResults:      types.JSON(`[]`),
		GenerationPIDs:     types.JSON(`[]`),
		PerSampleMetrics:   types.JSON(`{}`),
		MetricObservations: types.JSON(`[]`),
		Status:             types.EvaluationQuestionStatusSuccess,
		ResultHash:         "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
	}).Error)

	deleted, err := repo.DeleteExpiredTerminalTasks(ctx, cutoff, 10)
	require.NoError(t, err)
	require.Equal(t, int64(1), deleted)
	var rows int64
	require.NoError(t, db.Raw(
		"SELECT COUNT(*) FROM evaluation_question_results WHERE tenant_id = ? AND task_id = ?",
		task.TenantID, task.ID,
	).Scan(&rows).Error)
	require.Zero(t, rows)
}

func TestEvaluationTaskRepositoryRetentionQueryUsesRetentionIndex(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)

	rows, err := sqlDB.Query(
		`EXPLAIN QUERY PLAN SELECT id FROM evaluation_tasks INDEXED BY idx_evaluation_tasks_retention
			WHERE status IN (2, 3, 4, 5, 6) AND end_time IS NOT NULL AND end_time < '2026-08-28'
			ORDER BY end_time ASC LIMIT 500`,
	)
	require.NoError(t, err)
	plan := ""
	for rows.Next() {
		var id, parent, notused int
		var detail string
		require.NoError(t, rows.Scan(&id, &parent, &notused, &detail))
		plan += detail + "\n"
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	assert.Contains(t, plan, "idx_evaluation_tasks_retention")
}

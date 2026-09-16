package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvaluationTaskRepositoryDeleteTaskSoftDeletesTerminalTask(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	repo := NewEvaluationTaskRepository(db)
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 11, 0, 0, 0, time.UTC)

	task := newListTask(91, "delete-terminal", now.Add(-time.Hour), types.EvaluationStatueSuccess)
	endTime := task.StartTime.Add(time.Minute)
	task.EndTime = &endTime
	task.LeaseExpiresAt = nil
	require.NoError(t, db.Create(task).Error)

	require.NoError(t, repo.DeleteTask(ctx, 91, task.ID, now))

	// Normal reads hide the soft-deleted task.
	_, err := repo.GetTask(ctx, 91, task.ID)
	require.ErrorIs(t, err, ErrEvaluationTaskNotFound)
	tasks, err := repo.ListTasks(ctx, 91, types.EvaluationTaskListQuery{Limit: 10})
	require.NoError(t, err)
	assert.Empty(t, tasks)

	// The row remains physically present with deleted_at set.
	var deletedAt *time.Time
	require.NoError(t, db.Raw(
		"SELECT deleted_at FROM evaluation_tasks WHERE id = ?", task.ID,
	).Scan(&deletedAt).Error)
	require.NotNil(t, deletedAt)
	assert.Equal(t, now.UTC(), deletedAt.UTC())

	// Deleting again stays idempotent.
	require.NoError(t, repo.DeleteTask(ctx, 91, task.ID, now.Add(time.Minute)))
}

func TestEvaluationTaskRepositoryDeleteTaskBoundaries(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	repo := NewEvaluationTaskRepository(db)
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 11, 0, 0, 0, time.UTC)

	active := newListTask(92, "delete-active", now.Add(-time.Hour), types.EvaluationStatueRunning)
	require.NoError(t, db.Create(active).Error)
	otherTenant := newListTask(93, "delete-other-tenant", now.Add(-time.Hour), types.EvaluationStatueSuccess)
	endTime := otherTenant.StartTime.Add(time.Minute)
	otherTenant.EndTime = &endTime
	otherTenant.LeaseExpiresAt = nil
	require.NoError(t, db.Create(otherTenant).Error)

	// Missing, cross-tenant, and already deleted tasks are idempotent 204s.
	require.NoError(t, repo.DeleteTask(ctx, 92, "missing-task", now))
	require.NoError(t, repo.DeleteTask(ctx, 92, otherTenant.ID, now))

	// Active tasks are rejected and remain visible.
	err := repo.DeleteTask(ctx, 92, active.ID, now)
	require.ErrorIs(t, err, ErrEvaluationTaskStateConflict)
	_, err = repo.GetTask(ctx, 92, active.ID)
	require.NoError(t, err)

	// Cross-tenant delete does not touch the other tenant's task.
	_, err = repo.GetTask(ctx, 93, otherTenant.ID)
	require.NoError(t, err)
}

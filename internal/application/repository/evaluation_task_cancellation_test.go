package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvaluationTaskCanceledStatusHasStableNumericValue(t *testing.T) {
	assert.Equal(t, types.EvaluationStatue(6), types.EvaluationStatueCanceled)
}

func TestEvaluationTaskRepositoryRequestCancelKeepsFirstTimeAndVersion(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	repo := NewEvaluationTaskRepository(db)
	ctx := context.Background()
	task := newEvaluationTaskEntity(61, "cancel-first-wins")
	require.NoError(t, repo.CreateTask(ctx, task.TenantID, task))

	startedAt := task.StartTime.Add(10 * time.Second)
	started, err := repo.TryStartTask(ctx, types.EvaluationTaskStartCommand{
		TenantID:        task.TenantID,
		TaskID:          task.ID,
		OwnerID:         task.OwnerID,
		ExpectedVersion: task.Version,
		Now:             startedAt,
		LeaseExpiresAt:  startedAt.Add(2 * time.Minute),
	})
	require.NoError(t, err)

	firstCancelAt := startedAt.Add(30 * time.Second)
	canceled, err := repo.RequestCancel(ctx, types.EvaluationTaskCancelCommand{
		TenantID: task.TenantID,
		TaskID:   task.ID,
		Now:      firstCancelAt,
	})
	require.NoError(t, err)
	require.NotNil(t, canceled.CancelRequestedAt)
	assert.Equal(t, firstCancelAt.UTC(), *canceled.CancelRequestedAt)
	assert.Equal(t, started.Version, canceled.Version)
	assert.Equal(t, types.EvaluationStatueRunning, canceled.Status)

	repeated, err := repo.RequestCancel(ctx, types.EvaluationTaskCancelCommand{
		TenantID: task.TenantID,
		TaskID:   task.ID,
		Now:      firstCancelAt.Add(time.Minute),
	})
	require.NoError(t, err)
	require.NotNil(t, repeated.CancelRequestedAt)
	assert.Equal(t, firstCancelAt.UTC(), *repeated.CancelRequestedAt)
	assert.Equal(t, started.Version, repeated.Version)

	persisted, err := repo.GetTask(ctx, task.TenantID, task.ID)
	require.NoError(t, err)
	assert.Equal(t, started.Version, persisted.Version)
	assert.Equal(t, firstCancelAt.UTC(), *persisted.CancelRequestedAt)
}

func TestEvaluationTaskRepositoryRequestCancelNotFoundAndTerminal(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	repo := NewEvaluationTaskRepository(db)
	ctx := context.Background()
	task := newEvaluationTaskEntity(62, "cancel-boundaries")
	require.NoError(t, repo.CreateTask(ctx, task.TenantID, task))

	now := task.StartTime.Add(time.Minute)
	_, err := repo.RequestCancel(ctx, types.EvaluationTaskCancelCommand{
		TenantID: task.TenantID,
		TaskID:   "missing-task",
		Now:      now,
	})
	require.ErrorIs(t, err, ErrEvaluationTaskNotFound)

	_, err = repo.RequestCancel(ctx, types.EvaluationTaskCancelCommand{
		TenantID: task.TenantID + 100,
		TaskID:   task.ID,
		Now:      now,
	})
	require.ErrorIs(t, err, ErrEvaluationTaskNotFound)

	started, err := repo.TryStartTask(ctx, types.EvaluationTaskStartCommand{
		TenantID:        task.TenantID,
		TaskID:          task.ID,
		OwnerID:         task.OwnerID,
		ExpectedVersion: task.Version,
		Now:             task.StartTime.Add(10 * time.Second),
		LeaseExpiresAt:  task.StartTime.Add(2 * time.Minute),
	})
	require.NoError(t, err)
	terminal, err := repo.PublishTerminal(ctx, types.EvaluationTaskTerminalCommand{
		TenantID:        task.TenantID,
		TaskID:          task.ID,
		OwnerID:         task.OwnerID,
		ExpectedVersion: started.Version,
		Status:          types.EvaluationStatueSuccess,
		EndTime:         task.StartTime.Add(time.Minute),
		CleanupErrors:   types.JSON(`[]`),
	})
	require.NoError(t, err)

	repeated, err := repo.RequestCancel(ctx, types.EvaluationTaskCancelCommand{
		TenantID: task.TenantID,
		TaskID:   task.ID,
		Now:      now,
	})
	require.NoError(t, err)
	assert.Equal(t, types.EvaluationStatueSuccess, repeated.Status)
	assert.Nil(t, repeated.CancelRequestedAt)
	assert.Equal(t, terminal.Version, repeated.Version)
}

func TestEvaluationTaskRepositoryTerminalPublishRequiresMatchingCancelTruth(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	repo := NewEvaluationTaskRepository(db)
	ctx := context.Background()
	task := newEvaluationTaskEntity(63, "cancel-cas-truth")
	require.NoError(t, repo.CreateTask(ctx, task.TenantID, task))

	startedAt := task.StartTime.Add(10 * time.Second)
	started, err := repo.TryStartTask(ctx, types.EvaluationTaskStartCommand{
		TenantID:        task.TenantID,
		TaskID:          task.ID,
		OwnerID:         task.OwnerID,
		ExpectedVersion: task.Version,
		Now:             startedAt,
		LeaseExpiresAt:  startedAt.Add(2 * time.Minute),
	})
	require.NoError(t, err)
	started, err = repo.PublishProgress(ctx, types.EvaluationTaskProgressCommand{
		TenantID: task.TenantID, TaskID: task.ID, OwnerID: task.OwnerID,
		ExpectedVersion: started.Version, Total: 2, Finished: 1,
		Now: startedAt.Add(20 * time.Second), LeaseExpiresAt: startedAt.Add(2 * time.Minute),
	})
	require.NoError(t, err)

	// Canceled without a persistent cancel request is rejected.
	endTime := startedAt.Add(time.Minute)
	_, err = repo.PublishTerminal(ctx, types.EvaluationTaskTerminalCommand{
		TenantID:        task.TenantID,
		TaskID:          task.ID,
		OwnerID:         task.OwnerID,
		ExpectedVersion: started.Version,
		Status:          types.EvaluationStatueCanceled,
		EndTime:         endTime,
		ErrMsg:          "evaluation task canceled",
		CleanupErrors:   types.JSON(`[]`),
	})
	require.ErrorIs(t, err, ErrEvaluationTaskStateConflict)

	// A concurrent cancel request invalidates every other terminal state.
	_, err = repo.RequestCancel(ctx, types.EvaluationTaskCancelCommand{
		TenantID: task.TenantID,
		TaskID:   task.ID,
		Now:      startedAt.Add(30 * time.Second),
	})
	require.NoError(t, err)
	for _, terminal := range []struct {
		status types.EvaluationStatue
		errMsg string
	}{
		{types.EvaluationStatueSuccess, ""},
		{types.EvaluationStatueFailed, "terminal with concurrent cancel"},
		{types.EvaluationStatueTimedOut, "terminal with concurrent cancel"},
		{types.EvaluationStatueInterrupted, "terminal with concurrent cancel"},
	} {
		_, err = repo.PublishTerminal(ctx, types.EvaluationTaskTerminalCommand{
			TenantID:        task.TenantID,
			TaskID:          task.ID,
			OwnerID:         task.OwnerID,
			ExpectedVersion: started.Version,
			Status:          terminal.status,
			EndTime:         endTime,
			ErrMsg:          terminal.errMsg,
			CleanupErrors:   types.JSON(`[]`),
		})
		require.ErrorIs(t, err, ErrEvaluationTaskStateConflict, "status %d", terminal.status)
	}

	// The executor re-reads and publishes Canceled with the same version.
	canceled, err := repo.PublishTerminal(ctx, types.EvaluationTaskTerminalCommand{
		TenantID:        task.TenantID,
		TaskID:          task.ID,
		OwnerID:         task.OwnerID,
		ExpectedVersion: started.Version,
		Status:          types.EvaluationStatueCanceled,
		EndTime:         endTime,
		ErrMsg:          "evaluation task canceled",
		CleanupErrors:   types.JSON(`[]`),
	})
	require.NoError(t, err)
	assert.Equal(t, types.EvaluationStatueCanceled, canceled.Status)
	assert.Equal(t, 2, canceled.Total)
	assert.Equal(t, 1, canceled.Finished)
	require.NotNil(t, canceled.CancelRequestedAt)
	assert.Nil(t, canceled.LeaseExpiresAt)

	persisted, err := repo.GetTask(ctx, task.TenantID, task.ID)
	require.NoError(t, err)
	assert.Equal(t, types.EvaluationStatueCanceled, persisted.Status)
}

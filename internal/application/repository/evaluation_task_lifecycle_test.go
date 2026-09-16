package repository

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvaluationTaskRepositoryPublishesLifecycleSnapshotsAtomically(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	repo := NewEvaluationTaskRepository(db)
	ctx := context.Background()
	task := newEvaluationTaskEntity(21, "lifecycle")
	require.NoError(t, repo.CreateTask(ctx, task.TenantID, task))

	offset := time.FixedZone("UTC+9", 9*60*60)
	startedAt := time.Date(2026, 8, 28, 17, 0, 10, 0, offset)
	started, err := repo.TryStartTask(ctx, types.EvaluationTaskStartCommand{
		TenantID:        task.TenantID,
		TaskID:          task.ID,
		OwnerID:         task.OwnerID,
		ExpectedVersion: task.Version,
		Now:             startedAt,
		LeaseExpiresAt:  startedAt.Add(time.Minute),
	})
	require.NoError(t, err)
	require.NotNil(t, started)
	assert.Equal(t, types.EvaluationStatueRunning, started.Status)
	assert.Equal(t, uint64(2), started.Version)
	assert.Equal(t, time.UTC, started.HeartbeatAt.Location())
	assert.Equal(t, startedAt.UTC(), started.HeartbeatAt)
	require.NotNil(t, started.LeaseExpiresAt)
	assert.Equal(t, time.UTC, started.LeaseExpiresAt.Location())
	assert.Equal(t, startedAt.Add(time.Minute).UTC(), *started.LeaseExpiresAt)

	withKnowledge, err := repo.RecordTemporaryKnowledge(ctx, types.EvaluationTaskKnowledgeCommand{
		TenantID:             task.TenantID,
		TaskID:               task.ID,
		OwnerID:              task.OwnerID,
		ExpectedVersion:      started.Version,
		TemporaryKnowledgeID: "knowledge-lifecycle",
		UpdatedAt:            startedAt.Add(10 * time.Second),
	})
	require.NoError(t, err)
	require.NotNil(t, withKnowledge)
	assert.Equal(t, "knowledge-lifecycle", withKnowledge.TemporaryKnowledgeID)
	assert.Equal(t, uint64(3), withKnowledge.Version)
	assert.Equal(t, time.UTC, withKnowledge.UpdatedAt.Location())
	assert.Equal(t, startedAt.Add(10*time.Second).UTC(), withKnowledge.UpdatedAt)

	progressAt := startedAt.Add(20 * time.Second)
	progressMetric := types.JSON(`{"retrieval_metrics":{"precision":0.5}}`)
	progress, err := repo.PublishProgress(ctx, types.EvaluationTaskProgressCommand{
		TenantID:        task.TenantID,
		TaskID:          task.ID,
		OwnerID:         task.OwnerID,
		ExpectedVersion: withKnowledge.Version,
		Total:           4,
		Finished:        2,
		Metric:          progressMetric,
		Now:             progressAt,
		LeaseExpiresAt:  progressAt.Add(time.Minute),
	})
	require.NoError(t, err)
	require.NotNil(t, progress)
	assert.Equal(t, 4, progress.Total)
	assert.Equal(t, 2, progress.Finished)
	assert.JSONEq(t, progressMetric.ToString(), progress.Metric.ToString())
	assert.Equal(t, uint64(4), progress.Version)
	assert.Equal(t, progressAt.UTC(), progress.HeartbeatAt)
	require.NotNil(t, progress.LeaseExpiresAt)
	assert.Equal(t, progressAt.Add(time.Minute).UTC(), *progress.LeaseExpiresAt)

	endedAt := progressAt.Add(15 * time.Second)
	cleanupErrors := types.JSON(`["knowledge cleanup failed"]`)
	terminalMetric := types.JSON(`{"retrieval_metrics":{"precision":0.75}}`)
	terminal, err := repo.PublishTerminal(ctx, types.EvaluationTaskTerminalCommand{
		TenantID:        task.TenantID,
		TaskID:          task.ID,
		OwnerID:         task.OwnerID,
		ExpectedVersion: progress.Version,
		Status:          types.EvaluationStatueFailed,
		EndTime:         endedAt,
		ErrMsg:          "evaluation failed",
		CleanupErrors:   cleanupErrors,
		Metric:          terminalMetric,
	})
	require.NoError(t, err)
	require.NotNil(t, terminal)
	assert.Equal(t, types.EvaluationStatueFailed, terminal.Status)
	require.NotNil(t, terminal.EndTime)
	assert.Equal(t, time.UTC, terminal.EndTime.Location())
	assert.Equal(t, endedAt.UTC(), *terminal.EndTime)
	assert.Equal(t, "evaluation failed", terminal.ErrMsg)
	assert.JSONEq(t, cleanupErrors.ToString(), terminal.CleanupErrors.ToString())
	assert.JSONEq(t, terminalMetric.ToString(), terminal.Metric.ToString())
	assert.Nil(t, terminal.LeaseExpiresAt)
	assert.Equal(t, uint64(5), terminal.Version)

	persisted, err := repo.GetTask(ctx, task.TenantID, task.ID)
	require.NoError(t, err)
	assert.Equal(t, terminal.Status, persisted.Status)
	assert.Equal(t, terminal.Version, persisted.Version)
	assert.Equal(t, terminal.EndTime, persisted.EndTime)
	assert.Equal(t, terminal.ErrMsg, persisted.ErrMsg)
	assert.JSONEq(t, terminal.CleanupErrors.ToString(), persisted.CleanupErrors.ToString())
	assert.JSONEq(t, terminal.Metric.ToString(), persisted.Metric.ToString())
	assert.Nil(t, persisted.LeaseExpiresAt)
}

func TestEvaluationTaskRepositoryRejectsLifecycleCompareAndSwapMismatches(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*types.EvaluationTaskStartCommand)
	}{
		{
			name: "tenant",
			mutate: func(command *types.EvaluationTaskStartCommand) {
				command.TenantID++
			},
		},
		{
			name: "owner",
			mutate: func(command *types.EvaluationTaskStartCommand) {
				command.OwnerID = "different-owner"
			},
		},
		{
			name: "version",
			mutate: func(command *types.EvaluationTaskStartCommand) {
				command.ExpectedVersion++
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := setupEvaluationTaskRepositoryTestDB(t)
			repo := NewEvaluationTaskRepository(db)
			ctx := context.Background()
			task := newEvaluationTaskEntity(22, "cas-"+tc.name)
			require.NoError(t, repo.CreateTask(ctx, task.TenantID, task))
			command := types.EvaluationTaskStartCommand{
				TenantID:        task.TenantID,
				TaskID:          task.ID,
				OwnerID:         task.OwnerID,
				ExpectedVersion: task.Version,
				Now:             task.HeartbeatAt.Add(time.Second),
				LeaseExpiresAt:  task.HeartbeatAt.Add(time.Minute),
			}
			tc.mutate(&command)

			updated, err := repo.TryStartTask(ctx, command)
			require.Error(t, err)
			assert.Nil(t, updated)

			persisted, getErr := repo.GetTask(ctx, task.TenantID, task.ID)
			require.NoError(t, getErr)
			assert.Equal(t, types.EvaluationStatuePending, persisted.Status)
			assert.Equal(t, uint64(1), persisted.Version)
		})
	}
}

func TestEvaluationTaskRepositoryRejectsLifecycleStatusMismatch(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	repo := NewEvaluationTaskRepository(db)
	ctx := context.Background()
	task := newEvaluationTaskEntity(23, "status-cas")
	require.NoError(t, repo.CreateTask(ctx, task.TenantID, task))

	progress, err := repo.PublishProgress(ctx, types.EvaluationTaskProgressCommand{
		TenantID:        task.TenantID,
		TaskID:          task.ID,
		OwnerID:         task.OwnerID,
		ExpectedVersion: task.Version,
		Total:           1,
		Finished:        1,
		Now:             task.HeartbeatAt.Add(time.Second),
		LeaseExpiresAt:  task.HeartbeatAt.Add(time.Minute),
	})
	require.Error(t, err)
	assert.Nil(t, progress)

	started, err := repo.TryStartTask(ctx, types.EvaluationTaskStartCommand{
		TenantID:        task.TenantID,
		TaskID:          task.ID,
		OwnerID:         task.OwnerID,
		ExpectedVersion: task.Version,
		Now:             task.HeartbeatAt.Add(time.Second),
		LeaseExpiresAt:  task.HeartbeatAt.Add(time.Minute),
	})
	require.NoError(t, err)

	again, err := repo.TryStartTask(ctx, types.EvaluationTaskStartCommand{
		TenantID:        task.TenantID,
		TaskID:          task.ID,
		OwnerID:         task.OwnerID,
		ExpectedVersion: started.Version,
		Now:             started.HeartbeatAt.Add(time.Second),
		LeaseExpiresAt:  started.HeartbeatAt.Add(time.Minute),
	})
	require.Error(t, err)
	assert.Nil(t, again)

	persisted, err := repo.GetTask(ctx, task.TenantID, task.ID)
	require.NoError(t, err)
	assert.Equal(t, types.EvaluationStatueRunning, persisted.Status)
	assert.Equal(t, uint64(2), persisted.Version)
}

func TestEvaluationTaskRepositoryRejectsProgressAfterTerminalPublication(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	repo := NewEvaluationTaskRepository(db)
	ctx := context.Background()
	task := newEvaluationTaskEntity(24, "late-progress")
	require.NoError(t, repo.CreateTask(ctx, task.TenantID, task))

	started, err := repo.TryStartTask(ctx, types.EvaluationTaskStartCommand{
		TenantID:        task.TenantID,
		TaskID:          task.ID,
		OwnerID:         task.OwnerID,
		ExpectedVersion: task.Version,
		Now:             task.HeartbeatAt.Add(time.Second),
		LeaseExpiresAt:  task.HeartbeatAt.Add(time.Minute),
	})
	require.NoError(t, err)
	terminal, err := repo.PublishTerminal(ctx, types.EvaluationTaskTerminalCommand{
		TenantID:        task.TenantID,
		TaskID:          task.ID,
		OwnerID:         task.OwnerID,
		ExpectedVersion: started.Version,
		Status:          types.EvaluationStatueSuccess,
		EndTime:         started.HeartbeatAt.Add(time.Second),
		CleanupErrors:   types.JSON(`[]`),
	})
	require.NoError(t, err)

	late, err := repo.PublishProgress(ctx, types.EvaluationTaskProgressCommand{
		TenantID:        task.TenantID,
		TaskID:          task.ID,
		OwnerID:         task.OwnerID,
		ExpectedVersion: terminal.Version,
		Total:           10,
		Finished:        10,
		Metric:          types.JSON(`{"retrieval_metrics":{"recall":0}}`),
		Now:             *terminal.EndTime,
		LeaseExpiresAt:  terminal.EndTime.Add(time.Minute),
	})
	require.Error(t, err)
	assert.Nil(t, late)

	persisted, err := repo.GetTask(ctx, task.TenantID, task.ID)
	require.NoError(t, err)
	assert.Equal(t, types.EvaluationStatueSuccess, persisted.Status)
	assert.Equal(t, terminal.Version, persisted.Version)
	assert.Empty(t, persisted.Metric)
	assert.Nil(t, persisted.LeaseExpiresAt)
}

func TestEvaluationTaskRepositoryProgressDoesNotShortenNewerHeartbeatLease(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	repo := NewEvaluationTaskRepository(db)
	ctx := context.Background()
	task := newEvaluationTaskEntity(28, "monotonic-lease")
	require.NoError(t, repo.CreateTask(ctx, task.TenantID, task))
	started, err := repo.TryStartTask(ctx, types.EvaluationTaskStartCommand{
		TenantID:        task.TenantID,
		TaskID:          task.ID,
		OwnerID:         task.OwnerID,
		ExpectedVersion: task.Version,
		Now:             task.HeartbeatAt.Add(time.Second),
		LeaseExpiresAt:  task.HeartbeatAt.Add(time.Minute),
	})
	require.NoError(t, err)

	newerHeartbeat := started.HeartbeatAt.Add(30 * time.Second)
	newerLease := newerHeartbeat.Add(2 * time.Minute)
	require.NoError(t, db.Model(&types.EvaluationTaskEntity{}).
		Where("tenant_id = ? AND id = ?", task.TenantID, task.ID).
		Updates(map[string]any{
			"heartbeat_at":     newerHeartbeat,
			"lease_expires_at": newerLease,
			"updated_at":       newerHeartbeat,
		}).Error)

	progress, err := repo.PublishProgress(ctx, types.EvaluationTaskProgressCommand{
		TenantID:        task.TenantID,
		TaskID:          task.ID,
		OwnerID:         task.OwnerID,
		ExpectedVersion: started.Version,
		Total:           1,
		Finished:        1,
		Metric:          types.JSON(`{"retrieval_metrics":{}}`),
		Now:             started.HeartbeatAt.Add(10 * time.Second),
		LeaseExpiresAt:  started.HeartbeatAt.Add(70 * time.Second),
	})
	require.NoError(t, err)
	require.NotNil(t, progress.LeaseExpiresAt)
	assert.Equal(t, newerHeartbeat.UTC(), progress.HeartbeatAt)
	assert.Equal(t, newerLease.UTC(), *progress.LeaseExpiresAt)
	assert.Equal(t, newerHeartbeat.UTC(), progress.UpdatedAt)
}

func TestEvaluationTaskRepositoryRejectsTerminalTimeBeforeLatestActivity(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	repo := NewEvaluationTaskRepository(db)
	ctx := context.Background()
	task := newEvaluationTaskEntity(29, "terminal-time")
	require.NoError(t, repo.CreateTask(ctx, task.TenantID, task))
	started, err := repo.TryStartTask(ctx, types.EvaluationTaskStartCommand{
		TenantID:        task.TenantID,
		TaskID:          task.ID,
		OwnerID:         task.OwnerID,
		ExpectedVersion: task.Version,
		Now:             task.HeartbeatAt.Add(10 * time.Second),
		LeaseExpiresAt:  task.HeartbeatAt.Add(time.Minute),
	})
	require.NoError(t, err)

	terminal, err := repo.PublishTerminal(ctx, types.EvaluationTaskTerminalCommand{
		TenantID:        task.TenantID,
		TaskID:          task.ID,
		OwnerID:         task.OwnerID,
		ExpectedVersion: started.Version,
		Status:          types.EvaluationStatueSuccess,
		EndTime:         started.HeartbeatAt.Add(-time.Second),
		CleanupErrors:   types.JSON(`[]`),
	})
	require.ErrorIs(t, err, ErrEvaluationTaskStateConflict)
	assert.Nil(t, terminal)
}

func TestEvaluationTaskRepositoryRejectsSuccessfulTerminalWithoutAllResultRows(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	repo := NewEvaluationTaskRepository(db)
	ctx := context.Background()
	task := newEvaluationTaskEntity(29, "incomplete-success")
	require.NoError(t, repo.CreateTask(ctx, task.TenantID, task))
	started, err := repo.TryStartTask(ctx, types.EvaluationTaskStartCommand{
		TenantID: task.TenantID, TaskID: task.ID, OwnerID: task.OwnerID,
		ExpectedVersion: task.Version, Now: task.HeartbeatAt.Add(time.Second),
		LeaseExpiresAt: task.HeartbeatAt.Add(time.Minute),
	})
	require.NoError(t, err)
	progress, err := repo.PublishProgress(ctx, types.EvaluationTaskProgressCommand{
		TenantID: task.TenantID, TaskID: task.ID, OwnerID: task.OwnerID,
		ExpectedVersion: started.Version, Total: 2, Finished: 2,
		Now: started.HeartbeatAt.Add(time.Second), LeaseExpiresAt: started.HeartbeatAt.Add(time.Minute),
	})
	require.NoError(t, err)

	terminal, err := repo.PublishTerminal(ctx, types.EvaluationTaskTerminalCommand{
		TenantID: task.TenantID, TaskID: task.ID, OwnerID: task.OwnerID,
		ExpectedVersion: progress.Version, Status: types.EvaluationStatueSuccess,
		EndTime: progress.HeartbeatAt.Add(time.Second), CleanupErrors: types.JSON(`[]`),
	})
	require.ErrorIs(t, err, ErrEvaluationTaskStateConflict)
	require.Nil(t, terminal)
	persisted, getErr := repo.GetTask(ctx, task.TenantID, task.ID)
	require.NoError(t, getErr)
	assert.Equal(t, types.EvaluationStatueRunning, persisted.Status)
	assert.Equal(t, 2, persisted.Total)
	assert.Equal(t, 2, persisted.Finished)
	assert.Equal(t, progress.Version, persisted.Version)
	assert.NotNil(t, persisted.LeaseExpiresAt)
}

func TestEvaluationTaskRepositoryClassifiesLifecycleConflicts(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	repo := NewEvaluationTaskRepository(db)
	ctx := context.Background()
	task := newEvaluationTaskEntity(25, "classified-conflicts")
	require.NoError(t, repo.CreateTask(ctx, task.TenantID, task))

	base := types.EvaluationTaskStartCommand{
		TenantID:        task.TenantID,
		TaskID:          task.ID,
		OwnerID:         task.OwnerID,
		ExpectedVersion: task.Version,
		Now:             task.HeartbeatAt.Add(time.Second),
		LeaseExpiresAt:  task.HeartbeatAt.Add(time.Minute),
	}

	wrongTenant := base
	wrongTenant.TenantID++
	_, err := repo.TryStartTask(ctx, wrongTenant)
	require.ErrorIs(t, err, ErrEvaluationTaskNotFound)

	wrongOwner := base
	wrongOwner.OwnerID = "another-owner"
	_, err = repo.TryStartTask(ctx, wrongOwner)
	require.ErrorIs(t, err, ErrEvaluationTaskOwnerConflict)

	staleVersion := base
	staleVersion.ExpectedVersion++
	_, err = repo.TryStartTask(ctx, staleVersion)
	require.ErrorIs(t, err, ErrEvaluationTaskVersionConflict)

	expiredTask := newEvaluationTaskEntity(25, "expired-start")
	expiredTask.LeaseExpiresAt = ptrToTime(expiredTask.HeartbeatAt.Add(time.Second))
	require.NoError(t, repo.CreateTask(ctx, expiredTask.TenantID, expiredTask))
	expiredStart := base
	expiredStart.TaskID = expiredTask.ID
	expiredStart.OwnerID = expiredTask.OwnerID
	expiredStart.Now = expiredTask.HeartbeatAt.Add(2 * time.Second)
	expiredStart.LeaseExpiresAt = expiredStart.Now.Add(time.Minute)
	_, err = repo.TryStartTask(ctx, expiredStart)
	require.ErrorIs(t, err, ErrEvaluationTaskStateConflict)
}

func TestEvaluationTaskRepositoryDoesNotOverwriteTemporaryKnowledge(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	repo := NewEvaluationTaskRepository(db)
	ctx := context.Background()
	task := newEvaluationTaskEntity(26, "immutable-knowledge")
	require.NoError(t, repo.CreateTask(ctx, task.TenantID, task))

	started, err := repo.TryStartTask(ctx, types.EvaluationTaskStartCommand{
		TenantID:        task.TenantID,
		TaskID:          task.ID,
		OwnerID:         task.OwnerID,
		ExpectedVersion: task.Version,
		Now:             task.HeartbeatAt.Add(time.Second),
		LeaseExpiresAt:  task.HeartbeatAt.Add(time.Minute),
	})
	require.NoError(t, err)

	first, err := repo.RecordTemporaryKnowledge(ctx, types.EvaluationTaskKnowledgeCommand{
		TenantID:             task.TenantID,
		TaskID:               task.ID,
		OwnerID:              task.OwnerID,
		ExpectedVersion:      started.Version,
		TemporaryKnowledgeID: "knowledge-first",
		UpdatedAt:            started.HeartbeatAt.Add(time.Second),
	})
	require.NoError(t, err)

	overwrite, err := repo.RecordTemporaryKnowledge(ctx, types.EvaluationTaskKnowledgeCommand{
		TenantID:             task.TenantID,
		TaskID:               task.ID,
		OwnerID:              task.OwnerID,
		ExpectedVersion:      first.Version,
		TemporaryKnowledgeID: "knowledge-second",
		UpdatedAt:            first.UpdatedAt.Add(time.Second),
	})
	require.ErrorIs(t, err, ErrEvaluationTaskStateConflict)
	assert.Nil(t, overwrite)

	persisted, err := repo.GetTask(ctx, task.TenantID, task.ID)
	require.NoError(t, err)
	assert.Equal(t, "knowledge-first", persisted.TemporaryKnowledgeID)
	assert.Equal(t, first.Version, persisted.Version)
}

func TestEvaluationTaskRepositoryAllowsOnlyOneConcurrentProgressWriter(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	repo := NewEvaluationTaskRepository(db)
	ctx := context.Background()
	task := newEvaluationTaskEntity(26, "concurrent-progress")
	require.NoError(t, repo.CreateTask(ctx, task.TenantID, task))
	started, err := repo.TryStartTask(ctx, types.EvaluationTaskStartCommand{
		TenantID:        task.TenantID,
		TaskID:          task.ID,
		OwnerID:         task.OwnerID,
		ExpectedVersion: task.Version,
		Now:             task.HeartbeatAt.Add(time.Second),
		LeaseExpiresAt:  task.HeartbeatAt.Add(time.Minute),
	})
	require.NoError(t, err)

	start := make(chan struct{})
	errorsByWriter := make(chan error, 2)
	var workers sync.WaitGroup
	for writer := 0; writer < 2; writer++ {
		writer := writer
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			_, updateErr := repo.PublishProgress(ctx, types.EvaluationTaskProgressCommand{
				TenantID:        task.TenantID,
				TaskID:          task.ID,
				OwnerID:         task.OwnerID,
				ExpectedVersion: started.Version,
				Total:           2,
				Finished:        1,
				Metric:          types.JSON(`{"retrieval_metrics":{}}`),
				Now:             started.HeartbeatAt.Add(time.Duration(writer+1) * time.Second),
				LeaseExpiresAt:  started.HeartbeatAt.Add(time.Minute),
			})
			errorsByWriter <- updateErr
		}()
	}
	close(start)
	workers.Wait()
	close(errorsByWriter)

	var successes, versionConflicts int
	for updateErr := range errorsByWriter {
		switch {
		case updateErr == nil:
			successes++
		case errors.Is(updateErr, ErrEvaluationTaskVersionConflict):
			versionConflicts++
		default:
			t.Fatalf("unexpected concurrent update error: %v", updateErr)
		}
	}
	assert.Equal(t, 1, successes)
	assert.Equal(t, 1, versionConflicts)

	persisted, err := repo.GetTask(ctx, task.TenantID, task.ID)
	require.NoError(t, err)
	assert.Equal(t, uint64(3), persisted.Version)
	assert.Equal(t, 2, persisted.Total)
	assert.Equal(t, 1, persisted.Finished)
}

func TestEvaluationTaskLifecycleNeverModifiesExperimentSnapshot(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	repo := NewEvaluationTaskRepository(db)
	questionRepo := NewEvaluationQuestionResultRepository(db)
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC)

	task := newEvaluationTaskEntity(23, "experiment-frozen")
	task.LeaseExpiresAt = ptrToTime(now.Add(time.Minute))
	datasetVersionID := "dataset-version-1"
	contentSHA256 := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	experimentSHA256 := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	snapshot := types.JSON(`{"schema_version":1,"dataset":{"dataset_id":"default"}}`)
	task.DatasetVersionID = &datasetVersionID
	task.DatasetContentSHA256 = &contentSHA256
	task.ExperimentSnapshot = snapshot
	task.ExperimentSHA256 = &experimentSHA256
	require.NoError(t, repo.CreateTask(ctx, task.TenantID, task))

	assertFrozen := func(stage string, entity *types.EvaluationTaskEntity) {
		t.Helper()
		require.NotNil(t, entity, stage)
		require.NotNil(t, entity.DatasetVersionID, stage)
		assert.Equal(t, datasetVersionID, *entity.DatasetVersionID, stage)
		require.NotNil(t, entity.DatasetContentSHA256, stage)
		assert.Equal(t, contentSHA256, *entity.DatasetContentSHA256, stage)
		assert.JSONEq(t, string(snapshot), string(entity.ExperimentSnapshot), stage)
		require.NotNil(t, entity.ExperimentSHA256, stage)
		assert.Equal(t, experimentSHA256, *entity.ExperimentSHA256, stage)
	}

	started, err := repo.TryStartTask(ctx, types.EvaluationTaskStartCommand{
		TenantID: task.TenantID, TaskID: task.ID, OwnerID: task.OwnerID,
		ExpectedVersion: task.Version, Now: now, LeaseExpiresAt: now.Add(time.Minute),
	})
	require.NoError(t, err)
	assertFrozen("TryStartTask", started)

	progress := started
	for sampleIndex := 0; sampleIndex < 2; sampleIndex++ {
		command := newEvaluationQuestionCommandFixture(task, progress.Version, sampleIndex)
		command.Total = 2
		command.Metric = types.JSON(`{"retrieval_metrics":{"precision":0.5}}`)
		command.Now = now.Add(time.Duration(sampleIndex+1) * time.Second)
		command.LeaseExpiresAt = now.Add(time.Minute)
		progress, _, err = questionRepo.PublishQuestionResult(ctx, command)
		require.NoError(t, err)
	}
	assertFrozen("PublishQuestionResult", progress)

	terminal, err := repo.PublishTerminal(ctx, types.EvaluationTaskTerminalCommand{
		TenantID: task.TenantID, TaskID: task.ID, OwnerID: task.OwnerID,
		ExpectedVersion: progress.Version, Status: types.EvaluationStatueSuccess,
		EndTime: now.Add(20 * time.Second),
		Metric:  types.JSON(`{"retrieval_metrics":{"precision":0.5}}`),
	})
	require.NoError(t, err)
	assertFrozen("PublishTerminal", terminal)

	// A fresh read after the full lifecycle still returns the frozen bytes.
	persisted, err := repo.GetTask(ctx, task.TenantID, task.ID)
	require.NoError(t, err)
	assertFrozen("GetTask after terminal", persisted)
}

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvaluationTaskInterruptedStatusHasStableNumericValue(t *testing.T) {
	assert.Equal(t, types.EvaluationStatue(5), types.EvaluationStatueInterrupted)
}

func TestEvaluationTaskRepositoryHeartbeatRenewsRunningLeaseWithoutChangingVersion(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	repo := NewEvaluationTaskRepository(db)
	ctx := context.Background()
	task := newEvaluationTaskEntity(31, "heartbeat")
	require.NoError(t, repo.CreateTask(ctx, task.TenantID, task))

	startedAt := task.StartTime.Add(10 * time.Second)
	started, err := repo.TryStartTask(ctx, types.EvaluationTaskStartCommand{
		TenantID:        task.TenantID,
		TaskID:          task.ID,
		OwnerID:         task.OwnerID,
		ExpectedVersion: task.Version,
		Now:             startedAt,
		LeaseExpiresAt:  startedAt.Add(time.Minute),
	})
	require.NoError(t, err)

	heartbeatAt := startedAt.Add(20 * time.Second)
	leaseExpiresAt := heartbeatAt.Add(2 * time.Minute)
	heartbeat, err := repo.HeartbeatTask(ctx, types.EvaluationTaskHeartbeatCommand{
		TenantID:       task.TenantID,
		TaskID:         task.ID,
		OwnerID:        task.OwnerID,
		Now:            heartbeatAt,
		LeaseExpiresAt: leaseExpiresAt,
	})
	require.NoError(t, err)
	require.NotNil(t, heartbeat)
	assert.Equal(t, started.Version, heartbeat.Version)
	assert.Equal(t, heartbeatAt.UTC(), heartbeat.HeartbeatAt)
	require.NotNil(t, heartbeat.LeaseExpiresAt)
	assert.Equal(t, leaseExpiresAt.UTC(), *heartbeat.LeaseExpiresAt)
	assert.Equal(t, heartbeatAt.UTC(), heartbeat.UpdatedAt)

	stale, err := repo.HeartbeatTask(ctx, types.EvaluationTaskHeartbeatCommand{
		TenantID:       task.TenantID,
		TaskID:         task.ID,
		OwnerID:        task.OwnerID,
		Now:            heartbeatAt.Add(-time.Second),
		LeaseExpiresAt: leaseExpiresAt.Add(-time.Second),
	})
	require.NoError(t, err)
	require.NotNil(t, stale)
	assert.Equal(t, started.Version, stale.Version)
	assert.Equal(t, heartbeatAt.UTC(), stale.HeartbeatAt)
	require.NotNil(t, stale.LeaseExpiresAt)
	assert.Equal(t, leaseExpiresAt.UTC(), *stale.LeaseExpiresAt)
	assert.Equal(t, heartbeatAt.UTC(), stale.UpdatedAt)
}

func TestEvaluationTaskRepositoryHeartbeatRequiresRunningOwner(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	repo := NewEvaluationTaskRepository(db)
	ctx := context.Background()
	task := newEvaluationTaskEntity(32, "heartbeat-guards")
	require.NoError(t, repo.CreateTask(ctx, task.TenantID, task))

	command := types.EvaluationTaskHeartbeatCommand{
		TenantID:       task.TenantID,
		TaskID:         task.ID,
		OwnerID:        task.OwnerID,
		Now:            task.HeartbeatAt.Add(10 * time.Second),
		LeaseExpiresAt: task.HeartbeatAt.Add(2 * time.Minute),
	}
	got, err := repo.HeartbeatTask(ctx, command)
	require.ErrorIs(t, err, ErrEvaluationTaskStateConflict)
	assert.Nil(t, got)

	started, err := repo.TryStartTask(ctx, types.EvaluationTaskStartCommand{
		TenantID:        task.TenantID,
		TaskID:          task.ID,
		OwnerID:         task.OwnerID,
		ExpectedVersion: task.Version,
		Now:             task.HeartbeatAt.Add(time.Second),
		LeaseExpiresAt:  task.HeartbeatAt.Add(time.Minute),
	})
	require.NoError(t, err)

	command.OwnerID = "different-owner"
	got, err = repo.HeartbeatTask(ctx, command)
	require.ErrorIs(t, err, ErrEvaluationTaskOwnerConflict)
	assert.Nil(t, got)

	terminal, err := repo.PublishTerminal(ctx, types.EvaluationTaskTerminalCommand{
		TenantID:        task.TenantID,
		TaskID:          task.ID,
		OwnerID:         task.OwnerID,
		ExpectedVersion: started.Version,
		Status:          types.EvaluationStatueInterrupted,
		EndTime:         command.Now,
		ErrMsg:          "execution lease expired",
		CleanupErrors:   types.JSON(`[]`),
	})
	require.NoError(t, err)
	assert.Equal(t, types.EvaluationStatueInterrupted, terminal.Status)
	assert.Nil(t, terminal.LeaseExpiresAt)

	command.OwnerID = task.OwnerID
	command.Now = command.Now.Add(time.Second)
	command.LeaseExpiresAt = command.Now.Add(time.Minute)
	got, err = repo.HeartbeatTask(ctx, command)
	require.ErrorIs(t, err, ErrEvaluationTaskStateConflict)
	assert.Nil(t, got)
}

func TestEvaluationTaskRepositoryHeartbeatRejectsExpiredLease(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	repo := NewEvaluationTaskRepository(db)
	ctx := context.Background()
	task := newEvaluationTaskEntity(33, "heartbeat-expired")
	require.NoError(t, repo.CreateTask(ctx, task.TenantID, task))

	startedAt := task.StartTime.Add(10 * time.Second)
	expiresAt := startedAt.Add(2 * time.Minute)
	started, err := repo.TryStartTask(ctx, types.EvaluationTaskStartCommand{
		TenantID:        task.TenantID,
		TaskID:          task.ID,
		OwnerID:         task.OwnerID,
		ExpectedVersion: task.Version,
		Now:             startedAt,
		LeaseExpiresAt:  expiresAt,
	})
	require.NoError(t, err)

	heartbeat, err := repo.HeartbeatTask(ctx, types.EvaluationTaskHeartbeatCommand{
		TenantID:       task.TenantID,
		TaskID:         task.ID,
		OwnerID:        task.OwnerID,
		Now:            expiresAt,
		LeaseExpiresAt: expiresAt.Add(time.Minute),
	})
	require.ErrorIs(t, err, ErrEvaluationTaskStateConflict)
	assert.Nil(t, heartbeat)

	persisted, err := repo.GetTask(ctx, task.TenantID, task.ID)
	require.NoError(t, err)
	assert.Equal(t, started.Version, persisted.Version)
	assert.Equal(t, started.HeartbeatAt, persisted.HeartbeatAt)
	assert.Equal(t, started.LeaseExpiresAt, persisted.LeaseExpiresAt)
}

func TestEvaluationTaskRepositoryRejectsExpiredOwnerLifecycleWrites(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(
			context.Context,
			interfaces.EvaluationTaskRepository,
			*types.EvaluationTaskEntity,
			time.Time,
		) (*types.EvaluationTaskEntity, error)
	}{
		{
			name: "progress",
			mutate: func(
				ctx context.Context,
				repo interfaces.EvaluationTaskRepository,
				task *types.EvaluationTaskEntity,
				expiresAt time.Time,
			) (*types.EvaluationTaskEntity, error) {
				return repo.PublishProgress(ctx, types.EvaluationTaskProgressCommand{
					TenantID:        task.TenantID,
					TaskID:          task.ID,
					OwnerID:         task.OwnerID,
					ExpectedVersion: task.Version,
					Total:           1,
					Finished:        1,
					Now:             expiresAt,
					LeaseExpiresAt:  expiresAt.Add(time.Minute),
				})
			},
		},
		{
			name: "temporary knowledge",
			mutate: func(
				ctx context.Context,
				repo interfaces.EvaluationTaskRepository,
				task *types.EvaluationTaskEntity,
				expiresAt time.Time,
			) (*types.EvaluationTaskEntity, error) {
				return repo.RecordTemporaryKnowledge(ctx, types.EvaluationTaskKnowledgeCommand{
					TenantID:             task.TenantID,
					TaskID:               task.ID,
					OwnerID:              task.OwnerID,
					ExpectedVersion:      task.Version,
					TemporaryKnowledgeID: "expired-owner-knowledge",
					UpdatedAt:            expiresAt,
				})
			},
		},
		{
			name: "terminal",
			mutate: func(
				ctx context.Context,
				repo interfaces.EvaluationTaskRepository,
				task *types.EvaluationTaskEntity,
				expiresAt time.Time,
			) (*types.EvaluationTaskEntity, error) {
				return repo.PublishTerminal(ctx, types.EvaluationTaskTerminalCommand{
					TenantID:        task.TenantID,
					TaskID:          task.ID,
					OwnerID:         task.OwnerID,
					ExpectedVersion: task.Version,
					Status:          types.EvaluationStatueFailed,
					EndTime:         expiresAt,
					ErrMsg:          "expired owner",
					CleanupErrors:   types.JSON(`[]`),
				})
			},
		},
	}

	for index, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := setupEvaluationTaskRepositoryTestDB(t)
			repo := NewEvaluationTaskRepository(db)
			ctx := context.Background()
			task := newEvaluationTaskEntity(uint64(34+index), "expired-owner-"+tc.name)
			require.NoError(t, repo.CreateTask(ctx, task.TenantID, task))

			startedAt := task.StartTime.Add(10 * time.Second)
			expiresAt := startedAt.Add(2 * time.Minute)
			started, err := repo.TryStartTask(ctx, types.EvaluationTaskStartCommand{
				TenantID:        task.TenantID,
				TaskID:          task.ID,
				OwnerID:         task.OwnerID,
				ExpectedVersion: task.Version,
				Now:             startedAt,
				LeaseExpiresAt:  expiresAt,
			})
			require.NoError(t, err)

			updated, err := tc.mutate(ctx, repo, started, expiresAt)
			require.ErrorIs(t, err, ErrEvaluationTaskStateConflict)
			assert.Nil(t, updated)

			persisted, err := repo.GetTask(ctx, task.TenantID, task.ID)
			require.NoError(t, err)
			assert.Equal(t, types.EvaluationStatueRunning, persisted.Status)
			assert.Equal(t, started.Version, persisted.Version)
			assert.Equal(t, started.LeaseExpiresAt, persisted.LeaseExpiresAt)
			assert.Zero(t, persisted.Total)
			assert.Zero(t, persisted.Finished)
			assert.Empty(t, persisted.TemporaryKnowledgeID)
			assert.Nil(t, persisted.EndTime)
		})
	}
}

func TestEvaluationTaskRepositoryClaimsExpiredTasksFromSQLiteProductionMigration(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	repo := NewEvaluationTaskRepository(db)
	ctx := context.Background()
	claimAt := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)

	pending := newRecoveryTask(41, "claim-pending", claimAt.Add(-2*time.Hour), claimAt.Add(-time.Minute))
	require.NoError(t, repo.CreateTask(ctx, pending.TenantID, pending))

	running := newRecoveryTask(42, "claim-running", claimAt.Add(-2*time.Hour), claimAt.Add(-time.Hour))
	require.NoError(t, repo.CreateTask(ctx, running.TenantID, running))
	running, err := repo.TryStartTask(ctx, types.EvaluationTaskStartCommand{
		TenantID:        running.TenantID,
		TaskID:          running.ID,
		OwnerID:         running.OwnerID,
		ExpectedVersion: running.Version,
		Now:             running.StartTime.Add(time.Minute),
		LeaseExpiresAt:  claimAt,
	})
	require.NoError(t, err)

	fresh := newRecoveryTask(43, "claim-fresh", claimAt.Add(-time.Hour), claimAt.Add(time.Minute))
	require.NoError(t, repo.CreateTask(ctx, fresh.TenantID, fresh))

	newLease := claimAt.Add(30 * time.Second)
	claimed, err := repo.ClaimExpiredTasks(ctx, types.EvaluationTaskClaimExpiredCommand{
		OwnerID:        "recovery-owner-a",
		Now:            claimAt,
		LeaseExpiresAt: newLease,
		Limit:          10,
	})
	require.NoError(t, err)
	require.Len(t, claimed, 2)
	assert.ElementsMatch(t, []string{pending.ID, running.ID}, []string{claimed[0].ID, claimed[1].ID})

	versions := map[string]uint64{pending.ID: pending.Version + 1, running.ID: running.Version + 1}
	statuses := map[string]types.EvaluationStatue{
		pending.ID: types.EvaluationStatuePending,
		running.ID: types.EvaluationStatueRunning,
	}
	for _, task := range claimed {
		assert.Equal(t, "recovery-owner-a", task.OwnerID)
		assert.Equal(t, versions[task.ID], task.Version)
		assert.Equal(t, statuses[task.ID], task.Status)
		assert.Equal(t, claimAt.UTC(), task.HeartbeatAt)
		require.NotNil(t, task.LeaseExpiresAt)
		assert.Equal(t, newLease.UTC(), *task.LeaseExpiresAt)
		assert.False(t, task.UpdatedAt.Before(claimAt))
	}

	none, err := repo.ClaimExpiredTasks(ctx, types.EvaluationTaskClaimExpiredCommand{
		OwnerID:        "recovery-owner-b",
		Now:            claimAt,
		LeaseExpiresAt: newLease,
		Limit:          10,
	})
	require.NoError(t, err)
	assert.Empty(t, none)

	reclaimed, err := repo.ClaimExpiredTasks(ctx, types.EvaluationTaskClaimExpiredCommand{
		OwnerID:        "recovery-owner-b",
		Now:            newLease,
		LeaseExpiresAt: newLease.Add(time.Minute),
		Limit:          1,
	})
	require.NoError(t, err)
	require.Len(t, reclaimed, 1)
	assert.Equal(t, "recovery-owner-b", reclaimed[0].OwnerID)
	assert.Equal(t, versions[reclaimed[0].ID]+1, reclaimed[0].Version)

	persistedFresh, err := repo.GetTask(ctx, fresh.TenantID, fresh.ID)
	require.NoError(t, err)
	assert.Equal(t, fresh.OwnerID, persistedFresh.OwnerID)
	assert.Equal(t, fresh.Version, persistedFresh.Version)
}

func newRecoveryTask(tenantID uint64, suffix string, startTime, leaseExpiresAt time.Time) *types.EvaluationTaskEntity {
	return &types.EvaluationTaskEntity{
		ID:                       "evaluation-" + suffix,
		TenantID:                 tenantID,
		DatasetID:                "default",
		StartTime:                startTime,
		TemporaryKnowledgeBaseID: "kb-" + suffix,
		OwnerID:                  "owner-" + suffix,
		LeaseExpiresAt:           ptrToTime(leaseExpiresAt),
		HeartbeatAt:              startTime,
		CreatedAt:                startTime,
		UpdatedAt:                startTime,
	}
}

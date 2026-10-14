package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type resumedSyncQueue struct{ tasks chan *asynq.Task }

func (q resumedSyncQueue) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	q.tasks <- task
	return &asynq.TaskInfo{ID: "scheduled-again"}, nil
}

func TestProcessSyncTerminalCleanupRestoresScheduledEligibility(t *testing.T) {
	f := newSQLiteDataSourceDeleteFixture(t)
	sqlDB, err := f.db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	svc := &DataSourceService{dsRepo: f.dsRepo, syncLogRepo: f.syncLogRepo}
	payload, err := json.Marshal(types.DataSourceSyncPayload{
		DataSourceID: f.ds.ID, TenantID: f.ds.TenantID, SyncLogID: f.runningLog.ID,
	})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(types.WithTaskRetryMetadata(context.Background(), 5, 5))
	cancel()
	require.ErrorIs(t, svc.ProcessSync(ctx, asynq.NewTask(types.TypeDataSourceSync, payload)), context.Canceled)
	running, err := f.syncLogRepo.HasRunningSync(context.Background(), f.ds.ID)
	require.NoError(t, err)
	assert.False(t, running)
	f.ds.SyncSchedule = "* * * * * *"
	require.NoError(t, f.dsRepo.Update(context.Background(), f.ds))
	queue := resumedSyncQueue{tasks: make(chan *asynq.Task, 1)}
	scheduler := datasource.NewScheduler(f.dsRepo, f.syncLogRepo, queue)
	require.NoError(t, scheduler.Start(context.Background()))
	defer scheduler.Stop()
	select {
	case task := <-queue.tasks:
		var next types.DataSourceSyncPayload
		require.NoError(t, json.Unmarshal(task.Payload(), &next))
		assert.Equal(t, f.ds.ID, next.DataSourceID)
		assert.Equal(t, "schedule", next.Trigger)
		assert.NotEqual(t, f.runningLog.ID, next.SyncLogID)
	case <-time.After(3 * time.Second):
		t.Fatal("scheduler did not enqueue after terminal log cleanup")
	}
}

func TestProcessSyncRetryableCancellationKeepsLogPending(t *testing.T) {
	svc, _, repo := newConcurrentSyncService(t)
	ctx, cancel := context.WithCancel(types.WithTaskRetryMetadata(context.Background(), 4, 5))
	cancel()
	require.ErrorIs(t, svc.ProcessSync(ctx, concurrentSyncTask(t, "first")), context.Canceled)
	log, err := svc.syncLogRepo.FindByID(context.Background(), "first")
	require.NoError(t, err)
	assert.Equal(t, types.SyncLogStatusRunning, log.Status)
	assert.Nil(t, log.FinishedAt)
	assert.Zero(t, repo.writes)
}

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type deferredSyncQueue struct {
	task *asynq.Task
	opts []asynq.Option
	err  error
}

func (q *deferredSyncQueue) Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	q.task, q.opts = task, opts
	return &asynq.TaskInfo{ID: "deferred"}, q.err
}

func (q *deferredSyncQueue) remainingRetries() int {
	for _, opt := range q.opts {
		if opt.Type() == asynq.MaxRetryOpt {
			return opt.Value().(int)
		}
	}
	return -1
}

func TestProcessSyncContentionPreservesLastRetry(t *testing.T) {
	for _, backend := range []string{"lite", "redis"} {
		t.Run(backend, func(t *testing.T) { testSyncDeferralAtLastRetry(t, backend) })
	}
}

func testSyncDeferralAtLastRetry(t *testing.T, backend string) {
	t.Helper()
	svc, connector, repo := newConcurrentSyncService(t)
	queue := &deferredSyncQueue{}
	svc.taskEnqueuer = queue
	if backend == "redis" {
		svc.syncCoordinator.redis, _ = syncLockRedisClient(t)
	}
	ownerDone := make(chan error, 1)
	go func() { ownerDone <- svc.ProcessSync(context.Background(), concurrentSyncTask(t, "first")) }()
	<-connector.entered
	task := concurrentSyncTask(t, "second")
	ctx := types.WithTaskRetryMetadata(context.Background(), 5, 5)
	for range 7 {
		require.NoError(t, svc.ProcessSync(ctx, task))
		require.NotNil(t, queue.task)
		assert.Zero(t, queue.remainingRetries(), "contention must neither spend nor reset the failure budget")
		assert.Equal(t, "5", queue.task.Headers()[dataSourceSyncRetryOffsetHeader])
		assert.Equal(t, task.Payload(), queue.task.Payload())
		log, err := svc.syncLogRepo.FindByID(context.Background(), "second")
		require.NoError(t, err)
		assert.Equal(t, types.SyncLogStatusRunning, log.Status)
		task = queue.task
		ctx = types.WithTaskRetryMetadata(context.Background(), 0, 0)
	}
	assert.Equal(t, int32(1), connector.calls.Load())
	close(connector.release)
	require.NoError(t, <-ownerDone)
	require.NoError(t, svc.ProcessSync(ctx, task))
	assert.Equal(t, 2, repo.writes)
	assert.Equal(t, int32(2), connector.calls.Load())
}

func TestDeferSyncPreservesHeadersAndRemainingBudget(t *testing.T) {
	queue := &deferredSyncQueue{}
	svc := &DataSourceService{taskEnqueuer: queue}
	task := asynq.NewTaskWithHeaders(types.TypeDataSourceSync, []byte("{}"), map[string]string{"trace": "keep"})
	ctx := withDataSourceSyncAttempt(types.WithTaskRetryMetadata(context.Background(), 2, 5), task)
	require.NoError(t, svc.deferContendedSync(ctx, task))
	assert.Equal(t, 3, queue.remainingRetries())
	assert.Equal(t, "keep", queue.task.Headers()["trace"])
	assert.NotContains(t, task.Headers(), dataSourceSyncRetryOffsetHeader)
	assert.Equal(t, 2, dataSourceSyncAttempt(withDataSourceSyncAttempt(
		types.WithTaskRetryMetadata(context.Background(), 0, 3), queue.task)))
	var delay time.Duration
	for _, opt := range queue.opts {
		if opt.Type() == asynq.ProcessInOpt {
			delay = opt.Value().(time.Duration)
		}
	}
	assert.GreaterOrEqual(t, delay, dataSourceSyncDeferDelay)
	assert.Less(t, delay, 2*dataSourceSyncDeferDelay)
}

func TestProcessSyncDeferredEnqueueFailureFinalizesLog(t *testing.T) {
	svc, _, repo := newConcurrentSyncService(t)
	failure := errors.New("queue unavailable")
	svc.taskEnqueuer = &deferredSyncQueue{err: failure}
	ctx := types.WithTaskRetryMetadata(context.Background(), 5, 5)
	err := svc.syncCoordinator.run(context.Background(), t.Name(), func(context.Context) error {
		return svc.ProcessSync(ctx, concurrentSyncTask(t, "second"))
	})
	require.ErrorIs(t, err, failure)
	log, err := svc.syncLogRepo.FindByID(context.Background(), "second")
	require.NoError(t, err)
	assert.Equal(t, types.SyncLogStatusFailed, log.Status)
	assert.NotNil(t, log.FinishedAt)
	assert.Zero(t, repo.writes)
}

func TestProcessSyncDeferredFullSyncResumesCheckpoint(t *testing.T) {
	svc, connector, repo := newConcurrentSyncService(t)
	repo.source.SyncMode = types.SyncModeFull
	checkpoint := &types.SyncCursor{ConnectorCursor: map[string]interface{}{"revision": "accepted"}}
	var err error
	repo.source.LastSyncCursor, err = checkpoint.ToJSON()
	require.NoError(t, err)
	connector.calls.Store(1) // This attempt follows an earlier partially completed fetch.
	svc.connectorRegistry = datasource.NewConnectorRegistry()
	require.NoError(t, svc.connectorRegistry.Register(&cancelingSyncStream{concurrentSyncConnector: connector}))
	original := concurrentSyncTask(t, "first")
	task := asynq.NewTaskWithHeaders(original.Type(), original.Payload(),
		map[string]string{dataSourceSyncRetryOffsetHeader: "5"})
	ctx := types.WithTaskRetryMetadata(context.Background(), 0, 0)
	require.NoError(t, svc.ProcessSync(ctx, task))
	observed := <-connector.entered
	require.NotNil(t, observed.cursor, "a delayed retry must not restart a full import")
	assert.Equal(t, "accepted", observed.cursor.ConnectorCursor["revision"])
}

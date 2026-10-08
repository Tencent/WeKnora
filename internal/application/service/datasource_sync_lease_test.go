package service

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/common/redislock"
	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDataSourceSyncRedisLeaseRenewsWhileRunning(t *testing.T) {
	client, server := syncLockRedisClient(t)
	coordinator := &dataSourceSyncCoordinator{redis: client}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- coordinator.run(context.Background(), "source", func(context.Context) error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	key := dataSourceSyncLockKey("source")
	server.FastForward(20 * time.Second)
	assert.Eventually(t, func() bool { return server.TTL(key) > 20*time.Second },
		dataSourceSyncLockRenew+5*time.Second, 10*time.Millisecond)
	server.FastForward(20 * time.Second)
	assert.True(t, server.Exists(key), "renewal must keep a long-running owner's lease alive")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	contender := &dataSourceSyncCoordinator{redis: client}
	err := contender.run(ctx, "source", func(context.Context) error { return nil })
	assert.ErrorIs(t, err, redislock.ErrLockBusy)
	close(release)
	require.NoError(t, <-done)
	assert.False(t, server.Exists(key))
}

type interruptedCheckpointStream struct {
	*concurrentSyncConnector
	checkpointErr error
	emitErr       error
}

func (c *interruptedCheckpointStream) FetchStream(
	ctx context.Context, cfg *types.DataSourceConfig, cursor *types.SyncCursor, h datasource.StreamHandler,
) (*types.SyncCursor, error) {
	checkpoint := &types.SyncCursor{ConnectorCursor: map[string]interface{}{"revision": "checkpoint"}}
	if err := h.Checkpoint(ctx, checkpoint); err != nil {
		return nil, err
	}
	_, stale, err := c.FetchIncremental(ctx, cfg, cursor)
	if err != nil {
		return nil, err
	}
	c.checkpointErr = h.Checkpoint(ctx, stale)
	c.emitErr = h.Emit(ctx, types.FetchedItem{IsDeleted: true, ExternalID: "old-item"})
	// Even if a connector ignores callback errors, the worker must fail closed.
	return stale, nil
}

func TestProcessSyncStreamingOwnershipLossPreservesLastCheckpoint(t *testing.T) {
	svc, connector, repo := newConcurrentSyncService(t)
	client, server := syncLockRedisClient(t)
	svc.syncCoordinator.redis = client
	stream := &interruptedCheckpointStream{concurrentSyncConnector: connector}
	svc.connectorRegistry = datasource.NewConnectorRegistry()
	require.NoError(t, svc.connectorRegistry.Register(stream))
	task := concurrentSyncTask(t, "first")
	done := make(chan error, 1)
	go func() { done <- svc.ProcessSync(context.Background(), task) }()
	observation := <-connector.entered
	require.NoError(t, server.Set(dataSourceSyncLockKey(t.Name()), "replacement-owner"))
	select {
	case <-observation.ctx.Done():
	case <-time.After(dataSourceSyncLockRenew + 5*time.Second):
		close(connector.release)
		t.Fatal("ownership loss did not cancel the stream")
	}
	close(connector.release)
	require.ErrorIs(t, <-done, redislock.ErrLockOwnershipLost)
	assert.ErrorIs(t, stream.checkpointErr, context.Canceled)
	assert.ErrorIs(t, stream.emitErr, context.Canceled)
	assert.Equal(t, 1, repo.writes, "only the checkpoint made before ownership loss may be committed")
	cursor, err := repo.source.ParseSyncCursor()
	require.NoError(t, err)
	assert.Equal(t, "checkpoint", cursor.ConnectorCursor["revision"])
	log, err := svc.syncLogRepo.FindByID(context.Background(), "first")
	require.NoError(t, err)
	assert.Equal(t, types.SyncLogStatusFailed, log.Status)
	assert.NotNil(t, log.FinishedAt)
}

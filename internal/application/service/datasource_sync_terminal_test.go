package service

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcessSyncTerminalCancellationFinishesLog(t *testing.T) {
	svc, _, repo := newConcurrentSyncService(t)
	ctx, cancel := context.WithCancel(types.WithTaskRetryMetadata(context.Background(), 5, 5))
	cancel()
	require.ErrorIs(t, svc.ProcessSync(ctx, concurrentSyncTask(t, "first")), context.Canceled)
	log, err := svc.syncLogRepo.FindByID(context.Background(), "first")
	require.NoError(t, err)
	assert.Equal(t, types.SyncLogStatusFailed, log.Status)
	assert.NotNil(t, log.FinishedAt)
	assert.Zero(t, repo.writes, "cleanup must not publish a cursor or data-source state")
	assert.Equal(t, types.DataSourceStatusActive, repo.source.Status)
}

func TestDataSourceSyncContentionReturnsImmediately(t *testing.T) {
	for _, backend := range []string{"lite", "redis"} {
		t.Run(backend, func(t *testing.T) {
			c := &dataSourceSyncCoordinator{}
			if backend == "redis" {
				c.redis, _ = syncLockRedisClient(t)
			}
			entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			go func() {
				done <- c.run(context.Background(), "source", func(context.Context) error {
					close(entered)
					<-release
					return nil
				})
			}()
			<-entered
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			err := c.run(ctx, "source", func(context.Context) error {
				t.Error("contending callback ran")
				return nil
			})
			assert.Error(t, err)
			assert.NoError(t, ctx.Err(), "contention must return before the attempt times out")
			close(release)
			require.NoError(t, <-done)
		})
	}
}

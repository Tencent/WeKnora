package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/common/redislock"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func syncLockRedisClient(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	return client, server
}

func TestDataSourceSyncLockScopesAndCancellation(t *testing.T) {
	for _, backend := range []string{"lite", "redis"} {
		t.Run(backend, func(t *testing.T) {
			first := &dataSourceSyncCoordinator{}
			second := first
			if backend == "redis" {
				client, _ := syncLockRedisClient(t)
				first.redis = client
				second = &dataSourceSyncCoordinator{redis: client}
			}
			entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			go func() {
				done <- first.run(context.Background(), "same-source", func(context.Context) error {
					close(entered)
					<-release
					return nil
				})
			}()
			<-entered
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			ran := false
			err := second.run(ctx, "same-source", func(context.Context) error { ran = true; return nil })
			assert.ErrorIs(t, err, redislock.ErrLockBusy)
			assert.False(t, ran)
			err = second.run(context.Background(), "different-source", func(context.Context) error { return nil })
			assert.NoError(t, err, "a different source must progress while the first is held")
			close(release)
			require.NoError(t, <-done)
			err = second.run(context.Background(), "same-source", func(context.Context) error { return nil })
			require.NoError(t, err)
			assert.Empty(t, first.local, "idle sources and canceled waiters must not leak local entries")
		})
	}
}

func TestDataSourceSyncLockReleasesAfterErrorAndPanic(t *testing.T) {
	for _, backend := range []string{"lite", "redis"} {
		t.Run(backend, func(t *testing.T) {
			c := &dataSourceSyncCoordinator{}
			if backend == "redis" {
				c.redis, _ = syncLockRedisClient(t)
			}
			failure := errors.New("connector failed")
			err := c.run(context.Background(), "source", func(context.Context) error { return failure })
			require.ErrorIs(t, err, failure)
			require.Panics(t, func() {
				_ = c.run(context.Background(), "source", func(context.Context) error { panic("connector panic") })
			})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			require.NoError(t, c.run(ctx, "source", func(context.Context) error { return nil }))
			assert.Empty(t, c.local)
		})
	}
}

func TestDataSourceSyncRedisAbandonedLeaseExpires(t *testing.T) {
	client, server := syncLockRedisClient(t)
	key := dataSourceSyncLockKey("source")
	require.NoError(t, client.Set(context.Background(), key, "crashed-worker", dataSourceSyncLockLease).Err())
	server.FastForward(dataSourceSyncLockLease + time.Second)
	c := &dataSourceSyncCoordinator{redis: client}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, c.run(ctx, "source", func(context.Context) error { return nil }))
	assert.False(t, server.Exists(key))
}

func TestProcessSyncRedisOwnershipLossRejectsLateFetch(t *testing.T) {
	svc, connector, repo := newConcurrentSyncService(t)
	client, server := syncLockRedisClient(t)
	svc.syncCoordinator.redis = client
	task := concurrentSyncTask(t, "first")
	done := make(chan error, 1)
	go func() { done <- svc.ProcessSync(context.Background(), task) }()
	observation := <-connector.entered
	key := dataSourceSyncLockKey(t.Name())
	require.NoError(t, server.Set(key, "replacement-owner"))
	// Observe ownership cancellation before letting an uncooperative fetch return.
	select {
	case <-observation.ctx.Done():
	case <-time.After(dataSourceSyncLockRenew + 5*time.Second):
		close(connector.release)
		t.Fatal("ownership loss did not cancel the fetch context")
	}
	close(connector.release)
	require.ErrorIs(t, <-done, redislock.ErrLockOwnershipLost)
	assert.Zero(t, repo.writes)
	assert.Equal(t, "replacement-owner", client.Get(context.Background(), key).Val(),
		"the old worker must not release another owner's lease")
}

func TestDataSourceSyncLockDoesNotAcknowledgeCanceledCallback(t *testing.T) {
	for _, backend := range []string{"lite", "redis"} {
		t.Run(backend, func(t *testing.T) {
			c := &dataSourceSyncCoordinator{}
			if backend == "redis" {
				c.redis, _ = syncLockRedisClient(t)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			err := c.run(ctx, "source", func(context.Context) error {
				cancel()
				return nil
			})
			assert.ErrorIs(t, err, context.Canceled)
		})
	}
}

func TestProcessSyncRedisReplicasSerializeCursorSnapshots(t *testing.T) {
	client, _ := syncLockRedisClient(t)
	testProcessSyncSerializesCursorSnapshots(t, func(svc *DataSourceService) *DataSourceService {
		svc.syncCoordinator.redis = client
		return &DataSourceService{
			dsRepo: svc.dsRepo, syncLogRepo: svc.syncLogRepo,
			connectorRegistry: svc.connectorRegistry, kbService: svc.kbService,
			tenantRepo: svc.tenantRepo, tagService: svc.tagService,
			syncCoordinator: dataSourceSyncCoordinator{redis: client},
		}
	})
}

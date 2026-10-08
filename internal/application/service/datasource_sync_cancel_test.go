package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type cancelingSyncStream struct {
	*concurrentSyncConnector
	fetchErr error
}

func (c *cancelingSyncStream) FetchStream(
	ctx context.Context, cfg *types.DataSourceConfig, cursor *types.SyncCursor, _ datasource.StreamHandler,
) (*types.SyncCursor, error) {
	_, next, _ := c.FetchIncremental(ctx, cfg, cursor)
	return next, c.fetchErr
}

func TestProcessSyncStreamingCancellationDoesNotPublishResult(t *testing.T) {
	for _, fetchErr := range []error{nil, errors.New("fetch interrupted")} {
		name := "late-success"
		if fetchErr != nil {
			name = "late-error"
		}
		t.Run(name, func(t *testing.T) {
			svc, connector, repo := newConcurrentSyncService(t)
			registry := datasource.NewConnectorRegistry()
			require.NoError(t, registry.Register(&cancelingSyncStream{connector, fetchErr}))
			svc.connectorRegistry = registry
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			task := concurrentSyncTask(t, "first")
			done := make(chan error, 1)
			go func() { done <- svc.ProcessSync(ctx, task) }()
			<-connector.entered
			cancel()
			close(connector.release)
			require.ErrorIs(t, <-done, context.Canceled)
			assert.Zero(t, repo.writes)
			log, err := svc.syncLogRepo.FindByID(context.Background(), "first")
			require.NoError(t, err)
			assert.Equal(t, types.SyncLogStatusFailed, log.Status)
			assert.NotNil(t, log.FinishedAt)
			// An explicit retry can still resume without a stale-running-log gate.
			registry = datasource.NewConnectorRegistry()
			require.NoError(t, registry.Register(connector))
			svc.connectorRegistry = registry
			require.NoError(t, svc.ProcessSync(context.Background(), task))
			assert.Equal(t, 1, repo.writes)
			log, err = svc.syncLogRepo.FindByID(context.Background(), "first")
			require.NoError(t, err)
			assert.Equal(t, types.SyncLogStatusSuccess, log.Status)
		})
	}
}

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type pauseDuringSyncConnector struct {
	*ingestAckConnector
	beforeFetch func()
	fetchErr    error
}

func (c *pauseDuringSyncConnector) FetchIncremental(
	ctx context.Context, cfg *types.DataSourceConfig, cursor *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	c.beforeFetch()
	if c.fetchErr != nil {
		return nil, nil, c.fetchErr
	}
	return c.ingestAckConnector.FetchIncremental(ctx, cfg, cursor)
}

type pauseDuringStreamConnector struct {
	*ingestAckStream
	beforeFetch func()
}

func (c *pauseDuringStreamConnector) FetchStream(
	ctx context.Context, cfg *types.DataSourceConfig, cursor *types.SyncCursor, h datasource.StreamHandler,
) (*types.SyncCursor, error) {
	c.beforeFetch()
	return c.ingestAckStream.FetchStream(ctx, cfg, cursor, h)
}

func TestProcessSyncPreservesPauseOrResumeDuringFetch(t *testing.T) {
	for _, mode := range []string{"batch", "stream", "batch-error"} {
		for _, action := range []string{"pause", "resume"} {
			t.Run(mode+"/"+action, func(t *testing.T) {
				ctx := t.Context()
				h := newIngestAckHarness(t, mode == "stream")
				db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
				require.NoError(t, err)
				sqlDB, err := db.DB()
				require.NoError(t, err)
				sqlDB.SetMaxOpenConns(1)
				t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
				require.NoError(t, db.AutoMigrate(&types.DataSource{}, &types.SyncLog{}))
				sources := repository.NewDataSourceRepository(db)
				logs := repository.NewSyncLogRepository(db)
				if action == "resume" {
					h.ds.Status = types.DataSourceStatusPaused
				}
				require.NoError(t, sources.Create(ctx, h.ds))
				require.NoError(t, logs.Create(ctx, h.syncLogRepo.logs[h.syncLogID]))
				h.svc.dsRepo, h.svc.syncLogRepo = sources, logs
				h.svc.scheduler = datasource.NewScheduler(sources, logs, nil)
				h.svc.connectorRegistry = datasource.NewConnectorRegistry()
				beforeFetch := func() {
					if action == "pause" {
						require.NoError(t, h.svc.PauseDataSource(ctx, h.ds.ID))
					} else {
						require.NoError(t, h.svc.ResumeDataSource(ctx, h.ds.ID))
					}
				}
				var fetchErr error
				if mode == "batch-error" {
					fetchErr = errors.New("fetch failed after status changed")
				}
				var connector datasource.Connector = &pauseDuringSyncConnector{h.connector, beforeFetch, fetchErr}
				if mode == "stream" {
					connector = &pauseDuringStreamConnector{&ingestAckStream{h.connector}, beforeFetch}
				}
				require.NoError(t, h.svc.connectorRegistry.Register(connector))
				err = h.svc.ProcessSync(ctx, h.task)
				if fetchErr != nil {
					require.ErrorIs(t, err, fetchErr)
				} else {
					require.NoError(t, err)
				}
				stored, err := sources.FindByID(ctx, h.ds.ID)
				require.NoError(t, err)
				want := types.DataSourceStatusPaused
				if action == "resume" {
					want = types.DataSourceStatusActive
				}
				require.Equal(t, want, stored.Status)
				cursor, err := stored.ParseSyncCursor()
				require.NoError(t, err)
				wantOffset, wantLogStatus := 3, types.SyncLogStatusSuccess
				if fetchErr != nil {
					wantOffset, wantLogStatus = 0, types.SyncLogStatusFailed
				}
				require.Equal(t, wantOffset, ingestAckOffset(cursor))
				log, err := logs.FindByID(ctx, h.syncLogID)
				require.NoError(t, err)
				require.Equal(t, wantLogStatus, log.Status)
			})
		}
	}
}

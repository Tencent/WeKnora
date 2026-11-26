package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestProcessSyncRetriesFinalPersistenceFailure(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, stage := range []string{"source", "log", "partial-log"} {
			t.Run(map[bool]string{false: "batch", true: "stream"}[streaming]+"/"+stage, func(t *testing.T) {
				ctx := context.Background()
				h := newIngestAckHarness(t, streaming)
				wantStatus := types.SyncLogStatusSuccess
				wantCreated := 3
				if stage == "partial-log" {
					h.sink.failure = ErrInvalidFileType
					wantStatus = types.SyncLogStatusPartial
					wantCreated = 2
				}
				logFailure := stage != "source"
				needsResync := streaming && stage == "partial-log"
				if needsResync {
					cfg := mustParseDataSourceConfig(t, h.ds)
					cfg.Settings = map[string]interface{}{feishuSettingResyncRequired: true}
					var err error
					h.ds.Config, err = cfg.ToJSON()
					require.NoError(t, err)
				}
				db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
				require.NoError(t, err)
				sqlDB, err := db.DB()
				require.NoError(t, err)
				sqlDB.SetMaxOpenConns(1)
				t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
				require.NoError(t, db.AutoMigrate(&types.DataSource{}, &types.SyncLog{}))
				sources := apprepo.NewDataSourceRepository(db)
				logs := apprepo.NewSyncLogRepository(db)
				require.NoError(t, sources.Create(ctx, h.ds))
				require.NoError(t, logs.Create(ctx, h.syncLogRepo.logs[h.syncLogID]))
				h.svc.dsRepo, h.svc.syncLogRepo = sources, logs
				table, column := h.ds.TableName(), "last_sync_at"
				if logFailure {
					table, column = (&types.SyncLog{}).TableName(), "finished_at"
				}
				require.NoError(t, db.Exec("CREATE TRIGGER reject_completion BEFORE UPDATE ON "+table+
					" WHEN NEW."+column+" IS NOT NULL BEGIN SELECT RAISE(ABORT, 'completion blocked'); END").Error)
				err = h.svc.ProcessSync(ctx, h.task)
				require.ErrorContains(t, err, "completion blocked",
					"a completed connector run is not an acknowledged task until its final state is persisted")
				require.Len(t, h.sink.stored, wantCreated)
				var completedAt *time.Time
				var completedResult *types.SyncResult
				if logFailure {
					stored, queryErr := sources.FindByID(ctx, h.ds.ID)
					require.NoError(t, queryErr)
					completedAt = stored.LastSyncAt
					require.NotNil(t, completedAt)
					cursor, queryErr := stored.ParseSyncCursor()
					require.NoError(t, queryErr)
					require.Equal(t, 3, ingestAckOffset(cursor))
					completedResult, queryErr = stored.ParseSyncResult()
					require.NoError(t, queryErr)
					require.NotNil(t, completedResult)
					require.Equal(t, wantCreated, completedResult.Created)
					if needsResync {
						require.True(t, resyncRequired(mustParseDataSourceConfig(t, stored)))
					}
					running, queryErr := logs.HasRunningSync(ctx, h.ds.ID)
					require.NoError(t, queryErr)
					require.True(t, running, "the database fault must leave a real running row")
				} else {
					stored, queryErr := sources.FindByID(ctx, h.ds.ID)
					require.NoError(t, queryErr)
					require.Nil(t, stored.LastSyncAt)
				}
				if stage == "partial-log" {
					require.ErrorContains(t, h.svc.ProcessSync(ctx, h.task), "completion blocked")
				}
				require.NoError(t, db.Exec("DROP TRIGGER reject_completion").Error)
				require.NoError(t, h.svc.ProcessSync(ctx, h.task))
				stored, err := sources.FindByID(ctx, h.ds.ID)
				require.NoError(t, err)
				require.NotNil(t, stored.LastSyncAt)
				cursor, err := stored.ParseSyncCursor()
				require.NoError(t, err)
				require.Equal(t, 3, ingestAckOffset(cursor))
				log, err := logs.FindByID(ctx, h.syncLogID)
				require.NoError(t, err)
				if logFailure {
					require.Equal(t, wantCreated, log.ItemsCreated, "retry must retain the original created count")
					require.Equal(t, completedResult.Total, log.ItemsTotal)
					require.Equal(t, completedResult.Failed, log.ItemsFailed)
					result, parseErr := log.ParseResult()
					require.NoError(t, parseErr)
					require.Equal(t, completedResult, result)
					require.Equal(t, completedAt, log.FinishedAt)
					require.Equal(t, completedAt, stored.LastSyncAt)
					if needsResync {
						require.False(t, resyncRequired(mustParseDataSourceConfig(t, stored)))
					}
					if stage == "partial-log" {
						require.Equal(t, "1 document(s) failed to sync", log.ErrorMessage)
					}
					require.NoError(t, h.svc.ProcessSync(ctx, h.task))
					require.Equal(t, []int{0}, h.connector.starts, "completion retries and redelivery must skip fetching")
					require.Len(t, h.sink.attempted, 3, "completion retries must skip ingestion")
				}
				require.Equal(t, wantStatus, log.Status)
				running, err := logs.HasRunningSync(ctx, h.ds.ID)
				require.NoError(t, err)
				require.False(t, running, "recovery must unblock subsequent scheduled runs")
				require.Len(t, h.sink.stored, wantCreated)
			})
		}
	}
}

type finalResyncFailureRepo struct {
	*resyncDSRepo
	failure error
}

func (r *finalResyncFailureRepo) UpdateSyncState(ctx context.Context, ds *types.DataSource) error {
	if ds.LastSyncAt != nil && r.failure != nil {
		return r.failure
	}
	return r.resyncDSRepo.UpdateSyncState(ctx, ds)
}

func TestProcessSyncFinalPersistenceFailureKeepsResyncMarker(t *testing.T) {
	connector := &resyncStreamConnector{}
	svc, ds, repo, log := newResyncHarness(t, map[string]interface{}{feishuSettingResyncRequired: true}, connector)
	failure := errors.New("final state unavailable")
	faults := &finalResyncFailureRepo{resyncDSRepo: repo, failure: failure}
	svc.dsRepo = faults
	payload, err := json.Marshal(types.DataSourceSyncPayload{
		DataSourceID: ds.ID, TenantID: ds.TenantID, SyncLogID: log.ID,
	})
	require.NoError(t, err)
	task := asynq.NewTask(types.TypeDataSourceSync, payload)
	require.ErrorIs(t, svc.ProcessSync(context.Background(), task), failure)
	require.Zero(t, repo.updates, "a failed final write must not retire the upgrade marker")
	require.True(t, resyncRequired(mustParseDataSourceConfig(t, ds)))
	faults.failure = nil
	require.NoError(t, svc.ProcessSync(context.Background(), task))
	require.Equal(t, 1, repo.updates)
	require.False(t, resyncRequired(mustParseDataSourceConfig(t, ds)))
}

func mustParseDataSourceConfig(t *testing.T, ds *types.DataSource) *types.DataSourceConfig {
	t.Helper()
	cfg, err := ds.ParseConfig()
	require.NoError(t, err)
	return cfg
}

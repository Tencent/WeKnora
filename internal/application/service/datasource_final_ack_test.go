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
	for _, tc := range []struct {
		name, stage                   string
		stream, partial, changeConfig bool
	}{
		{name: "batch/source", stage: "source"},
		{name: "batch/log", stage: "log"},
		{name: "stream/source", stage: "source", stream: true},
		{name: "stream/log", stage: "log", stream: true},
		{name: "stream/partial-log-with-new-settings", stage: "log", stream: true, partial: true, changeConfig: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			h := newIngestAckHarness(t, tc.stream)
			if tc.stream && tc.stage == "log" {
				cfg := mustParseDataSourceConfig(t, h.ds)
				cfg.Settings = map[string]any{feishuSettingResyncRequired: true}
				var err error
				h.ds.Config, err = cfg.ToJSON()
				require.NoError(t, err)
			}
			wantCreated, wantStatus := 3, types.SyncLogStatusSuccess
			if tc.partial {
				h.connector.items[1] = types.FetchedItem{
					ExternalID: "blocked", Metadata: map[string]string{"error": "fetch failed"},
				}
				wantCreated, wantStatus = 2, types.SyncLogStatusPartial
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
			if tc.stage == "log" {
				table, column = (&types.SyncLog{}).TableName(), "finished_at"
			}
			require.NoError(t, db.Exec("CREATE TRIGGER reject_completion BEFORE UPDATE ON "+table+
				" WHEN NEW."+column+" IS NOT NULL BEGIN SELECT RAISE(ABORT, 'completion blocked'); END").Error)
			err = h.svc.ProcessSync(ctx, h.task)
			require.ErrorContains(t, err, "completion blocked",
				"a completed connector run is not acknowledged until its final state is persisted")
			require.Len(t, h.sink.stored, wantCreated)
			stored, err := sources.FindByID(ctx, h.ds.ID)
			require.NoError(t, err)
			if tc.stage == "log" {
				running, err := logs.HasRunningSync(ctx, h.ds.ID)
				require.NoError(t, err)
				require.True(t, running, "the database fault must leave a real running row")
				require.NotNil(t, stored.LastSyncAt)
				if tc.stream {
					require.True(t, resyncRequired(mustParseDataSourceConfig(t, stored)))
				}
				require.ErrorContains(t, h.svc.ProcessSync(ctx, h.task), "completion blocked")
				require.Equal(t, []int{0}, h.connector.starts, "a second recovery failure must not fetch")
			} else {
				require.Nil(t, stored.LastSyncAt)
			}
			if tc.changeConfig {
				cfg := mustParseDataSourceConfig(t, stored)
				cfg.ResourceIDs = []string{"new-selection"}
				stored.Config, err = cfg.ToJSON()
				require.NoError(t, err)
				require.NoError(t, sources.Update(ctx, stored))
			}
			require.NoError(t, db.Exec("DROP TRIGGER reject_completion").Error)
			require.NoError(t, h.svc.ProcessSync(ctx, h.task))
			stored, err = sources.FindByID(ctx, h.ds.ID)
			require.NoError(t, err)
			require.NotNil(t, stored.LastSyncAt)
			cursor, err := stored.ParseSyncCursor()
			require.NoError(t, err)
			require.Equal(t, 3, ingestAckOffset(cursor))
			log, err := logs.FindByID(ctx, h.syncLogID)
			require.NoError(t, err)
			require.Equal(t, wantStatus, log.Status)
			if tc.stage == "log" {
				require.Equal(t, wantCreated, log.ItemsCreated, "retry must preserve the run summary")
				require.Equal(t, 3, log.ItemsTotal)
				require.Equal(t, []int{0}, h.connector.starts, "retry must not fetch again")
				require.NotContains(t, string(log.Result), "_pending_sync_log")
				require.Equal(t, tc.changeConfig, resyncRequired(mustParseDataSourceConfig(t, stored)))
				if tc.partial {
					require.Equal(t, 1, log.ItemsFailed)
					require.Contains(t, log.ErrorMessage, "1 document(s) failed")
					require.Contains(t, string(log.Result), "fetch failed")
				}
			}
			running, err := logs.HasRunningSync(ctx, h.ds.ID)
			require.NoError(t, err)
			require.False(t, running, "recovery must unblock subsequent scheduled runs")
			require.Len(t, h.sink.stored, wantCreated)
		})
	}
}

func TestProcessSyncOlderTerminalRoundDoesNotConsumeResyncMarker(t *testing.T) {
	connector := &resyncStreamConnector{}
	svc, ds, repo, log := newResyncHarness(t, map[string]any{feishuSettingResyncRequired: true}, connector)
	log.Status = types.SyncLogStatusSuccess
	log.FinishedAt = timePtr(log.StartedAt.Add(time.Second))
	ds.LastSyncAt = timePtr(log.FinishedAt.Add(time.Second))
	payload, err := json.Marshal(types.DataSourceSyncPayload{
		DataSourceID: ds.ID, TenantID: ds.TenantID, SyncLogID: log.ID,
	})
	require.NoError(t, err)
	require.NoError(t, svc.ProcessSync(context.Background(), asynq.NewTask(types.TypeDataSourceSync, payload)))
	require.Zero(t, repo.updates)
	require.Empty(t, connector.startCursors)
	require.True(t, resyncRequired(mustParseDataSourceConfig(t, ds)))
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

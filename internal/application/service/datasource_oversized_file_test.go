package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

type itemFailureConnector struct {
	deletedItemConnector
	items  []types.FetchedItem
	cursor *types.SyncCursor
	err    error
}

func (c itemFailureConnector) FetchAll(
	context.Context, *types.DataSourceConfig, []string,
) ([]types.FetchedItem, error) {
	return c.items, c.err
}

func (c itemFailureConnector) FetchIncremental(
	context.Context, *types.DataSourceConfig, *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	return c.items, c.cursor, c.err
}

type itemFailureStreamConnector struct{ itemFailureConnector }

func (c itemFailureStreamConnector) FetchStream(
	ctx context.Context, _ *types.DataSourceConfig, _ *types.SyncCursor, handler datasource.StreamHandler,
) (*types.SyncCursor, error) {
	for _, item := range c.items {
		if err := handler.Emit(ctx, item); err != nil {
			return nil, err
		}
		if err := handler.Checkpoint(ctx, c.cursor); err != nil {
			return nil, err
		}
	}
	return c.cursor, c.err
}

func TestProcessSyncOversizedFilesKeepSchedule(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, scenario := range []string{
			"mixed", "all-failed", "many-oversized", "transient-only",
			"mixed-failures", "ingest-failed", "paused", "fetch-failed",
			"mixed-permanent", "oversized-and-ingest-failed",
		} {
			name := scenario + "/batch"
			if stream {
				name = scenario + "/stream"
			}
			t.Run(name, func(t *testing.T) {
				h := newSyncDeletionHarness(t, false, "ds-item-failure", "log-item-failure",
					&deletionLookupKnowledgeRepo{}, nil)
				h.ds.SyncMode = types.SyncModeIncremental
				oldCursor := &types.SyncCursor{ConnectorCursor: map[string]interface{}{"revision": "old"}}
				h.ds.LastSyncCursor, _ = oldCursor.ToJSON()
				c := itemFailureConnector{
					items: []types.FetchedItem{{Title: "large.pptx", Metadata: map[string]string{
						"error":             "document exceeds the download limit",
						"error_reason_code": "dingtalk_document_too_large",
						"error_reason":      "DingTalk file exceeds the 64 MiB download limit",
					}}},
					cursor: &types.SyncCursor{ConnectorCursor: map[string]interface{}{
						"revision": "healthy-advanced-failed-retained",
					}},
				}
				healthy := types.FetchedItem{
					ExternalID: "healthy", Title: "healthy", FileName: "healthy.md", Content: []byte("ok"),
				}
				switch scenario {
				case "mixed":
					c.items = append(c.items, healthy)
				case "many-oversized", "mixed-failures":
					for i := 0; i < maxSyncResultErrors; i++ {
						c.items = append(c.items, c.items[0])
					}
					if scenario == "mixed-failures" {
						c.items = append(c.items, types.FetchedItem{Title: "temporary", Metadata: map[string]string{
							"error": "temporary API error", "error_reason_code": "dingtalk_document_failed",
						}})
					}
				case "transient-only":
					c.items[0].Metadata = map[string]string{
						"error": "temporary API error", "error_reason_code": "dingtalk_document_failed",
					}
				case "mixed-permanent":
					healthy.FileName = "unsupported.mp4"
					c.items = append(c.items, healthy)
				case "oversized-and-ingest-failed":
					c.items = append(c.items, healthy)
					h.knowledgeSvc.createErr = errors.New("storage unavailable")
				case "ingest-failed":
					c.items = []types.FetchedItem{healthy, healthy}
					h.knowledgeSvc.createErr = errors.New("storage unavailable")
				case "paused":
					h.ds.Status = types.DataSourceStatusPaused
				case "fetch-failed":
					c.items = nil
					c.err = errors.New("source unavailable")
				}
				registry := datasource.NewConnectorRegistry()
				var connector datasource.Connector = c
				if stream {
					connector = itemFailureStreamConnector{c}
				}
				require.NoError(t, registry.Register(connector))
				h.svc.connectorRegistry = registry
				payload, err := json.Marshal(types.DataSourceSyncPayload{
					DataSourceID: h.ds.ID, TenantID: h.ds.TenantID, SyncLogID: h.syncLogID,
				})
				require.NoError(t, err)
				err = h.svc.ProcessSync(context.Background(), asynq.NewTask(types.TypeDataSourceSync, payload))
				log := h.syncLogRepo.logs[h.syncLogID]
				if scenario == "fetch-failed" || scenario == "transient-only" ||
					scenario == "mixed-failures" || scenario == "ingest-failed" ||
					scenario == "oversized-and-ingest-failed" {
					require.Error(t, err)
					require.Equal(t, types.SyncLogStatusFailed, log.Status)
					require.Equal(t, types.DataSourceStatusError, h.ds.Status)
					if !stream && scenario == "oversized-and-ingest-failed" {
						require.JSONEq(t, string(mustCursorJSON(t, oldCursor)), string(h.ds.LastSyncCursor))
					}
					return
				}
				require.NoError(t, err, "oversized files must not replay the whole task")
				require.Equal(t, types.SyncLogStatusPartial, log.Status)
				wantState := types.DataSourceStatusActive
				if scenario == "paused" {
					wantState = types.DataSourceStatusPaused
				}
				require.Equal(t, wantState, h.ds.Status)
				require.Positive(t, log.ItemsFailed)
				result, err := log.ParseResult()
				require.NoError(t, err)
				require.NotEmpty(t, result.Errors)
				require.Equal(t, "dingtalk_document_too_large", result.Errors[0].Code)
				require.Contains(t, result.Errors[0].Message, "64 MiB")
				require.JSONEq(t, string(mustCursorJSON(t, c.cursor)), string(h.ds.LastSyncCursor))
				if scenario == "many-oversized" {
					require.Equal(t, maxSyncResultErrors+1, result.Failed)
					require.Len(t, result.Errors, maxSyncResultErrors)
				}

				if scenario == "mixed" {
					require.Equal(t, 1, log.ItemsCreated)
					require.Equal(t, []string{"create:healthy.md"}, h.knowledgeSvc.events)
				}
			})
		}
	}
}

func mustCursorJSON(t *testing.T, cursor *types.SyncCursor) types.JSON {
	t.Helper()
	data, err := cursor.ToJSON()
	require.NoError(t, err)
	return data
}

type syncScheduleRecorder struct{ tasks chan *asynq.Task }

func (r syncScheduleRecorder) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	r.tasks <- task
	return &asynq.TaskInfo{ID: "scheduled-after-file-failure"}, nil
}

func TestSchedulerRestartsAfterOversizedFiles(t *testing.T) {
	for _, stream := range []bool{false, true} {
		name := "batch"
		if stream {
			name = "stream"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			f := newSQLiteDataSourceDeleteFixture(t)
			config, err := (&types.DataSourceConfig{Type: deletedItemConnectorType}).ToJSON()
			require.NoError(t, err)
			f.ds.Type = deletedItemConnectorType
			f.ds.Config = config
			f.ds.SyncMode = types.SyncModeIncremental
			f.ds.SyncSchedule = "@every 1s"
			require.NoError(t, f.dsRepo.Update(ctx, f.ds))
			h := newSyncDeletionHarness(t, false, f.ds.ID, f.runningLog.ID, nil, nil)
			h.svc.dsRepo = f.dsRepo
			h.svc.syncLogRepo = f.syncLogRepo
			h.svc.kbService = &processSyncKBService{kb: &types.KnowledgeBase{
				ID: f.ds.KnowledgeBaseID, TenantID: f.ds.TenantID,
			}}
			c := itemFailureConnector{items: []types.FetchedItem{{Title: "large.pptx", Metadata: map[string]string{
				"error": "document exceeds the download limit", "error_reason_code": "dingtalk_document_too_large",
			}}}}
			var connector datasource.Connector = c
			if stream {
				connector = itemFailureStreamConnector{c}
			}
			registry := datasource.NewConnectorRegistry()
			require.NoError(t, registry.Register(connector))
			h.svc.connectorRegistry = registry
			payload, err := json.Marshal(types.DataSourceSyncPayload{
				DataSourceID: f.ds.ID, TenantID: f.ds.TenantID, SyncLogID: f.runningLog.ID,
			})
			require.NoError(t, err)
			require.NoError(t, h.svc.ProcessSync(ctx, asynq.NewTask(types.TypeDataSourceSync, payload)))
			found, err := f.dsRepo.FindByID(ctx, f.ds.ID)
			require.NoError(t, err)
			require.Equal(t, types.DataSourceStatusActive, found.Status)
			log, err := f.syncLogRepo.FindByID(ctx, f.runningLog.ID)
			require.NoError(t, err)
			require.Equal(t, types.SyncLogStatusPartial, log.Status)
			require.Equal(t, 1, log.ItemsFailed)
			recorder := syncScheduleRecorder{tasks: make(chan *asynq.Task, 2)}
			restarted := datasource.NewScheduler(f.dsRepo, f.syncLogRepo, recorder)
			require.NoError(t, restarted.Start(ctx))
			defer restarted.Stop()
			require.Equal(t, 1, restarted.EntryCount())
			select {
			case task := <-recorder.tasks:
				var scheduled types.DataSourceSyncPayload
				require.NoError(t, json.Unmarshal(task.Payload(), &scheduled))
				require.Equal(t, f.ds.ID, scheduled.DataSourceID)
				require.Equal(t, "schedule", scheduled.Trigger)
			case <-time.After(5 * time.Second):
				t.Fatal("next scheduled sync was not enqueued after an oversized file")
			}
		})
	}
}

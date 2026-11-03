package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An asynq retry or redelivery must not replay a round that already reached
// success, partial, or canceled; failed and running rounds must still run.
func TestProcessSyncReplayGuardBySyncLogStatus(t *testing.T) {
	cases := []struct {
		status  string
		replays bool
	}{
		{types.SyncLogStatusSuccess, false},
		{types.SyncLogStatusPartial, false},
		{types.SyncLogStatusCanceled, false},
		{types.SyncLogStatusFailed, true},
		{types.SyncLogStatusRunning, true},
	}
	for _, streaming := range []bool{false, true} {
		for _, tc := range cases {
			name := tc.status + "/batch"
			if streaming {
				name = tc.status + "/streaming"
			}
			t.Run(name, func(t *testing.T) {
				h := newIngestAckHarness(t, streaming)
				log := h.syncLogRepo.logs[h.syncLogID]
				log.Status = tc.status

				require.NoError(t, h.svc.ProcessSync(context.Background(), h.task))

				if tc.replays {
					assert.Equal(t, []int{0}, h.connector.starts)
					assert.Len(t, h.sink.attempted, 3)
					assert.Equal(t, types.SyncLogStatusSuccess, log.Status)
					return
				}
				assert.Empty(t, h.connector.starts, "connector must not be called")
				assert.Empty(t, h.sink.attempted, "no item may be re-ingested")
				assert.Equal(t, tc.status, log.Status, "terminal status must be left untouched")
				cursor, err := h.ds.ParseSyncCursor()
				require.NoError(t, err)
				assert.Zero(t, ingestAckOffset(cursor), "cursor must not advance")
			})
		}
	}
}

// When the data source was deleted before a redelivery of an already finished
// round arrives, the finished sync log must be left untouched. Only a round
// that had not finished is still flipped to canceled.
func TestProcessSyncDeletedDataSourceKeepsFinishedSyncLog(t *testing.T) {
	cases := []struct {
		status    string
		cancelled bool
	}{
		{types.SyncLogStatusSuccess, false},
		{types.SyncLogStatusPartial, false},
		{types.SyncLogStatusCanceled, false},
		{types.SyncLogStatusRunning, true},
	}
	for _, tc := range cases {
		t.Run(tc.status, func(t *testing.T) {
			finishedAt := time.Now().UTC().Add(-time.Hour)
			syncLog := &types.SyncLog{
				ID:           "log-deleted-ds",
				DataSourceID: "ds-deleted",
				TenantID:     1,
				Status:       tc.status,
				FinishedAt:   &finishedAt,
				ErrorMessage: "original message",
			}
			syncLogRepo := &processSyncSyncLogRepo{logs: map[string]*types.SyncLog{syncLog.ID: syncLog}}
			svc := &DataSourceService{
				dsRepo:      newKBDeleteDSRepo("kb-1"),
				syncLogRepo: syncLogRepo,
			}
			payload, err := json.Marshal(types.DataSourceSyncPayload{
				DataSourceID: syncLog.DataSourceID,
				TenantID:     syncLog.TenantID,
				SyncLogID:    syncLog.ID,
			})
			require.NoError(t, err)

			require.NoError(t, svc.ProcessSync(context.Background(), asynq.NewTask(types.TypeDataSourceSync, payload)))

			got := syncLogRepo.logs[syncLog.ID]
			if tc.cancelled {
				assert.Equal(t, types.SyncLogStatusCanceled, got.Status)
				assert.Equal(t, "data source has been deleted", got.ErrorMessage)
				return
			}
			assert.Equal(t, tc.status, got.Status, "finished status must not be rewritten")
			assert.Equal(t, "original message", got.ErrorMessage)
			assert.Equal(t, &finishedAt, got.FinishedAt)
		})
	}
}

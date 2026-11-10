package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
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

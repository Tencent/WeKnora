package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcessSyncPermanentRejectionsAdvanceCursor(t *testing.T) {
	cases := []struct {
		streaming, onlyRejected bool
		extension               string
	}{
		{false, false, "mp4"},
		{false, true, "mp4"},
		{true, false, "mp4"},
		{true, true, "mp4"},
		{false, false, "unsupported"},
		{false, true, "unsupported"},
		{true, false, "unsupported"},
		{true, true, "unsupported"},
	}
	for _, tc := range cases {
		name := fmt.Sprintf("stream=%v/onlyRejected=%v/%s", tc.streaming, tc.onlyRejected, tc.extension)
		t.Run(name, func(t *testing.T) {
			h := newIngestAckHarness(t, tc.streaming)
			h.connector.items[1].FileName = "blocked." + tc.extension
			if tc.onlyRejected {
				h.connector.items = h.connector.items[1:2]
			}
			require.NoError(t, h.svc.ProcessSync(context.Background(), h.task))
			log := h.syncLogRepo.logs[h.syncLogID]
			assert.Equal(t, types.SyncLogStatusPartial, log.Status)
			assert.Equal(t, 1, log.ItemsFailed)
			assert.Equal(t, len(h.connector.items)-1, log.ItemsCreated)
			assert.Contains(t, string(log.Result), "unsupported_file_type")
			assert.Equal(t, types.DataSourceStatusActive, h.ds.Status)
			assert.False(t, h.sink.stored["blocked"])
			cursor, err := h.ds.ParseSyncCursor()
			require.NoError(t, err)
			assert.Equal(t, len(h.connector.items), ingestAckOffset(cursor))
			require.NoError(t, h.svc.ProcessSync(context.Background(), h.task))
			assert.Zero(t, h.syncLogRepo.logs[h.syncLogID].ItemsTotal)
		})
	}
}

func TestProcessSyncRecoverableRejectionsRetainCursor(t *testing.T) {
	failures := []error{
		werrors.NewBadRequestError("storage configuration missing"),
		errors.New("storage quota exhausted"),
		errors.New("database unavailable"),
		errors.New("network timeout"),
	}
	for _, streaming := range []bool{false, true} {
		for _, failure := range failures {
			t.Run(fmt.Sprintf("stream=%v/%s", streaming, failure), func(t *testing.T) {
				h := newIngestAckHarness(t, streaming)
				h.connector.items = h.connector.items[1:2]
				h.sink.failure = failure
				require.ErrorIs(t, h.svc.ProcessSync(context.Background(), h.task), failure)
				cursor, err := h.ds.ParseSyncCursor()
				require.NoError(t, err)
				assert.Zero(t, ingestAckOffset(cursor))
				h.sink.failure = nil
				require.NoError(t, h.svc.ProcessSync(context.Background(), h.task))
				assert.True(t, h.sink.stored["blocked"])
			})
		}
	}
}

func TestProcessSyncWrappedUnsupportedRejection(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		h := newIngestAckHarness(t, streaming)
		h.connector.items = h.connector.items[1:2]
		h.sink.failure = fmt.Errorf("import rejected: %w", ErrInvalidFileType)
		require.NoError(t, h.svc.ProcessSync(context.Background(), h.task))
		assert.Equal(t, types.SyncLogStatusPartial, h.syncLogRepo.logs[h.syncLogID].Status)
		cursor, err := h.ds.ParseSyncCursor()
		require.NoError(t, err)
		assert.Equal(t, 1, ingestAckOffset(cursor))
	}
}

func TestProcessSyncPermanentRejectionsDoNotHideFetchFailures(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		h := newIngestAckHarness(t, streaming)
		h.connector.items = []types.FetchedItem{
			{ExternalID: "unsupported", FileName: "video.mp4", Content: []byte("video")},
			{ExternalID: "failed", Metadata: map[string]string{"error": "fetch failed"}},
		}
		require.ErrorContains(t, h.svc.ProcessSync(context.Background(), h.task), "all fetched items failed")
		assert.Equal(t, types.SyncLogStatusFailed, h.syncLogRepo.logs[h.syncLogID].Status)
	}
}

func TestProcessSyncPermanentRejectionsBeyondErrorSampleLimit(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		h := newIngestAckHarness(t, streaming)
		h.connector.items = make([]types.FetchedItem, maxSyncResultErrors+1)
		for i := range h.connector.items {
			h.connector.items[i] = types.FetchedItem{
				ExternalID: fmt.Sprint(i), FileName: "video.mp4", Content: []byte("video"),
			}
		}
		require.NoError(t, h.svc.ProcessSync(context.Background(), h.task))
		assert.Equal(t, len(h.connector.items), h.syncLogRepo.logs[h.syncLogID].ItemsFailed)
		cursor, err := h.ds.ParseSyncCursor()
		require.NoError(t, err)
		assert.Equal(t, len(h.connector.items), ingestAckOffset(cursor))
	}
}

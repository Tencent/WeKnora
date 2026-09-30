package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcessSyncIngestFailureRetainsRetryCursor(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		name := "batch"
		if streaming {
			name = "streaming"
		}
		t.Run(name, func(t *testing.T) {
			h := newIngestAckHarness(t, streaming)
			failure := errors.New("private storage diagnostic")
			h.sink.failure = failure
			err := h.svc.ProcessSync(context.Background(), h.task)
			require.ErrorIs(t, err, failure)
			log := h.syncLogRepo.logs[h.syncLogID]
			assert.Equal(t, types.SyncLogStatusFailed, log.Status)
			assert.Equal(t, 1, log.ItemsFailed)
			assert.NotContains(t, log.ErrorMessage, failure.Error())
			cursor, parseErr := h.ds.ParseSyncCursor()
			require.NoError(t, parseErr)
			wantOffset := 0
			if streaming {
				wantOffset = 1
			}
			assert.Equal(t, wantOffset, ingestAckOffset(cursor))
			assert.True(t, h.sink.stored["first"])
			assert.False(t, h.sink.stored["blocked"])
			assert.Equal(t, !streaming, h.sink.stored["last"])

			h.sink.failure = nil
			require.NoError(t, h.svc.ProcessSync(context.Background(), h.task))
			assert.Equal(t, []int{0, wantOffset}, h.connector.starts)
			assert.Len(t, h.sink.stored, 3)
			assert.Equal(t, types.SyncLogStatusSuccess, h.syncLogRepo.logs[h.syncLogID].Status)
			cursor, parseErr = h.ds.ParseSyncCursor()
			require.NoError(t, parseErr)
			assert.Equal(t, 3, ingestAckOffset(cursor))
			require.NoError(t, h.svc.ProcessSync(context.Background(), h.task))
			assert.Zero(t, h.syncLogRepo.logs[h.syncLogID].ItemsTotal)
		})
	}
}

func TestStreamIngestFailureCannotBeCheckpointed(t *testing.T) {
	h := newIngestAckHarness(t, true)
	failure := errors.New("storage unavailable")
	h.sink.failure = failure
	handler := newStreamHandler(h.svc, h.ds, &types.SyncResult{}, h.syncLogRepo.logs[h.syncLogID])
	require.NoError(t, handler.Emit(context.Background(), h.connector.items[0]))
	require.NoError(t, handler.Checkpoint(context.Background(), ingestAckCursor(1)))
	require.ErrorIs(t, handler.Emit(context.Background(), h.connector.items[1]), failure)
	require.ErrorIs(t, handler.Checkpoint(context.Background(), ingestAckCursor(2)), failure)
	require.ErrorIs(t, handler.Emit(context.Background(), h.connector.items[2]), failure)
	cursor, err := h.ds.ParseSyncCursor()
	require.NoError(t, err)
	assert.Equal(t, 1, ingestAckOffset(cursor))
	assert.Equal(t, []string{"first", "blocked"}, h.sink.attempted)
}

func TestStreamIngestAcknowledgementPreservesSoftOutcomes(t *testing.T) {
	tests := []struct {
		name            string
		item            types.FetchedItem
		createErr       error
		skipped, failed int
	}{
		{
			name:   "fetch failure",
			item:   types.FetchedItem{Metadata: map[string]string{"error": "fetch unavailable"}},
			failed: 1,
		},
		{name: "empty", item: types.FetchedItem{}, skipped: 1},
		{
			name:      "duplicate",
			item:      types.FetchedItem{Content: []byte("doc"), FileName: "doc.md"},
			createErr: &types.DuplicateKnowledgeError{Message: "duplicate"},
			skipped:   1,
		},
		{
			name: "embedded image",
			item: types.FetchedItem{
				Content: []byte("image"), FileName: "image.png",
				Metadata: map[string]string{"embedded_image": "true"},
			},
			createErr: errors.New("no VLM"),
			skipped:   1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newIngestAckHarness(t, true)
			h.svc.knowledgeService = &sweepFakeKS{repo: &deletionLookupKnowledgeRepo{}, createErr: tt.createErr}
			result := &types.SyncResult{}
			handler := newStreamHandler(h.svc, h.ds, result, h.syncLogRepo.logs[h.syncLogID])
			require.NoError(t, handler.Emit(context.Background(), tt.item))
			require.NoError(t, handler.Checkpoint(context.Background(), ingestAckCursor(1)))
			assert.Equal(t, tt.skipped, result.Skipped)
			assert.Equal(t, tt.failed, result.Failed)
		})
	}
}

func TestProcessSyncIngestErrorCannotBeIgnoredByConnector(t *testing.T) {
	h := newIngestAckHarness(t, true)
	failure := errors.New("private storage diagnostic")
	h.sink.failure = failure
	require.NoError(t, h.svc.connectorRegistry.Register(&ignoringIngestAckStream{h.connector}))
	require.ErrorIs(t, h.svc.ProcessSync(context.Background(), h.task), failure)
	cursor, err := h.ds.ParseSyncCursor()
	require.NoError(t, err)
	assert.Equal(t, 1, ingestAckOffset(cursor))
	assert.Equal(t, []string{"first", "blocked"}, h.sink.attempted)
	log := h.syncLogRepo.logs[h.syncLogID]
	assert.Equal(t, types.SyncLogStatusFailed, log.Status)
	assert.NotContains(t, log.ErrorMessage, failure.Error())
}

func TestProcessSyncIngestAcknowledgementKeepsFetchFailuresPartial(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		h := newIngestAckHarness(t, streaming)
		h.connector.items[1] = types.FetchedItem{
			ExternalID: "blocked", Metadata: map[string]string{"error": "fetch failed"},
		}
		require.NoError(t, h.svc.ProcessSync(context.Background(), h.task))
		log := h.syncLogRepo.logs[h.syncLogID]
		assert.Equal(t, types.SyncLogStatusPartial, log.Status)
		assert.Equal(t, 2, log.ItemsCreated)
		assert.Equal(t, 1, log.ItemsFailed)
		cursor, err := h.ds.ParseSyncCursor()
		require.NoError(t, err)
		assert.Equal(t, 3, ingestAckOffset(cursor))
	}
}

func TestStreamIngestFailureIndependentOfErrorSampleLimit(t *testing.T) {
	h := newIngestAckHarness(t, true)
	failure := errors.New("storage unavailable")
	h.sink.failure = failure
	result := &types.SyncResult{Errors: make([]types.SyncItemError, maxSyncResultErrors)}
	handler := newStreamHandler(h.svc, h.ds, result, h.syncLogRepo.logs[h.syncLogID])
	require.ErrorIs(t, handler.Emit(context.Background(), h.connector.items[1]), failure)
	require.ErrorIs(t, handler.Checkpoint(context.Background(), ingestAckCursor(2)), failure)
	assert.Len(t, result.Errors, maxSyncResultErrors)
	assert.Equal(t, 1, result.Failed)
}

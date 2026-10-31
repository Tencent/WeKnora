package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestStreamRetryableFailureCannotAdvanceCursor(t *testing.T) {
	h := newIngestAckHarness(t, true)
	handler := newStreamHandler(h.svc, h.ds, &types.SyncResult{}, h.syncLogRepo.logs[h.syncLogID])
	item := types.FetchedItem{}
	raw := []byte(`{"retry_on_failure":true,"metadata":{"error":"download unavailable"}}`)
	require.NoError(t, json.Unmarshal(raw, &item))
	require.Error(t, handler.Emit(context.Background(), item))
	require.Error(t, handler.Checkpoint(context.Background(), ingestAckCursor(2)))
}

func TestStreamRetryableDeletionFailureCanRetry(t *testing.T) {
	for _, stage := range []string{"lookup", "delete", "hard delete"} {
		t.Run(stage, func(t *testing.T) {
			repo := &deletionLookupKnowledgeRepo{knowledge: &types.Knowledge{ID: "attachment"}}
			ks := &sweepFakeKS{repo: repo}
			failure := errors.New("unavailable")
			switch stage {
			case "lookup":
				repo.lookupErr = failure
			case "delete":
				ks.deleteErr = failure
			case "hard delete":
				repo.hardDeleteErr = failure
			}
			h := newSyncDeletionHarness(t, true, "ds-retry", "log-retry", repo, ks)
			result := &types.SyncResult{}
			handler := newStreamHandler(h.svc, h.ds, result, h.syncLogRepo.logs[h.syncLogID])
			item := types.FetchedItem{ExternalID: "p1#file#a1", IsDeleted: true, RetryOnFailure: true}
			require.Error(t, handler.Emit(context.Background(), item))
			require.Equal(t, 1, result.DeletionFailed)
			require.Error(t, handler.Checkpoint(context.Background(), ingestAckCursor(1)))
			require.Empty(t, h.ds.LastSyncCursor)
			repo.lookupErr = nil
			ks.deleteErr = nil
			repo.hardDeleteErr = nil
			retry := newStreamHandler(h.svc, h.ds, &types.SyncResult{}, h.syncLogRepo.logs[h.syncLogID])
			require.NoError(t, retry.Emit(context.Background(), item))
			require.NoError(t, retry.Checkpoint(context.Background(), ingestAckCursor(1)))
			require.Contains(t, repo.hardDeleted, "attachment")
		})
	}
}

func TestProcessSyncRetryableFetchFailureResumesLastAcceptedItem(t *testing.T) {
	h := newIngestAckHarness(t, true)
	h.connector.items[1] = types.FetchedItem{
		ExternalID: "blocked", RetryOnFailure: true,
		Metadata: map[string]string{"error": "attachment unavailable"},
	}
	require.Error(t, h.svc.ProcessSync(context.Background(), h.task))
	cur, err := h.ds.ParseSyncCursor()
	require.NoError(t, err)
	require.Equal(t, 1, ingestAckOffset(cur))
	h.connector.items[1] = types.FetchedItem{
		ExternalID: "blocked", RetryOnFailure: true, Content: []byte("pdf"), FileName: "checklist.pdf",
	}
	require.NoError(t, h.svc.ProcessSync(context.Background(), h.task))
	cur, err = h.ds.ParseSyncCursor()
	require.NoError(t, err)
	require.Equal(t, 3, ingestAckOffset(cur))
	require.Len(t, h.sink.stored, 3)
	require.Equal(t, []int{0, 1}, h.connector.starts)
}

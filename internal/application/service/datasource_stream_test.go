package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingDSRepo captures UpdateSyncState calls so checkpoint persistence can
// be asserted without a database.
type recordingDSRepo struct {
	kbDeleteDSRepo
	updated []*types.DataSource
}

func (r *recordingDSRepo) UpdateSyncState(_ context.Context, ds *types.DataSource) error {
	// Snapshot the fields a checkpoint is expected to persist.
	cp := *ds
	r.updated = append(r.updated, &cp)
	return nil
}

func newStreamHandler(svc *DataSourceService, ds *types.DataSource, result *types.SyncResult, syncLog *types.SyncLog) *streamSyncHandler {
	return &streamSyncHandler{svc: svc, ds: ds, result: result, syncLog: syncLog}
}

// Emit routes items through the same classification as the batch loop: deleted
// items count into result.Deleted (the actual deletion, scoping and failure
// counters are covered by the ProcessSync tests) and connector-reported
// failures (an item carrying only a Metadata["error"]) land in result.Failed
// with a message — never silently lost.
func TestStreamHandler_EmitClassifiesDeletedAndFailed(t *testing.T) {
	ds := &types.DataSource{
		ID: "ds-1", TenantID: 1, KnowledgeBaseID: "kb-1",
		Type: types.ConnectorTypeFeishu, SyncDeletions: true,
	}
	result := &types.SyncResult{}
	knowledgeRepo := &deletionLookupKnowledgeRepo{knowledge: &types.Knowledge{ID: "knowledge-gone"}}
	knowledgeSvc := &sweepFakeKS{repo: knowledgeRepo}
	h := newStreamHandler(&DataSourceService{knowledgeService: knowledgeSvc}, ds, result, &types.SyncLog{})

	require.NoError(t, h.Emit(context.Background(), types.FetchedItem{ExternalID: "gone", IsDeleted: true}))
	require.NoError(t, h.Emit(context.Background(), types.FetchedItem{
		ExternalID: "bad", Title: "Broken Doc",
		Metadata: map[string]string{"error": "export failed"},
	}))

	assert.Equal(t, 1, result.Deleted)
	assert.Equal(t, 1, result.Failed)
	require.Len(t, result.Errors, 1)
	assert.Equal(t, "Broken Doc", result.Errors[0].Title)
	assert.Contains(t, result.Errors[0].Message, "export failed")
}

// A canceled context aborts the stream: Emit returns the context error so the
// connector stops fetching instead of burning API budget on a doomed run.
func TestStreamHandler_EmitAbortsOnCanceledContext(t *testing.T) {
	h := newStreamHandler(&DataSourceService{}, &types.DataSource{}, &types.SyncResult{}, &types.SyncLog{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := h.Emit(ctx, types.FetchedItem{ExternalID: "x", Content: []byte("data"), FileName: "x.md"})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

// Checkpoint persists the connector cursor onto the data source so a crash
// after it keeps the progress made so far.
func TestStreamHandler_CheckpointPersistsCursor(t *testing.T) {
	dsRepo := &recordingDSRepo{}
	svc := &DataSourceService{dsRepo: dsRepo, syncLogRepo: &processSyncSyncLogRepo{logs: map[string]*types.SyncLog{}}}
	ds := &types.DataSource{ID: "ds-1"}
	result := &types.SyncResult{Created: 3}
	syncLog := &types.SyncLog{ID: "log-1"}
	h := newStreamHandler(svc, ds, result, syncLog)

	cursor := &types.SyncCursor{ConnectorCursor: map[string]interface{}{
		"space_node_times": map[string]map[string]string{"space1": {"nt1": "100"}},
	}}
	require.NoError(t, h.Checkpoint(context.Background(), cursor))

	require.Len(t, dsRepo.updated, 1)
	assert.NotEmpty(t, dsRepo.updated[0].LastSyncCursor, "checkpoint must persist the cursor JSON")
}

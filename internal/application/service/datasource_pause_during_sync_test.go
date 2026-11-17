package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regression for #5430: a user-controlled pause or resume that happens while a
// sync is in flight must survive the worker's completion. updateSyncRunResult
// used to pick the status from the stale wasPaused snapshot captured at sync
// start, so a pause applied mid-sync was overwritten back to Active (or Error),
// and a resume applied mid-sync was reverted to Paused.
func newPauseSyncHarness(t *testing.T, ds *types.DataSource) (*DataSourceService, *types.SyncLog) {
	t.Helper()
	syncLog := &types.SyncLog{
		ID:           "log-" + ds.ID,
		DataSourceID: ds.ID,
		TenantID:     ds.TenantID,
	}
	svc := &DataSourceService{
		dsRepo:      newKBDeleteDSRepo(ds.KnowledgeBaseID, ds),
		syncLogRepo: &processSyncSyncLogRepo{logs: map[string]*types.SyncLog{syncLog.ID: syncLog}},
	}
	return svc, syncLog
}

// A pause applied while the sync ran must not be cleared by a successful run.
func TestUpdateSyncRunResult_PreservesConcurrentPauseOnSuccess(t *testing.T) {
	ds := &types.DataSource{
		ID:              "ds-pause-ok",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		Status:          types.DataSourceStatusPaused,
	}
	svc, syncLog := newPauseSyncHarness(t, ds)

	// wasPaused=false is the stale snapshot (sync started while active), but the
	// data source is paused now. Completion must not clear the pause.
	err := svc.updateSyncRunResult(
		context.Background(), ds, syncLog,
		&types.SyncResult{}, nil, types.SyncLogStatusSuccess, "", false,
	)
	require.NoError(t, err)

	assert.Equal(t, types.DataSourceStatusPaused, ds.Status,
		"a pause applied while the sync ran must not be overwritten by a successful worker")
}

// A pause applied while the sync ran must not be flipped to Error on failure.
func TestUpdateSyncRunResult_PreservesConcurrentPauseOnFailure(t *testing.T) {
	ds := &types.DataSource{
		ID:              "ds-pause-fail",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		Status:          types.DataSourceStatusPaused,
	}
	svc, syncLog := newPauseSyncHarness(t, ds)

	err := svc.updateSyncRunResult(
		context.Background(), ds, syncLog,
		&types.SyncResult{}, nil, types.SyncLogStatusFailed, "boom", false,
	)
	require.NoError(t, err)

	assert.Equal(t, types.DataSourceStatusPaused, ds.Status,
		"a failure must not overwrite a concurrent pause")
}

// A resume applied while the sync ran must not be reverted back to Paused.
func TestUpdateSyncRunResult_ResumedSourceNotRepaused(t *testing.T) {
	ds := &types.DataSource{
		ID:              "ds-resume",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		Status:          types.DataSourceStatusActive,
	}
	svc, syncLog := newPauseSyncHarness(t, ds)

	// wasPaused=true is the stale snapshot (sync started while paused), but the
	// data source has been resumed to Active now. Completion must respect the resume.
	err := svc.updateSyncRunResult(
		context.Background(), ds, syncLog,
		&types.SyncResult{}, nil, types.SyncLogStatusSuccess, "", true,
	)
	require.NoError(t, err)

	assert.Equal(t, types.DataSourceStatusActive, ds.Status,
		"a resume applied while the sync ran must not be reverted to paused")
}

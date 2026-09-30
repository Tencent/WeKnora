package service

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcessSyncSerializesCursorSnapshots(t *testing.T) {
	testProcessSyncSerializesCursorSnapshots(t, func(svc *DataSourceService) *DataSourceService { return svc })
}

func testProcessSyncSerializesCursorSnapshots(t *testing.T, replica func(*DataSourceService) *DataSourceService) {
	t.Helper()
	svc, connector, repo := newConcurrentSyncService(t)
	secondService := replica(svc)
	first, second := concurrentSyncTask(t, "first"), concurrentSyncTask(t, "second")
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	go func() { firstDone <- svc.ProcessSync(context.Background(), first) }()
	require.Equal(t, int32(1), (<-connector.entered).revision)
	secondStarted := make(chan struct{})
	go func() {
		close(secondStarted)
		secondDone <- secondService.ProcessSync(context.Background(), second)
	}()
	<-secondStarted
	var next syncFetchObservation
	overlapped := false
	select {
	case next = <-connector.entered:
		overlapped = true
		// Ensure the newer cursor is committed before releasing the older fetch.
		require.NoError(t, <-secondDone)
	case <-time.After(100 * time.Millisecond):
	}
	close(connector.release)
	require.NoError(t, <-firstDone)
	if !overlapped {
		next = <-connector.entered
		require.NoError(t, <-secondDone)
	}
	assert.False(t, overlapped, "the second sync must wait before reading the source cursor")
	if assert.NotNil(t, next.cursor, "the waiting sync must reload the committed cursor") {
		assert.Equal(t, "1", next.cursor.ConnectorCursor["revision"])
	}
	stored, err := repo.source.ParseSyncCursor()
	require.NoError(t, err)
	assert.Equal(t, "2", stored.ConnectorCursor["revision"], "an older fetch must not overwrite newer state")
}

func TestProcessSyncRejectsFetchAfterCancellation(t *testing.T) {
	svc, connector, repo := newConcurrentSyncService(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	task := concurrentSyncTask(t, "first")
	done := make(chan error, 1)
	go func() { done <- svc.ProcessSync(ctx, task) }()
	<-connector.entered
	cancel()
	close(connector.release)
	require.ErrorIs(t, <-done, context.Canceled)
	assert.Zero(t, repo.writes, "a canceled fetch must not commit a new cursor or success")
}

func TestStreamHandlerCheckpointRejectsCanceledContext(t *testing.T) {
	repo := &recordingDSRepo{}
	svc := &DataSourceService{dsRepo: repo, syncLogRepo: &processSyncSyncLogRepo{logs: map[string]*types.SyncLog{}}}
	handler := newStreamHandler(svc, &types.DataSource{}, &types.SyncResult{}, &types.SyncLog{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := handler.Checkpoint(ctx, &types.SyncCursor{})
	assert.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, repo.updated, "an expired worker must not checkpoint")
}

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

type concurrentSyncRepo struct {
	interfaces.DataSourceRepository
	mu     sync.Mutex
	source types.DataSource
	writes int
}

func (r *concurrentSyncRepo) FindByID(context.Context, string) (*types.DataSource, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	snapshot := r.source
	return &snapshot, nil
}

func (r *concurrentSyncRepo) UpdateSyncState(_ context.Context, source *types.DataSource) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.source = *source
	r.writes++
	return nil
}

type concurrentSyncLogs struct {
	interfaces.SyncLogRepository
	mu   sync.Mutex
	logs map[string]types.SyncLog
}

func (r *concurrentSyncLogs) FindByID(_ context.Context, id string) (*types.SyncLog, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	log := r.logs[id]
	return &log, nil
}

func (r *concurrentSyncLogs) UpdateResult(_ context.Context, log *types.SyncLog) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs[log.ID] = *log
	return nil
}

func (r *concurrentSyncLogs) Update(ctx context.Context, log *types.SyncLog) error {
	return r.UpdateResult(ctx, log)
}

type concurrentSyncTags struct{ interfaces.KnowledgeTagService }

func (*concurrentSyncTags) FindOrCreateTagByName(context.Context, string, string) (*types.KnowledgeTag, error) {
	return nil, nil
}

type syncFetchObservation struct {
	revision int32
	cursor   *types.SyncCursor
	ctx      context.Context
}

type concurrentSyncConnector struct {
	deletedItemConnector
	calls   atomic.Int32
	entered chan syncFetchObservation
	release chan struct{}
}

func (c *concurrentSyncConnector) FetchIncremental(
	ctx context.Context, _ *types.DataSourceConfig, cursor *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	revision := c.calls.Add(1)
	c.entered <- syncFetchObservation{revision: revision, cursor: cursor, ctx: ctx}
	if revision == 1 {
		// Deliberately allow a slow connector to return after cancellation.
		<-c.release
	}
	return nil, &types.SyncCursor{
		ConnectorCursor: map[string]interface{}{"revision": fmt.Sprint(revision)},
	}, nil
}

func newConcurrentSyncService(t *testing.T) (*DataSourceService, *concurrentSyncConnector, *concurrentSyncRepo) {
	t.Helper()
	connector := &concurrentSyncConnector{
		entered: make(chan syncFetchObservation, 4), release: make(chan struct{}),
	}
	registry := datasource.NewConnectorRegistry()
	require.NoError(t, registry.Register(connector))
	config, err := (&types.DataSourceConfig{Type: connector.Type()}).ToJSON()
	require.NoError(t, err)
	repo := &concurrentSyncRepo{source: types.DataSource{
		ID: t.Name(), TenantID: 1, KnowledgeBaseID: "kb-sync",
		Type: connector.Type(), Config: config, SyncMode: types.SyncModeIncremental,
		Status: types.DataSourceStatusActive,
	}}
	logs := &concurrentSyncLogs{logs: map[string]types.SyncLog{
		"first":  {ID: "first", DataSourceID: t.Name(), Status: types.SyncLogStatusRunning},
		"second": {ID: "second", DataSourceID: t.Name(), Status: types.SyncLogStatusRunning},
	}}
	service := &DataSourceService{
		dsRepo: repo, syncLogRepo: logs, connectorRegistry: registry,
		kbService:  &processSyncKBService{kb: &types.KnowledgeBase{ID: "kb-sync", TenantID: 1}},
		tenantRepo: &processSyncTenantRepo{tenant: &types.Tenant{ID: 1}},
		tagService: &concurrentSyncTags{},
	}
	return service, connector, repo
}

func concurrentSyncTask(t *testing.T, logID string) *asynq.Task {
	t.Helper()
	payload, err := json.Marshal(types.DataSourceSyncPayload{
		DataSourceID: t.Name(), TenantID: 1, SyncLogID: logID, Trigger: logID,
	})
	require.NoError(t, err)
	return asynq.NewTask(types.TypeDataSourceSync, payload)
}

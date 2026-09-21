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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─────────────────────────────────────────────────────────────────────────────
// Feishu deep-adaptation legacy upgrade: legacy feishu data sources carry a
// one-shot Settings["resync_required"] marker (stamped by migration 000096 /
// sqlite 000017). The sync entry upgrades such a sync — even a scheduled
// incremental one — to a full pass and clears the marker on success.
// ─────────────────────────────────────────────────────────────────────────────

// resyncStreamConnector records the start cursor each FetchStream received, so
// tests can assert whether the sync ran full (nil cursor) or incremental.
type resyncStreamConnector struct {
	startCursors []*types.SyncCursor
	failFetch    bool
}

func (c *resyncStreamConnector) Type() string { return types.ConnectorTypeFeishu }
func (c *resyncStreamConnector) Validate(context.Context, *types.DataSourceConfig) error {
	return nil
}

func (c *resyncStreamConnector) ListResources(
	context.Context, *types.DataSourceConfig, string,
) ([]types.Resource, error) {
	return nil, nil
}

func (c *resyncStreamConnector) ResolveResourceAncestors(
	context.Context, *types.DataSourceConfig, []string,
) ([]string, error) {
	return nil, nil
}

func (c *resyncStreamConnector) FetchAll(
	context.Context, *types.DataSourceConfig, []string,
) ([]types.FetchedItem, error) {
	return nil, nil
}

func (c *resyncStreamConnector) FetchIncremental(
	context.Context, *types.DataSourceConfig, *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	return nil, nil, nil
}

func (c *resyncStreamConnector) FetchStream(
	_ context.Context, _ *types.DataSourceConfig, cursor *types.SyncCursor, _ datasource.StreamHandler,
) (*types.SyncCursor, error) {
	c.startCursors = append(c.startCursors, cursor)
	if c.failFetch {
		return nil, errors.New("fetch failed")
	}
	return &types.SyncCursor{ConnectorCursor: map[string]interface{}{"space_node_times": map[string]interface{}{}}}, nil
}

// resyncDSRepo captures Config-writing Update calls (backfill + marker clear).
type resyncDSRepo struct {
	kbDeleteDSRepo
	updates    int
	configs    []types.JSON
	syncStates int
}

func (r *resyncDSRepo) Update(_ context.Context, ds *types.DataSource) error {
	r.updates++
	r.configs = append(r.configs, ds.Config)
	return nil
}

func (r *resyncDSRepo) UpdateSyncState(_ context.Context, _ *types.DataSource) error {
	r.syncStates++
	return nil
}

// newResyncHarness wires a DataSourceService around the streaming fake for a
// feishu data source whose stored Config carries the given Settings.
func newResyncHarness(
	t *testing.T, settings map[string]interface{}, conn *resyncStreamConnector,
) (*DataSourceService, *types.DataSource, *resyncDSRepo, *types.SyncLog) {
	t.Helper()
	cfg := &types.DataSourceConfig{
		Type:        types.ConnectorTypeFeishu,
		Credentials: map[string]interface{}{"app_id": "cli-x", "app_secret": "sec"},
		ResourceIDs: []string{"space1"},
		Settings:    settings,
	}
	configJSON, err := cfg.ToJSON()
	require.NoError(t, err)

	ds := &types.DataSource{
		ID:              "ds-resync",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		Name:            "Feishu Legacy",
		Type:            types.ConnectorTypeFeishu,
		Config:          configJSON,
		SyncMode:        types.SyncModeIncremental,
		Status:          types.DataSourceStatusActive,
	}
	syncLog := &types.SyncLog{
		ID:           "log-resync",
		DataSourceID: ds.ID,
		TenantID:     ds.TenantID,
		Status:       types.SyncLogStatusRunning,
		StartedAt:    time.Now().UTC(),
	}
	dsRepo := &resyncDSRepo{kbDeleteDSRepo: *newKBDeleteDSRepo(ds.KnowledgeBaseID, ds)}
	registry := datasource.NewConnectorRegistry()
	require.NoError(t, registry.Register(conn))

	svc := &DataSourceService{
		dsRepo: dsRepo,
		syncLogRepo: &processSyncSyncLogRepo{
			logs: map[string]*types.SyncLog{syncLog.ID: syncLog},
		},
		kbService: &processSyncKBService{
			kb: &types.KnowledgeBase{ID: ds.KnowledgeBaseID, TenantID: ds.TenantID},
		},
		connectorRegistry: registry,
		tenantRepo:        &processSyncTenantRepo{tenant: &types.Tenant{ID: ds.TenantID}},
		tagService:        &processSyncTagService{},
	}
	return svc, ds, dsRepo, syncLog
}

func runResyncSync(t *testing.T, svc *DataSourceService, ds *types.DataSource, syncLog *types.SyncLog) *types.SyncLog {
	t.Helper()
	payload, err := json.Marshal(types.DataSourceSyncPayload{
		DataSourceID: ds.ID,
		TenantID:     ds.TenantID,
		SyncLogID:    syncLog.ID,
	})
	require.NoError(t, err)
	require.NoError(t, svc.ProcessSync(context.Background(), asynq.NewTask(types.TypeDataSourceSync, payload)))
	return svc.syncLogRepo.(*processSyncSyncLogRepo).logs[syncLog.ID]
}

// settingsOf decodes a stored Config blob and returns its Settings map.
func settingsOf(t *testing.T, blob types.JSON) map[string]interface{} {
	t.Helper()
	var raw struct {
		Settings map[string]interface{} `json:"settings"`
	}
	require.NoError(t, json.Unmarshal(blob, &raw))
	return raw.Settings
}

// A data source armed with the migration's resync_required marker runs FULL
// even though it is incremental, and the marker is cleared once the run
// succeeds.
func TestProcessSync_ResyncMarkerRunsFullOnceAndClears(t *testing.T) {
	conn := &resyncStreamConnector{}
	svc, ds, dsRepo, syncLog := newResyncHarness(t, map[string]interface{}{"resync_required": true}, conn)

	log := runResyncSync(t, svc, ds, syncLog)
	assert.Equal(t, types.SyncLogStatusSuccess, log.Status)

	// Full upgrade: the incremental sync started with no cursor.
	require.Len(t, conn.startCursors, 1)
	assert.Nil(t, conn.startCursors[0], "resync_required must upgrade the incremental sync to a full pass")

	// One Config write: clearing the marker on success.
	require.Equal(t, 1, dsRepo.updates)
	final := settingsOf(t, dsRepo.configs[0])
	assert.NotContains(t, final, "resync_required", "marker must be cleared after a successful upgrade")
}

// A data source without the marker stays incremental: no config writes.
func TestProcessSync_NoMarkerStaysIncremental(t *testing.T) {
	conn := &resyncStreamConnector{}
	svc, ds, dsRepo, syncLog := newResyncHarness(t, map[string]interface{}{"parse_mode": "export"}, conn)
	ds.LastSyncCursor = makeConnectorCursor(t, map[string]map[string]string{"space1": {"nt1": "100"}})

	log := runResyncSync(t, svc, ds, syncLog)
	assert.Equal(t, types.SyncLogStatusSuccess, log.Status)

	require.Len(t, conn.startCursors, 1)
	assert.NotNil(t, conn.startCursors[0], "no marker must keep the sync incremental")
	assert.Equal(t, 0, dsRepo.updates, "no backfill or marker write may happen when parse_mode exists")
}

// A failed upgraded sync keeps the marker so the next run retries the full
// upgrade instead of silently continuing incremental.
func TestProcessSync_FailedResyncKeepsMarker(t *testing.T) {
	conn := &resyncStreamConnector{failFetch: true}
	svc, ds, dsRepo, syncLog := newResyncHarness(t, map[string]interface{}{"resync_required": true}, conn)

	payload, err := json.Marshal(types.DataSourceSyncPayload{
		DataSourceID: ds.ID,
		TenantID:     ds.TenantID,
		SyncLogID:    syncLog.ID,
	})
	require.NoError(t, err)
	assert.Error(t, svc.ProcessSync(context.Background(), asynq.NewTask(types.TypeDataSourceSync, payload)))

	// No config writes on the failed run: the marker stays armed.
	assert.Equal(t, 0, dsRepo.updates)
}

// resyncRequired tolerates the encodings the marker may be written in.
func TestResyncRequired_Encodings(t *testing.T) {
	assert.False(t, resyncRequired(nil))
	assert.False(t, resyncRequired(&types.DataSourceConfig{}))
	assert.False(t, resyncRequired(&types.DataSourceConfig{Settings: map[string]interface{}{"resync_required": false}}))
	assert.False(t, resyncRequired(&types.DataSourceConfig{Settings: map[string]interface{}{"resync_required": "no"}}))
	assert.True(t, resyncRequired(&types.DataSourceConfig{Settings: map[string]interface{}{"resync_required": true}}))
	assert.True(t, resyncRequired(&types.DataSourceConfig{Settings: map[string]interface{}{"resync_required": "true"}}))
}

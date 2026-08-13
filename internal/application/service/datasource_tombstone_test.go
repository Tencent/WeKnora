package service

import (
	"context"
	"encoding/json"
	"mime/multipart"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// tombstoneTenantRepo / tombstoneTagService satisfy ProcessSync's tenant and
// auto-tag lookups with minimal behavior.
type tombstoneTenantRepo struct {
	interfaces.TenantRepository
	tenant *types.Tenant
}

func (r *tombstoneTenantRepo) GetTenantByID(context.Context, uint64) (*types.Tenant, error) {
	return r.tenant, nil
}

type tombstoneTagService struct {
	interfaces.KnowledgeTagService
}

func (*tombstoneTagService) FindOrCreateTagByName(context.Context, string, string) (*types.KnowledgeTag, error) {
	return nil, nil
}

// tombstoneRepo models the knowledge table's live/tombstone state with the
// same soft-delete mechanics the real repository uses:
//   - live: rows visible to FindByMetadataKey / FindByMetadataKeyPrefix
//   - tombstones: soft-deleted rows (DeleteKnowledge moved them here),
//     reported by FindTombstonedByDataSourceExternalID
//   - hardDeleted: physically removed rows (no tombstone effect)
type tombstoneRepo struct {
	interfaces.KnowledgeRepository
	liveByExternal map[string]*types.Knowledge
	liveByID       map[string]string // id → external_id
	tombstones     map[string]*types.Knowledge
	children       []*types.Knowledge // returned by FindByMetadataKeyPrefix

	tombstoneErr error // if set, the tombstone lookup fails

	lookupTenantID     uint64
	lookupKBID         string
	lookupDataSourceID string
	lookupExternalID   string

	hardDeleted     []string
	hardDeletedBats [][]string
}

func newTombstoneRepo() *tombstoneRepo {
	return &tombstoneRepo{
		liveByExternal: map[string]*types.Knowledge{},
		liveByID:       map[string]string{},
		tombstones:     map[string]*types.Knowledge{},
	}
}

func (r *tombstoneRepo) addLive(externalID string, k *types.Knowledge) {
	r.liveByExternal[externalID] = k
	r.liveByID[k.ID] = externalID
}

func (r *tombstoneRepo) FindByMetadataKey(
	_ context.Context, _ uint64, _ string, key, value string,
) (*types.Knowledge, error) {
	if key == "external_id" {
		return r.liveByExternal[value], nil
	}
	return nil, nil
}

func (r *tombstoneRepo) FindByDataSourceExternalID(
	_ context.Context, _ uint64, _ string, _ string, externalID string,
) (*types.Knowledge, error) {
	return r.liveByExternal[externalID], nil
}

func (r *tombstoneRepo) FindByMetadataKeyPrefix(
	context.Context, uint64, string, string, string,
) ([]*types.Knowledge, error) {
	return r.children, nil
}

func (r *tombstoneRepo) FindTombstonedByDataSourceExternalID(
	_ context.Context, tenantID uint64, kbID, dataSourceID, externalID string,
) (*types.Knowledge, error) {
	if r.tombstoneErr != nil {
		return nil, r.tombstoneErr
	}
	r.lookupTenantID = tenantID
	r.lookupKBID = kbID
	r.lookupDataSourceID = dataSourceID
	r.lookupExternalID = externalID
	return r.tombstones[externalID], nil
}

// HardDeleteKnowledge physically removes a row (update-replace path).
func (r *tombstoneRepo) HardDeleteKnowledge(_ context.Context, _ uint64, id string) error {
	r.hardDeleted = append(r.hardDeleted, id)
	if ext, ok := r.liveByID[id]; ok {
		delete(r.liveByExternal, ext)
		delete(r.liveByID, id)
	}
	for ext, row := range r.tombstones {
		if row.ID == id {
			delete(r.tombstones, ext)
		}
	}
	return nil
}

// HardDeleteKnowledgeList physically removes rows in batch (subtree sweep).
func (r *tombstoneRepo) HardDeleteKnowledgeList(_ context.Context, _ uint64, ids []string) error {
	r.hardDeletedBats = append(r.hardDeletedBats, ids)
	for _, id := range ids {
		_ = r.HardDeleteKnowledge(context.Background(), 0, id)
	}
	return nil
}

// tombstoneKS is a KnowledgeService fake. CreateKnowledgeFromFile records
// calls and honors an injected error. DeleteKnowledge / DeleteKnowledgeList
// move rows from live to tombstones like the soft-delete cascade.
type tombstoneKS struct {
	interfaces.KnowledgeService
	repo        *tombstoneRepo
	createErr   error
	createCalls int
	deletedIDs  []string
}

func (k *tombstoneKS) GetRepository() interfaces.KnowledgeRepository { return k.repo }

func (k *tombstoneKS) CreateKnowledgeFromFile(
	context.Context, string, *multipart.FileHeader, map[string]string,
	*bool, string, []string, string, *types.KnowledgeProcessOverrides,
) (*types.Knowledge, error) {
	k.createCalls++
	if k.createErr != nil {
		return nil, k.createErr
	}
	return &types.Knowledge{ID: "recreated"}, nil
}

func (k *tombstoneKS) DeleteKnowledge(_ context.Context, id string) error {
	k.deletedIDs = append(k.deletedIDs, id)
	if ext, ok := k.repo.liveByID[id]; ok {
		k.repo.tombstones[ext] = k.repo.liveByExternal[ext]
		delete(k.repo.liveByExternal, ext)
		delete(k.repo.liveByID, id)
	}
	return nil
}

func (k *tombstoneKS) DeleteKnowledgeList(_ context.Context, ids []string) error {
	for _, id := range ids {
		_ = k.DeleteKnowledge(context.Background(), id)
	}
	return nil
}

// tombstoneConnector returns a per-call round of items so a test can change
// what the source reports between syncs (e.g. a child re-appearing).
type tombstoneConnector struct {
	rounds [][]types.FetchedItem
	calls  int
}

func (c *tombstoneConnector) Type() string { return "test-tombstone-connector" }
func (c *tombstoneConnector) Validate(context.Context, *types.DataSourceConfig) error {
	return nil
}

func (c *tombstoneConnector) ListResources(context.Context, *types.DataSourceConfig, string) ([]types.Resource, error) {
	return nil, nil
}

func (c *tombstoneConnector) ResolveResourceAncestors(
	context.Context, *types.DataSourceConfig, []string,
) ([]string, error) {
	return nil, nil
}

func (c *tombstoneConnector) FetchAll(context.Context, *types.DataSourceConfig, []string) ([]types.FetchedItem, error) {
	return c.nextRound(), nil
}

func (c *tombstoneConnector) FetchIncremental(
	context.Context, *types.DataSourceConfig, *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	return c.nextRound(), nil, nil
}

func (c *tombstoneConnector) nextRound() []types.FetchedItem {
	idx := c.calls
	if len(c.rounds) == 0 {
		return nil
	}
	if idx >= len(c.rounds) {
		idx = len(c.rounds) - 1
	}
	c.calls++
	return c.rounds[idx]
}

func newTombstoneDataSource(t *testing.T, name string) *types.DataSource {
	t.Helper()
	configJSON, err := (&types.DataSourceConfig{Type: "test-tombstone-connector"}).ToJSON()
	require.NoError(t, err)
	return &types.DataSource{
		ID:              "ds-" + name,
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		Name:            name,
		Type:            "test-tombstone-connector",
		Config:          configJSON,
		SyncMode:        types.SyncModeFull,
		Status:          types.DataSourceStatusActive,
	}
}

func runTombstoneSync(
	t *testing.T, svc *DataSourceService, ds *types.DataSource,
	syncLogRepo *processSyncSyncLogRepo, logID string,
) *types.SyncLog {
	t.Helper()
	if _, ok := syncLogRepo.logs[logID]; !ok {
		syncLogRepo.logs[logID] = &types.SyncLog{
			ID:           logID,
			DataSourceID: ds.ID,
			TenantID:     ds.TenantID,
			Status:       types.SyncLogStatusRunning,
			StartedAt:    time.Now().UTC(),
		}
	}
	payload, err := json.Marshal(types.DataSourceSyncPayload{
		DataSourceID: ds.ID,
		TenantID:     ds.TenantID,
		SyncLogID:    logID,
		ForceFull:    true,
	})
	require.NoError(t, err)
	require.NoError(t, svc.ProcessSync(context.Background(), asynq.NewTask(types.TypeDataSourceSync, payload)))
	updated := syncLogRepo.logs[logID]
	require.NotNil(t, updated)
	return updated
}

func newTombstoneHarness(
	t *testing.T, ds *types.DataSource, connector *tombstoneConnector, ks *tombstoneKS,
) *DataSourceService {
	t.Helper()
	dsRepo := newKBDeleteDSRepo(ds.KnowledgeBaseID, ds)
	registry := datasource.NewConnectorRegistry()
	require.NoError(t, registry.Register(connector))
	return &DataSourceService{
		dsRepo:            dsRepo,
		syncLogRepo:       &processSyncSyncLogRepo{logs: map[string]*types.SyncLog{}},
		knowledgeService:  ks,
		kbService:         &processSyncKBService{},
		connectorRegistry: registry,
		tenantRepo:        &tombstoneTenantRepo{tenant: &types.Tenant{ID: ds.TenantID}},
		tagService:        &tombstoneTagService{},
	}
}

// TestProcessSync_TombstonedItemIsNotResurrected is the regression guard for
// deleted documents coming back: a document the user deleted from the KB must
// stay deleted even though the source still reports it. Before the fix the
// sync re-created the item ("deleted, 5 minutes later it comes back"). After
// the fix the item is counted as skipped and CreateKnowledgeFromFile is never
// called.
func TestProcessSync_TombstonedItemIsNotResurrected(t *testing.T) {
	ds := newTombstoneDataSource(t, "tombstone")
	connector := &tombstoneConnector{rounds: [][]types.FetchedItem{{{
		ExternalID: "file:gone-from-kb",
		Title:      "Manually Deleted Doc",
		Content:    []byte("# hello\n"),
		FileName:   "deleted.md",
	}}}}
	repo := newTombstoneRepo()
	repo.tombstones["file:gone-from-kb"] = &types.Knowledge{
		ID:        "soft-deleted-row",
		DeletedAt: gorm.DeletedAt{Time: time.Now(), Valid: true},
	}
	ks := &tombstoneKS{repo: repo}
	svc := newTombstoneHarness(t, ds, connector, ks)

	updated := runTombstoneSync(t, svc, ds, svc.syncLogRepo.(*processSyncSyncLogRepo), "log-tombstone")
	assert.Equal(t, types.SyncLogStatusSuccess, updated.Status)
	assert.Equal(t, 1, updated.ItemsTotal)
	assert.Equal(t, 1, updated.ItemsSkipped)
	assert.Zero(t, updated.ItemsCreated, "a tombstoned item must never be re-created")
	assert.Zero(t, ks.createCalls, "CreateKnowledgeFromFile must not run for a tombstoned item")
	assert.Equal(t, ds.TenantID, repo.lookupTenantID)
	assert.Equal(t, ds.KnowledgeBaseID, repo.lookupKBID)
	assert.Equal(t, ds.ID, repo.lookupDataSourceID)
	assert.Equal(t, "file:gone-from-kb", repo.lookupExternalID)

	// A later sync must behave identically: the exclusion is persistent.
	updated2 := runTombstoneSync(t, svc, ds, svc.syncLogRepo.(*processSyncSyncLogRepo), "log-tombstone-2")
	assert.Equal(t, 1, updated2.ItemsSkipped)
	assert.Zero(t, ks.createCalls, "still not re-created on a later sync")
}

// TestProcessSync_TombstoneLookupFailureDoesNotResurrect verifies the
// fail-closed path: a tombstone check that errors must not fall through to
// creation; the item counts as failed instead.
func TestProcessSync_TombstoneLookupFailureDoesNotResurrect(t *testing.T) {
	ds := newTombstoneDataSource(t, "tombstone-lookup-fail")
	connector := &tombstoneConnector{rounds: [][]types.FetchedItem{{{
		ExternalID: "file:doc",
		Title:      "Doc",
		Content:    []byte("# hello\n"),
		FileName:   "doc.md",
	}}}}
	repo := newTombstoneRepo()
	repo.tombstoneErr = assert.AnError
	ks := &tombstoneKS{repo: repo}
	svc := newTombstoneHarness(t, ds, connector, ks)
	syncLogRepo := svc.syncLogRepo.(*processSyncSyncLogRepo)
	syncLogRepo.logs["log-lookup-fail"] = &types.SyncLog{
		ID:           "log-lookup-fail",
		DataSourceID: ds.ID,
		TenantID:     ds.TenantID,
		Status:       types.SyncLogStatusRunning,
		StartedAt:    time.Now().UTC(),
	}

	payload, err := json.Marshal(types.DataSourceSyncPayload{
		DataSourceID: ds.ID,
		TenantID:     ds.TenantID,
		SyncLogID:    "log-lookup-fail",
		ForceFull:    true,
	})
	require.NoError(t, err)
	// The only item fails, so the whole sync surfaces as an error.
	require.Error(t, svc.ProcessSync(context.Background(), asynq.NewTask(types.TypeDataSourceSync, payload)))

	updated := syncLogRepo.logs["log-lookup-fail"]
	require.NotNil(t, updated)
	assert.Equal(t, 1, updated.ItemsFailed)
	assert.Zero(t, updated.ItemsCreated, "a failed tombstone check must not re-create the item")
	assert.Zero(t, ks.createCalls, "CreateKnowledgeFromFile must not run")
}

// TestProcessSync_UpdateIngestFailureRecoversNextSync guards the update-replace
// path: sync = delete-then-recreate, so a transient create failure must not
// leave a tombstone behind. The next sync must re-create the item from the
// source instead of skipping it as "deleted by user".
func TestProcessSync_UpdateIngestFailureRecoversNextSync(t *testing.T) {
	ds := newTombstoneDataSource(t, "update-recover")
	connector := &tombstoneConnector{rounds: [][]types.FetchedItem{{{
		ExternalID: "file:doc",
		Title:      "Doc",
		Content:    []byte("# v1\n"),
		FileName:   "doc.md",
	}}}}
	repo := newTombstoneRepo()
	repo.addLive("file:doc", &types.Knowledge{ID: "existing-live-row"})
	ks := &tombstoneKS{repo: repo}
	svc := newTombstoneHarness(t, ds, connector, ks)
	syncLogRepo := svc.syncLogRepo.(*processSyncSyncLogRepo)

	// First sync: update deletes the live row, then create fails. A sync where
	// every item fails is surfaced as an error, which is expected here.
	ks.createErr = assert.AnError
	syncLogRepo.logs["log-update-1"] = &types.SyncLog{
		ID:           "log-update-1",
		DataSourceID: ds.ID,
		TenantID:     ds.TenantID,
		Status:       types.SyncLogStatusRunning,
		StartedAt:    time.Now().UTC(),
	}
	payload1, err := json.Marshal(types.DataSourceSyncPayload{
		DataSourceID: ds.ID,
		TenantID:     ds.TenantID,
		SyncLogID:    "log-update-1",
		ForceFull:    true,
	})
	require.NoError(t, err)
	require.Error(t, svc.ProcessSync(context.Background(), asynq.NewTask(types.TypeDataSourceSync, payload1)))
	updated := syncLogRepo.logs["log-update-1"]
	require.NotNil(t, updated)
	assert.Equal(t, 1, updated.ItemsFailed)
	assert.Equal(t, []string{"existing-live-row"}, ks.deletedIDs)
	require.NotEmpty(t, repo.hardDeleted, "the replaced row must be hard-deleted, not left as a tombstone")

	// Second sync: the item must be re-created from the source (Created), not
	// skipped as a tombstone.
	ks.createErr = nil
	updated2 := runTombstoneSync(t, svc, ds, syncLogRepo, "log-update-2")
	assert.Zero(t, updated2.ItemsSkipped, "a failed update must not leave a tombstone behind")
	assert.Equal(t, 1, updated2.ItemsCreated, "the item must be re-created on the next sync")
}

// TestProcessSync_SweptChildReappearsIsReingested guards the subtree sweep:
// stale children removed by the sweep are sync-internal deletions. If the
// source re-adds such a child, it must be re-ingested instead of being
// skipped as "deleted by user".
func TestProcessSync_SweptChildReappearsIsReingested(t *testing.T) {
	ds := newTombstoneDataSource(t, "sweep-recover")
	parent := types.FetchedItem{
		ExternalID:      "doc:parent",
		Title:           "Parent",
		Content:         []byte("# parent\n"),
		FileName:        "parent.md",
		ReplacesSubtree: true,
		SubtreeKeep:     []string{"doc:parent#file#stays"},
	}
	child := types.FetchedItem{
		ExternalID: "doc:parent#file#c1",
		Title:      "Child",
		Content:    []byte("# child\n"),
		FileName:   "child.md",
	}
	connector := &tombstoneConnector{rounds: [][]types.FetchedItem{
		{parent}, // first sync: source reports only the parent
		{child},  // second sync: source re-adds the child
	}}
	repo := newTombstoneRepo()
	repo.children = []*types.Knowledge{
		{
			ID:       "stale-child-row",
			Metadata: types.JSON(`{"external_id":"doc:parent#file#c1","datasource_id":"ds-sweep-recover"}`),
		},
	}
	ks := &tombstoneKS{repo: repo}
	svc := newTombstoneHarness(t, ds, connector, ks)
	syncLogRepo := svc.syncLogRepo.(*processSyncSyncLogRepo)

	// First sync: parent ingests, the stale child (not in SubtreeKeep) is swept.
	updated := runTombstoneSync(t, svc, ds, syncLogRepo, "log-sweep-1")
	assert.Equal(t, types.SyncLogStatusSuccess, updated.Status)
	require.Contains(t, ks.deletedIDs, "stale-child-row")
	require.NotEmpty(t, repo.hardDeletedBats, "swept children must be hard-deleted, not left as tombstones")

	// Second sync: the source re-adds the child. It must be re-ingested.
	repo.children = nil
	updated2 := runTombstoneSync(t, svc, ds, syncLogRepo, "log-sweep-2")
	assert.Zero(t, updated2.ItemsSkipped, "a swept child must not be treated as a user tombstone")
	assert.Equal(t, 1, updated2.ItemsCreated, "a re-appearing child must be re-created")
}

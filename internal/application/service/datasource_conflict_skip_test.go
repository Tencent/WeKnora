package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─────────────────────────────────────────────────────────────────────────────
// Conflict strategy "skip" (跳过已存在): when a data source is configured with
// ConflictStrategySkip, re-syncing an item whose external_id already exists in
// the KB must leave the existing knowledge untouched and count the item as
// skipped. Tencent/WeKnora#4640: ingestItem ignored ds.ConflictStrategy and
// always replaced (delete + re-create) existing items.
// ─────────────────────────────────────────────────────────────────────────────

const conflictSkipConnectorType = "test-conflict-skip"

// conflictSkipConnector fetches one normal (non-deleted) content item whose
// external_id already exists in the KB fake.
type conflictSkipConnector struct{}

func (conflictSkipConnector) Type() string { return conflictSkipConnectorType }
func (conflictSkipConnector) Validate(context.Context, *types.DataSourceConfig) error {
	return nil
}

func (conflictSkipConnector) ListResources(context.Context, *types.DataSourceConfig, string) ([]types.Resource, error) {
	return nil, nil
}

func (conflictSkipConnector) ResolveResourceAncestors(
	context.Context, *types.DataSourceConfig, []string,
) ([]string, error) {
	return nil, nil
}

func (conflictSkipConnector) FetchAll(context.Context, *types.DataSourceConfig, []string) ([]types.FetchedItem, error) {
	return []types.FetchedItem{{
		ExternalID:       "file:exists",
		Title:            "Existing Doc",
		Content:          []byte("# v2\n"),
		ContentType:      "text/markdown",
		FileName:         "existing.md",
		SourceResourceID: "folder:1",
	}}, nil
}

func (conflictSkipConnector) FetchIncremental(
	context.Context, *types.DataSourceConfig, *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	items, err := (conflictSkipConnector{}).FetchAll(context.Background(), nil, nil)
	return items, nil, err
}

type conflictSkipKnowledgeService struct {
	*sweepFakeKS
	replacements int
}

func (s *conflictSkipKnowledgeService) ReplaceKnowledgeFromDataSource(
	_ context.Context, _ string, _ *types.FetchedItem, _ map[string]string, _ []string, _ string,
) (*types.Knowledge, error) {
	s.replacements++
	return &types.Knowledge{ID: "new-knowledge"}, nil
}

// newConflictSkipHarness wires a full-sync ProcessSync fixture whose data
// source carries the given conflict strategy and whose KB already contains the
// synced item under the same external_id.
func newConflictSkipHarness(
	t *testing.T, strategy string,
) (*DataSourceService, *types.DataSource, *deletionLookupKnowledgeRepo, *conflictSkipKnowledgeService, *types.SyncLog) {
	t.Helper()
	configJSON, err := (&types.DataSourceConfig{Type: conflictSkipConnectorType}).ToJSON()
	require.NoError(t, err)

	ds := &types.DataSource{
		ID:               "ds-conflict-skip",
		TenantID:         1,
		KnowledgeBaseID:  "kb-1",
		Name:             "Conflict Skip",
		Type:             conflictSkipConnectorType,
		Config:           configJSON,
		SyncMode:         types.SyncModeFull,
		Status:           types.DataSourceStatusActive,
		ConflictStrategy: strategy,
	}
	existing := &types.Knowledge{ID: "knowledge-exists", FileName: "existing.md"}
	repo := &deletionLookupKnowledgeRepo{knowledge: existing}
	ks := &conflictSkipKnowledgeService{sweepFakeKS: &sweepFakeKS{repo: repo}}
	syncLog := &types.SyncLog{
		ID:           "log-conflict-skip",
		DataSourceID: ds.ID,
		TenantID:     ds.TenantID,
		Status:       types.SyncLogStatusRunning,
		StartedAt:    time.Now().UTC(),
	}
	registry := datasource.NewConnectorRegistry()
	require.NoError(t, registry.Register(conflictSkipConnector{}))

	svc := &DataSourceService{
		dsRepo:           newKBDeleteDSRepo(ds.KnowledgeBaseID, ds),
		syncLogRepo:      &processSyncSyncLogRepo{logs: map[string]*types.SyncLog{syncLog.ID: syncLog}},
		knowledgeService: ks,
		kbService: &processSyncKBService{
			kb: &types.KnowledgeBase{ID: ds.KnowledgeBaseID, TenantID: ds.TenantID},
		},
		connectorRegistry: registry,
		tenantRepo:        &processSyncTenantRepo{tenant: &types.Tenant{ID: ds.TenantID}},
		tagService:        &processSyncTagService{},
	}
	return svc, ds, repo, ks, syncLog
}

func runConflictSkipSync(
	t *testing.T,
	svc *DataSourceService,
	ds *types.DataSource,
	syncLog *types.SyncLog,
) *types.SyncLog {
	t.Helper()
	payload, err := json.Marshal(types.DataSourceSyncPayload{
		DataSourceID: ds.ID,
		TenantID:     ds.TenantID,
		SyncLogID:    syncLog.ID,
		ForceFull:    true,
	})
	require.NoError(t, err)
	require.NoError(t, svc.ProcessSync(context.Background(), asynq.NewTask(types.TypeDataSourceSync, payload)))
	return svc.syncLogRepo.(*processSyncSyncLogRepo).logs[syncLog.ID]
}

// With ConflictStrategySkip the existing knowledge must be left untouched:
// no delete, no re-create, and the item counted as skipped.
func TestProcessSync_SkipStrategyKeepsExistingCountsSkipped(t *testing.T) {
	svc, ds, repo, ks, syncLog := newConflictSkipHarness(t, types.ConflictStrategySkip)

	log := runConflictSkipSync(t, svc, ds, syncLog)

	assert.Empty(t, repo.hardDeleted, "skip strategy must not hard-delete the existing knowledge")
	assert.Zero(t, ks.replacements)
	assert.NotContains(t, ks.events, "delete:knowledge-exists")
	assert.NotContains(t, ks.events, "create:existing.md")
	assert.Equal(t, 0, log.ItemsCreated)
	assert.Equal(t, 0, log.ItemsUpdated)
	assert.Equal(t, 1, log.ItemsSkipped)
}

// Default strategy (overwrite) keeps the historical replace behavior: the
// existing item is replaced transactionally, counted as updated.
func TestProcessSync_OverwriteStrategyReplacesExisting(t *testing.T) {
	svc, ds, repo, ks, syncLog := newConflictSkipHarness(t, types.ConflictStrategyOverwrite)

	log := runConflictSkipSync(t, svc, ds, syncLog)

	assert.Empty(t, repo.hardDeleted)
	assert.Empty(t, ks.events, "the caller must not delete or create outside the replacement transaction")
	assert.Equal(t, 1, ks.replacements)
	assert.Equal(t, 0, log.ItemsCreated)
	assert.Equal(t, 1, log.ItemsUpdated)
	assert.Equal(t, 0, log.ItemsSkipped)
}

// ingestItem-level: with skip strategy the lookup hit short-circuits into a
// DuplicateKnowledgeError (the caller's shared skip-counting path) before any
// delete or create happens.
func TestIngestItem_SkipStrategyShortCircuitsAsDuplicate(t *testing.T) {
	existing := &types.Knowledge{ID: "knowledge-exists", FileName: "existing.md"}
	repo := &deletionLookupKnowledgeRepo{knowledge: existing}
	ks := &sweepFakeKS{repo: repo}
	ds := &types.DataSource{
		ID:               "ds-1",
		TenantID:         1,
		KnowledgeBaseID:  "kb-1",
		ConflictStrategy: types.ConflictStrategySkip,
	}
	svc := &DataSourceService{knowledgeService: ks}

	isUpdate, err := svc.ingestItem(context.Background(), ds, &types.FetchedItem{
		ExternalID:  "file:exists",
		Title:       "Existing Doc",
		Content:     []byte("# v2\n"),
		ContentType: "text/markdown",
		FileName:    "existing.md",
	}, nil)

	require.Error(t, err)
	var dupErr *types.DuplicateKnowledgeError
	require.ErrorAs(t, err, &dupErr)
	assert.Same(t, existing, dupErr.Knowledge)
	assert.False(t, isUpdate)
	assert.Empty(t, repo.hardDeleted, "no hard delete may run for a skipped item")
	assert.Empty(t, ks.events, "neither delete nor create may run for a skipped item")
}

// URL-only items follow the same skip path: the existing knowledge survives
// and CreateKnowledgeFromURL is never called.
func TestIngestItem_SkipStrategyKeepsExistingURLItem(t *testing.T) {
	existing := &types.Knowledge{ID: "knowledge-url-exists", Source: "https://example.com/doc"}
	repo := &deletionLookupKnowledgeRepo{knowledge: existing}
	ks := &sweepFakeKS{repo: repo}
	ds := &types.DataSource{
		ID:               "ds-1",
		TenantID:         1,
		KnowledgeBaseID:  "kb-1",
		ConflictStrategy: types.ConflictStrategySkip,
	}
	svc := &DataSourceService{knowledgeService: ks}

	isUpdate, err := svc.ingestItem(context.Background(), ds, &types.FetchedItem{
		ExternalID: "url:1",
		URL:        "https://example.com/doc",
		Title:      "Existing URL Doc",
	}, nil)

	require.Error(t, err)
	var dupErr *types.DuplicateKnowledgeError
	require.ErrorAs(t, err, &dupErr)
	assert.Same(t, existing, dupErr.Knowledge)
	assert.False(t, isUpdate)
	assert.Empty(t, repo.hardDeleted)
	assert.Empty(t, ks.events)
	assert.Empty(t, repo.metadataUpdates, "CreateKnowledgeFromURL must not be reached for a skipped item")
}

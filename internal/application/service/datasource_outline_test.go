package service

import (
	"context"
	"errors"
	"mime/multipart"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestAcknowledgingHandlerReportsFailureAndDeferred(t *testing.T) {
	result := &types.SyncResult{}
	h := newStreamHandler(&DataSourceService{}, &types.DataSource{SyncDeletions: false}, result, &types.SyncLog{})
	outcome, err := h.EmitWithResult(context.Background(), types.FetchedItem{ExternalID: "a", IsDeleted: true})
	require.NoError(t, err)
	require.Equal(t, datasource.ApplyDeferred, outcome.Outcome)
	require.Zero(t, result.Total)
	outcome, err = h.EmitWithResult(context.Background(), types.FetchedItem{
		ExternalID: "a", Metadata: map[string]string{"error": "fetch failed"},
	})
	require.NoError(t, err)
	require.Equal(t, datasource.ApplyFailed, outcome.Outcome)
	require.Equal(t, 1, result.Total)
	require.Equal(t, 1, result.Failed)
}

func TestOutlineOptionsAllowOnlyPausedEmptyDraft(t *testing.T) {
	svc := &DataSourceService{}
	ds := &types.DataSource{Type: types.ConnectorTypeOutline, Status: types.DataSourceStatusPaused}
	cfg := &types.DataSourceConfig{}
	require.NoError(t, svc.validateOutlineOptions(ds, cfg))
	ds.SyncSchedule = "0 0 */6 * * *"
	require.Error(t, svc.validateOutlineOptions(ds, cfg))
	cfg.ResourceIDs = []string{"collection"}
	require.NoError(t, svc.validateOutlineOptions(ds, cfg))
	ds.ConflictStrategy = "skip"
	require.Error(t, svc.validateOutlineOptions(ds, cfg))
	ds.ConflictStrategy = "overwrite"
	ds.SyncSchedule = "* * * * *"
	require.Error(t, svc.validateOutlineOptions(ds, cfg))
}

type outlineAcceptanceKS struct {
	*sweepFakeKS
	created *types.Knowledge
}

func (k *outlineAcceptanceKS) CreateKnowledgeFromFile(context.Context, string, *multipart.FileHeader,
	map[string]string, *bool, string, []string, string, *types.KnowledgeProcessOverrides) (*types.Knowledge, error) {
	return k.created, nil
}

func TestOutlineRequiresParseAcceptanceAndStrictLookup(t *testing.T) {
	repo := &deletionLookupKnowledgeRepo{}
	ks := &outlineAcceptanceKS{sweepFakeKS: &sweepFakeKS{repo: repo},
		created: &types.Knowledge{ID: "a", ParseStatus: types.ParseStatusFailed}}
	svc := &DataSourceService{knowledgeService: ks}
	ds := &types.DataSource{ID: "ds", Type: types.ConnectorTypeOutline}
	item := &types.FetchedItem{ExternalID: "a", FileName: "a.md", Content: []byte("# a")}
	_, err := svc.ingestItem(context.Background(), ds, item, nil)
	require.Error(t, err)
	ks.created.ParseStatus = types.ParseStatusPending
	_, err = svc.ingestItem(context.Background(), ds, item, nil)
	require.NoError(t, err)
	repo.lookupErr = errors.New("lookup unavailable")
	_, err = svc.ingestItem(context.Background(), ds, item, nil)
	require.ErrorIs(t, err, repo.lookupErr)
}

func TestOutlineSameVersionIsIdempotentUnlessNewFullRun(t *testing.T) {
	repo := &deletionLookupKnowledgeRepo{knowledge: &types.Knowledge{ID: "old", ParseStatus: types.ParseStatusCompleted,
		Metadata: types.JSON(`{"source_fingerprint":"fp","source_sync_run_id":"run-a"}`)}}
	ks := &sweepFakeKS{repo: repo}
	svc := &DataSourceService{knowledgeService: ks}
	ds := &types.DataSource{ID: "ds", Type: types.ConnectorTypeOutline}
	item := &types.FetchedItem{ExternalID: "a", FileName: "a.md", Content: []byte("# a"),
		Metadata: map[string]string{"source_fingerprint": "fp"}}
	updated, err := svc.ingestItem(context.Background(), ds, item, nil)
	require.NoError(t, err)
	require.True(t, updated)
	require.Empty(t, ks.events)
	item.Metadata["source_full_sync"] = "true"
	item.Metadata["source_sync_run_id"] = "run-b"
	_, err = svc.ingestItem(context.Background(), ds, item, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"delete:old", "create:a.md"}, ks.events)
}

type deletionPolicyConnector struct{ deletedItemConnector }

func (deletionPolicyConnector) Type() string { return types.ConnectorTypeOutline }
func (deletionPolicyConnector) Validate(_ context.Context, cfg *types.DataSourceConfig) error {
	if cfg.SyncDeletions {
		return errors.New("documents.deleted permission required")
	}
	return nil
}

func TestOutlineEnablingDeletionRevalidatesUnchangedCredentials(t *testing.T) {
	cfg := &types.DataSourceConfig{Type: types.ConnectorTypeOutline, ResourceIDs: []string{"collection"},
		Credentials: map[string]interface{}{"base_url": "https://outline.example.com", "api_key": "key"}}
	blob, err := cfg.ToJSON()
	require.NoError(t, err)
	existing := &types.DataSource{ID: "outline-policy", TenantID: 1, KnowledgeBaseID: "kb",
		Type: types.ConnectorTypeOutline, Status: types.DataSourceStatusActive, Config: blob}
	registry := datasource.NewConnectorRegistry()
	require.NoError(t, registry.Register(deletionPolicyConnector{}))
	svc := &DataSourceService{dsRepo: newKBDeleteDSRepo("kb", existing), connectorRegistry: registry,
		syncLogRepo: &processSyncSyncLogRepo{logs: map[string]*types.SyncLog{}}}
	incoming := *existing
	incoming.SyncDeletions = true
	_, err = svc.UpdateDataSource(context.Background(), &incoming)
	require.ErrorContains(t, err, "documents.deleted permission required")
	require.False(t, existing.SyncDeletions)
}

package service

import (
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	plugincontrol "github.com/Tencent/WeKnora/internal/plugin"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

// externalSyncDataSourceRepo is intentionally tiny: ProcessSync only needs a
// data-source lookup and the cursor/state update for this acceptance test.
type externalSyncDataSourceRepo struct {
	interfaces.DataSourceRepository
	ds *types.DataSource
}

func (r *externalSyncDataSourceRepo) FindByID(context.Context, string) (*types.DataSource, error) {
	if r.ds == nil {
		return nil, errors.New("data source not found")
	}
	return r.ds, nil
}

func (r *externalSyncDataSourceRepo) UpdateSyncState(_ context.Context, ds *types.DataSource) error {
	r.ds = ds
	return nil
}

var _ interfaces.DataSourceRepository = (*externalSyncDataSourceRepo)(nil)

type externalSyncKBService struct {
	interfaces.KnowledgeBaseService
}

func (s *externalSyncKBService) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return &types.KnowledgeBase{ID: "kb-external", TenantID: 1}, nil
}

type externalSyncTenantRepo struct{ interfaces.TenantRepository }

func (externalSyncTenantRepo) GetTenantByID(_ context.Context, id uint64) (*types.Tenant, error) {
	return &types.Tenant{ID: id, Name: "test-tenant"}, nil
}

type externalIngestRepo struct{ interfaces.KnowledgeRepository }

func (externalIngestRepo) FindByDataSourceExternalID(context.Context, uint64, string, string, string) (*types.Knowledge, error) {
	return nil, nil
}

func (externalIngestRepo) FindAllByDataSourceExternalID(context.Context, uint64, string, string, string) ([]*types.Knowledge, error) {
	return nil, nil
}

func (externalIngestRepo) HardDeleteKnowledge(context.Context, uint64, string) error { return nil }

type externalIngestService struct {
	interfaces.KnowledgeService
	repo      interfaces.KnowledgeRepository
	fileNames []string
}

func (s *externalIngestService) GetRepository() interfaces.KnowledgeRepository { return s.repo }

func (s *externalIngestService) CreateKnowledgeFromFile(
	_ context.Context,
	_ string,
	_ *multipart.FileHeader,
	_ map[string]string,
	_ *bool,
	fileName string,
	_ []string,
	_ string,
	_ *types.KnowledgeProcessOverrides,
) (*types.Knowledge, error) {
	s.fileNames = append(s.fileNames, fileName)
	return &types.Knowledge{ID: "knowledge-" + fileName}, nil
}

func TestExternalLocalDirectoryProcessSyncOnlyIngestsChangedFile(t *testing.T) {
	pluginRoot := os.Getenv("WEKNORA_LOCALDIR_PLUGIN_ROOT")
	if pluginRoot == "" {
		t.Skip("set WEKNORA_LOCALDIR_PLUGIN_ROOT to run the external ProcessSync acceptance test")
	}

	sourceRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(sourceRoot, "a.md"), []byte("a-v1"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(sourceRoot, "b.md"), []byte("b-v1"), 0o644))

	manager := plugincontrol.NewManager("")
	registry := datasource.NewConnectorRegistry()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	require.NoError(t, plugincontrol.LoadExternal(ctx, []string{pluginRoot}, manager, registry))
	t.Cleanup(func() { _ = manager.Stop(context.Background(), "weknora.localdir") })

	config := &types.DataSourceConfig{Type: "weknora.localdir", Settings: map[string]interface{}{"root": sourceRoot}}
	configJSON, err := config.ToJSON()
	require.NoError(t, err)
	ds := &types.DataSource{
		ID:              "ds-external-localdir",
		TenantID:        1,
		KnowledgeBaseID: "kb-external",
		Name:            "external localdir",
		Type:            "weknora.localdir",
		Status:          types.DataSourceStatusActive,
		SyncMode:        types.SyncModeIncremental,
		SyncDeletions:   true,
		Config:          configJSON,
	}
	dsRepo := &externalSyncDataSourceRepo{ds: ds}
	logRepo := &processSyncSyncLogRepo{logs: map[string]*types.SyncLog{}}
	knowledge := &externalIngestService{repo: externalIngestRepo{}}

	svc := &DataSourceService{
		dsRepo:            dsRepo,
		syncLogRepo:       logRepo,
		knowledgeService:  knowledge,
		kbService:         &externalSyncKBService{},
		connectorRegistry: registry,
		connectorResolver: datasource.NewRegistryResolver(registry),
		tenantRepo:        externalSyncTenantRepo{},
		syncCoordinator:   datasource.NewSyncCoordinator(nil),
	}

	firstLog := &types.SyncLog{ID: "sync-1", DataSourceID: ds.ID, TenantID: 1, Status: types.SyncLogStatusPending, StartedAt: time.Now().UTC()}
	logRepo.logs[firstLog.ID] = firstLog
	firstPayload, err := json.Marshal(types.DataSourceSyncPayload{DataSourceID: ds.ID, TenantID: 1, SyncLogID: firstLog.ID, ForceFull: true})
	require.NoError(t, err)
	require.NoError(t, svc.ProcessSync(ctx, asynq.NewTask(types.TypeDataSourceSync, firstPayload)))
	require.ElementsMatch(t, []string{"a.md", "b.md"}, knowledge.fileNames)
	require.Equal(t, types.SyncLogStatusSuccess, firstLog.Status)
	require.NotEmpty(t, ds.LastSyncCursor)

	require.NoError(t, os.WriteFile(filepath.Join(sourceRoot, "a.md"), []byte("a-v2"), 0o644))
	secondLog := &types.SyncLog{ID: "sync-2", DataSourceID: ds.ID, TenantID: 1, Status: types.SyncLogStatusPending, StartedAt: time.Now().UTC()}
	logRepo.logs[secondLog.ID] = secondLog
	secondPayload, err := json.Marshal(types.DataSourceSyncPayload{DataSourceID: ds.ID, TenantID: 1, SyncLogID: secondLog.ID, ForceFull: false})
	require.NoError(t, err)
	require.NoError(t, svc.ProcessSync(ctx, asynq.NewTask(types.TypeDataSourceSync, secondPayload)))
	require.Equal(t, []string{"a.md", "b.md", "a.md"}, knowledge.fileNames)
	require.Equal(t, types.SyncLogStatusSuccess, secondLog.Status)
}

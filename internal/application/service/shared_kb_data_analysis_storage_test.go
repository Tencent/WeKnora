package service_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/application/repository"
	appservice "github.com/Tencent/WeKnora/internal/application/service"
	filesvc "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	_ "github.com/duckdb/duckdb-go/v2"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type sharedKBDataAnalysisKBService struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func (s *sharedKBDataAnalysisKBService) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

type sharedKBDataAnalysisKnowledgeService struct {
	interfaces.KnowledgeService
	knowledge *types.Knowledge
}

func (s *sharedKBDataAnalysisKnowledgeService) GetKnowledgeByIDOnly(context.Context, string) (*types.Knowledge, error) {
	return s.knowledge, nil
}

// A shared KB is accessed under the viewer's request context, but its storage
// backend belongs to the KB owner. Keep the real tenant-scoped resolver here so
// this test catches accidentally resolving the owner's backend as the viewer.
func TestDataAnalysisSharedKBUsesOwnerStorageBackend(t *testing.T) {
	localBaseDir := t.TempDir()
	t.Setenv("LOCAL_STORAGE_BASE_DIR", localBaseDir)
	ctx := context.WithValue(context.Background(), types.TenantInfoContextKey, &types.Tenant{ID: 2})
	ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(2))

	gormDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, gormDB.AutoMigrate(&types.Tenant{}, &types.StorageBackend{}))
	require.NoError(t, gormDB.Create(&types.Tenant{ID: 1, Name: "owner"}).Error)
	require.NoError(t, gormDB.Create(&types.Tenant{ID: 2, Name: "viewer"}).Error)

	storageRepo := repository.NewStorageBackendRepository(gormDB)
	backend := &types.StorageBackend{
		TenantID: 1, Name: "owner local", Provider: "local", Config: types.StorageBackendConfig{},
	}
	require.NoError(t, storageRepo.Create(ctx, backend))
	resolver := appservice.NewStorageBackendService(storageRepo, gormDB)
	ownerFiles, _, err := resolver.ResolveFileService(ctx, &types.Tenant{ID: 1}, backend.ID, "local", localBaseDir)
	require.NoError(t, err)
	filePath, err := ownerFiles.SaveBytes(ctx, []byte("amount\n42\n"), 1, "data.csv", false)
	require.NoError(t, err)

	duckDB, err := sql.Open("duckdb", "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = duckDB.Close() })

	kb := &types.KnowledgeBase{ID: "shared-kb", TenantID: 1, StorageBackendID: &backend.ID}
	knowledge := &types.Knowledge{
		ID: "shared-document", KnowledgeBaseID: "shared-kb", TenantID: 1,
		FileType: "csv", FileName: "data.csv", FilePath: filePath,
	}
	tenantService := appservice.NewTenantService(repository.NewTenantRepository(gormDB), storageRepo, nil)
	tool := tools.NewDataAnalysisTool(
		&sharedKBDataAnalysisKBService{kb: kb},
		&sharedKBDataAnalysisKnowledgeService{knowledge: knowledge}, tenantService,
		filesvc.NewLocalFileService(t.TempDir(), ""), duckDB, "shared-session", resolver,
	)

	schema, err := tool.LoadFromKnowledgeID(ctx, knowledge.ID)
	require.NoError(t, err)
	require.NotNil(t, schema)
	require.EqualValues(t, 1, schema.RowCount)
}

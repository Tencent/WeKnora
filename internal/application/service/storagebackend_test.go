package service_test

import (
	"context"
	"io"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestStorageBackendResolverScopesPathsAndTenant(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.StorageBackend{}))
	repo := repository.NewStorageBackendRepository(db)
	backend := &types.StorageBackend{TenantID: 7, Name: "Local A", Provider: "local", Config: types.StorageBackendConfig{}, LegacyAlias: true}
	require.NoError(t, repo.Create(context.Background(), backend))

	resolver := service.NewStorageBackendService(repo, db)
	svc, provider, err := resolver.ResolveFileService(context.Background(), &types.Tenant{ID: 7}, backend.ID, "local", t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, "local", provider)

	path, err := svc.SaveBytes(context.Background(), []byte("scoped"), 7, "exports/a.txt", false)
	require.NoError(t, err)
	assert.Contains(t, path, "storage://"+backend.ID+"/local://")
	reader, err := svc.GetFile(context.Background(), path)
	require.NoError(t, err)
	defer reader.Close()
	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	assert.Equal(t, "scoped", string(data))

	_, _, err = resolver.ResolveFileService(context.Background(), &types.Tenant{ID: 8}, backend.ID, "local", t.TempDir())
	require.Error(t, err)
}

func TestResolveFileServiceUsesWorkspaceDefaultForStubTenant(t *testing.T) {
	t.Setenv("STORAGE_TYPE", "local")
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.Tenant{}, &types.StorageBackend{}))

	tenantRepo := repository.NewTenantRepository(db)
	storageRepo := repository.NewStorageBackendRepository(db)
	tenant, err := service.NewTenantService(tenantRepo, storageRepo).CreateTenant(
		context.Background(), &types.Tenant{Name: "workspace"})
	require.NoError(t, err)
	require.NotNil(t, tenant.DefaultStorageBackendID)

	resolver := service.NewStorageBackendService(storageRepo, db)
	svc, provider, err := resolver.ResolveFileService(
		context.Background(), &types.Tenant{ID: tenant.ID}, "", "", t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, "local", provider)

	path, err := svc.SaveBytes(context.Background(), []byte("skill"), tenant.ID, "tenant-skills/sk.zip", false)
	require.NoError(t, err)
	assert.Contains(t, path, "storage://"+*tenant.DefaultStorageBackendID+"/")
}

func TestResolveFileServiceFallsBackToEnvWhenWorkspaceHasNoBackend(t *testing.T) {
	t.Setenv("STORAGE_TYPE", "local")
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.Tenant{}, &types.StorageBackend{}))
	require.NoError(t, db.Create(&types.Tenant{ID: 9, Name: "legacy"}).Error)

	resolver := service.NewStorageBackendService(repository.NewStorageBackendRepository(db), db)
	svc, provider, err := resolver.ResolveFileService(
		context.Background(), &types.Tenant{ID: 9}, "", "", t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, "local", provider)
	require.NotNil(t, svc)
}

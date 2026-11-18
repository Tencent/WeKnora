package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	filesvc "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func TestGlobalResourceBindingSurvivesCredentialAndLocationChanges(t *testing.T) {
	for name, value := range map[string]string{
		"STORAGE_TYPE": "oss", "OSS_ENDPOINT": "https://oss.example", "OSS_REGION": "region",
		"OSS_BUCKET_NAME": "first-bucket", "OSS_ACCESS_KEY": "first-key", "OSS_SECRET_KEY": "secret",
		"OSS_PATH_PREFIX": "", "OSS_TEMP_BUCKET_NAME": "", "OSS_TEMP_REGION": "",
	} {
		t.Setenv(name, value)
	}
	_, db := newResourceCatalogForTest(t)
	require.NoError(t, db.AutoMigrate(&types.StorageBackend{}))
	resources := repository.NewResourceRepository(db)
	storage := repository.NewStorageBackendRepository(db)
	newCatalog := func() *resourceCatalog {
		return NewResourceCatalogWithStorage(resources, storage).(*resourceCatalog)
	}
	ctx := context.Background()
	catalog := newCatalog()
	first, err := catalog.RegisterGlobal(ctx, 7,
		"oss://first-bucket/7/exports/a.png", interfaces.ResourceRegistration{})
	require.NoError(t, err)
	resource, err := catalog.ResolveStorageBinding(ctx, first)
	require.NoError(t, err)
	firstID := resource.StorageBackendID
	require.NotEmpty(t, firstID)

	t.Setenv("OSS_ACCESS_KEY", "rotated-key")
	catalog = newCatalog()
	resource, err = catalog.ResolveStorageBinding(ctx, first)
	require.NoError(t, err)
	require.Equal(t, firstID, resource.StorageBackendID)
	backend, err := storage.GetByID(ctx, 7, firstID)
	require.NoError(t, err)
	require.Equal(t, "rotated-key", backend.Config.AccessKeyID)
	require.False(t, backend.LegacyAlias)
	require.Equal(t, types.StorageBackendSourceEnv, backend.Source)

	t.Setenv("OSS_BUCKET_NAME", "second-bucket")
	catalog = newCatalog()
	second, err := catalog.RegisterGlobal(ctx, 7,
		"oss://second-bucket/7/exports/b.png", interfaces.ResourceRegistration{})
	require.NoError(t, err)
	resource, err = catalog.ResolveStorageBinding(ctx, second)
	require.NoError(t, err)
	require.NotEqual(t, firstID, resource.StorageBackendID)
	resource, err = catalog.ResolveStorageBinding(ctx, first)
	require.NoError(t, err)
	require.Equal(t, firstID, resource.StorageBackendID)
	backend, err = storage.GetByID(ctx, 7, firstID)
	require.NoError(t, err)
	require.Equal(t, "first-bucket", backend.Config.BucketName)
}

func TestBoundGlobalResourceDoesNotRecreateMissingOrDisabledInstance(t *testing.T) {
	for _, state := range []string{"disabled", "deleted"} {
		t.Run(state, func(t *testing.T) {
			t.Setenv("STORAGE_TYPE", "local")
			_, db := newResourceCatalogForTest(t)
			require.NoError(t, db.AutoMigrate(&types.StorageBackend{}))
			storage := repository.NewStorageBackendRepository(db)
			catalog := NewResourceCatalogWithStorage(repository.NewResourceRepository(db), storage).(*resourceCatalog)
			ctx := context.Background()
			ref, err := catalog.RegisterGlobal(ctx, 7, "local://7/exports/a.png", interfaces.ResourceRegistration{})
			require.NoError(t, err)
			resource, err := catalog.Resolve(ctx, ref)
			require.NoError(t, err)
			if state == "disabled" {
				require.NoError(t, db.Model(&types.StorageBackend{}).Where("id = ?", resource.StorageBackendID).
					Update("status", types.StorageBackendStatusDisabled).Error)
			} else {
				require.NoError(t, storage.Delete(ctx, 7, resource.StorageBackendID))
			}
			_, err = catalog.ResolveStorageBinding(ctx, ref)
			require.Error(t, err)
			stored, err := catalog.Resolve(ctx, ref)
			require.NoError(t, err)
			require.Equal(t, resource.StorageBackendID, stored.StorageBackendID)
		})
	}
}

func TestGlobalLocalWritesIgnoreWorkspacePathPrefix(t *testing.T) {
	t.Setenv("STORAGE_TYPE", "local")
	t.Setenv("LOCAL_STORAGE_PATH_PREFIX", "workspace-only")
	_, db := newResourceCatalogForTest(t)
	require.NoError(t, db.AutoMigrate(&types.StorageBackend{}))
	storage := repository.NewStorageBackendRepository(db)
	catalog := NewResourceCatalogWithStorage(repository.NewResourceRepository(db), storage)
	baseDir := t.TempDir()
	writer := filesvc.NewGlobalResourceCatalogFileService(filesvc.NewLocalFileService(baseDir, ""), catalog)
	ctx := context.Background()
	ref, err := writer.SaveBytes(ctx, []byte("artifact"), 7, "a.txt", false)
	require.NoError(t, err)
	resource, err := catalog.Resolve(ctx, ref)
	require.NoError(t, err)
	backend, err := storage.GetByID(ctx, 7, resource.StorageBackendID)
	require.NoError(t, err)
	require.NotNil(t, backend)
	require.Empty(t, backend.Config.PathPrefix)
	reader, _, err := NewStorageBackendService(storage, nil, catalog).
		ResolveFileService(ctx, &types.Tenant{ID: 7}, backend.ID, "local", baseDir)
	require.NoError(t, err)
	file, err := reader.GetFile(ctx, ref)
	require.NoError(t, err)
	require.NoError(t, file.Close())
}

func TestLegacyResourceBindingNeverOverwritesExistingBinding(t *testing.T) {
	catalog, db := newResourceCatalogForTest(t)
	ctx := context.Background()
	ref, err := catalog.Register(ctx, 7, "oss://bucket/7/exports/a.png", interfaces.ResourceRegistration{})
	require.NoError(t, err)
	resource, err := catalog.Resolve(ctx, ref)
	require.NoError(t, err)
	repo := repository.NewResourceRepository(db)
	bound, err := repo.BindStorageBackend(ctx, resource.ID, "actual-instance")
	require.NoError(t, err)
	require.Equal(t, "actual-instance", bound.StorageBackendID)
	bound, err = repo.BindStorageBackend(ctx, resource.ID, "stale-candidate")
	require.NoError(t, err)
	require.Equal(t, "actual-instance", bound.StorageBackendID)
}

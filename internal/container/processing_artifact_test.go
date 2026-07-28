package container

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"go.uber.org/dig"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type processingArtifactTenantRepository struct {
	interfaces.TenantRepository
}

type processingArtifactStorageResolver struct{}

func (*processingArtifactStorageResolver) ResolveFileService(
	context.Context,
	*types.Tenant,
	string,
	string,
	string,
) (interfaces.FileService, string, error) {
	return nil, "", nil
}

func (*processingArtifactStorageResolver) ResolveBackend(
	context.Context,
	*types.Tenant,
	string,
	string,
) (*types.StorageBackend, error) {
	return nil, nil
}

func TestProcessingArtifactProvidersResolveStore(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.ProcessingArtifact{}))

	c := dig.New()
	require.NoError(t, c.Provide(func() *gorm.DB { return db }))
	require.NoError(t, c.Provide(func() interfaces.TenantRepository {
		return &processingArtifactTenantRepository{}
	}))
	require.NoError(t, c.Provide(func() *config.Config {
		return &config.Config{ProcessingArtifact: &config.ProcessingArtifactConfig{MaxPayloadBytes: 2}}
	}))
	require.NoError(t, c.Provide(file.NewDummyFileService))
	require.NoError(t, c.Provide(func() interfaces.StorageBackendResolver {
		return &processingArtifactStorageResolver{}
	}))

	registerProcessingArtifactRepository(c)
	registerProcessingArtifactCounterRegistry(c)
	registerProcessingArtifactStore(c)
	registerProcessingArtifactRetentionService(c)

	require.NoError(t, c.Invoke(func(
		store interfaces.ProcessingArtifactStore,
		counters interfaces.ProcessingArtifactCounterRegistry,
		retention interfaces.ProcessingArtifactRetentionService,
	) {
		require.NotNil(t, store)
		require.NotNil(t, counters)
		require.Same(t, store, retention)
		key, keyErr := types.NewProcessingArtifactKey(1, "chunking", 1, []byte("configured-limit"))
		require.NoError(t, keyErr)
		_, created, putErr := store.PutIfAbsent(context.Background(), key, []byte("ok"))
		require.NoError(t, putErr)
		require.True(t, created)
		_, created, putErr = store.PutIfAbsent(context.Background(), key, []byte("big"))
		require.Error(t, putErr)
		require.False(t, created)
		require.NotEmpty(t, counters.Snapshot())
	}))
}

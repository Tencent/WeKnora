package container

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service/file"
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

func TestProcessingArtifactProvidersResolveStore(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.ProcessingArtifact{}))

	c := dig.New()
	require.NoError(t, c.Provide(func() *gorm.DB { return db }))
	require.NoError(t, c.Provide(func() interfaces.TenantRepository {
		return &processingArtifactTenantRepository{}
	}))
	require.NoError(t, c.Provide(file.NewDummyFileService))

	registerProcessingArtifactRepository(c)
	registerProcessingArtifactStore(c)

	require.NoError(t, c.Invoke(func(store interfaces.ProcessingArtifactStore) {
		require.NotNil(t, store)
	}))
}

package service_test

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestCreateTenantCreatesConcreteDefaultStorageBackend(t *testing.T) {
	t.Setenv("STORAGE_TYPE", "local")
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.Tenant{}, &types.StorageBackend{}))
	tenantRepo := repository.NewTenantRepository(db)
	storageRepo := repository.NewStorageBackendRepository(db)
	tenantSvc := service.NewTenantService(tenantRepo, storageRepo)

	tenant, err := tenantSvc.CreateTenant(context.Background(), &types.Tenant{Name: "workspace"})
	require.NoError(t, err)
	require.NotNil(t, tenant.DefaultStorageBackendID)

	backend, err := storageRepo.GetByID(context.Background(), tenant.ID, *tenant.DefaultStorageBackendID)
	require.NoError(t, err)
	require.NotNil(t, backend)
	assert.Equal(t, "local", backend.Provider)
	assert.Equal(t, types.StorageBackendSourceEnv, backend.Source)
	assert.True(t, backend.LegacyAlias)
}

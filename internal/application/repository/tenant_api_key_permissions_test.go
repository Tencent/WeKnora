package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestTenantAPIKeyRepositoryPreservesGranularScopeModes(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.TenantAPIKey{}))
	repo := NewTenantAPIKeyRepository(db)
	tenant := uint64(42)
	key := &types.TenantAPIKey{
		TenantID:  &tenant,
		ScopeType: types.APIKeyScopeTenant,
		Name:      "scope",
		KeyHash:   "scope-hash",
	}
	ctx := context.Background()
	require.NoError(t, repo.CreateAPIKey(ctx, key))
	for _, permissions := range []types.APIKeyKBPermissions{
		{"a": types.APIKeyKBRead, "b": types.APIKeyKBWrite}, {}, nil,
	} {
		_, err := repo.UpdateAPIKey(ctx, tenant, key.ID, &types.TenantAPIKey{
			Name: "scope", Capabilities: types.StringArray{"retrieve", "ingest"}, KnowledgeBasePermissions: permissions,
		})
		require.NoError(t, err)
		loaded, err := repo.GetAPIKeyByHash(ctx, key.KeyHash)
		require.NoError(t, err)
		require.Equal(t, permissions, loaded.KnowledgeBasePermissions)
		keys, err := repo.ListAPIKeys(ctx, tenant)
		require.NoError(t, err)
		require.Equal(t, permissions, keys[0].KnowledgeBasePermissions)
	}
}

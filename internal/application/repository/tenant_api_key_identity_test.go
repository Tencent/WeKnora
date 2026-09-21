package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestAPIKeyIdentityPersistenceAndConcurrentEdit(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "0123456789abcdef0123456789abcdef")
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.TenantAPIKey{}))
	repo := NewTenantAPIKeyRepository(db)
	ctx := context.Background()
	tid := uint64(7)
	key := &types.TenantAPIKey{
		TenantID: &tid, ScopeType: types.APIKeyScopeTenant, Name: "app", KeyHash: "hash", IdentityNamespace: "app-a",
		APIPrincipalConfig: &types.APIPrincipalConfig{
			Mode:       types.APIPrincipalModeSignedToken,
			HMACSecret: "signing-secret",
		},
	}
	require.NoError(t, repo.CreateAPIKey(ctx, key))
	var raw string
	require.NoError(t, db.Raw("SELECT api_principal_config FROM tenant_api_keys WHERE id = ?", key.ID).Scan(&raw).Error)
	require.NotContains(t, raw, "signing-secret")
	loaded, err := repo.GetAPIKeyByHash(ctx, "hash") // Authentication skips GORM hooks.
	require.NoError(t, err)
	require.Equal(t, "signing-secret", loaded.APIPrincipalConfig.HMACSecret)
	require.Equal(t, "app-a", loaded.IdentityNamespace)
	originalTime := loaded.UpdatedAt
	loaded.APIPrincipalConfig.HMACSecret = "rotated-secret"
	updated, err := repo.UpdateAPIKey(ctx, tid, key.ID, loaded)
	require.NoError(t, err)
	require.Equal(t, "rotated-secret", updated.APIPrincipalConfig.HMACSecret)
	loaded.UpdatedAt = originalTime
	loaded.APIPrincipalConfig.HMACSecret = "stale-secret"
	_, err = repo.UpdateAPIKey(ctx, tid, key.ID, loaded)
	require.Error(t, err, "a stale config must not restore a rotated secret")
	// An older client editing only permissions must leave identity untouched.
	preserved, err := repo.UpdateAPIKey(ctx, tid, key.ID, &types.TenantAPIKey{Name: "scope edit"})
	require.NoError(t, err)
	require.Equal(t, "rotated-secret", preserved.APIPrincipalConfig.HMACSecret)
	require.Equal(t, "app-a", preserved.IdentityNamespace)
}

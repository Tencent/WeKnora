package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestTenantAPIKeyRepositoryPersistsUTCExpiry(t *testing.T) {
	t.Setenv("TZ", "Asia/Shanghai")

	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.TenantAPIKey{}))

	repo := NewTenantAPIKeyRepository(db)
	ctx := context.Background()

	expiresAt := time.Unix(time.Now().UTC().Add(5*time.Second).Unix(), 0).UTC()
	tenantID := uint64(42)
	key := &types.TenantAPIKey{
		TenantID:   &tenantID,
		ScopeType:  types.APIKeyScopeTenant,
		Name:       "integration",
		KeyHash:    "hash-expiry",
		APIKey:     "sk-test",
		FullAccess: true,
		ExpiresAt:  &expiresAt,
	}
	require.NoError(t, repo.CreateAPIKey(ctx, key))

	loaded, err := repo.GetAPIKeyByHash(ctx, key.KeyHash)
	require.NoError(t, err)
	require.NotNil(t, loaded.ExpiresAt)
	require.Equal(t, time.UTC, loaded.ExpiresAt.Location())
	require.True(t, loaded.ExpiresAt.Equal(expiresAt))
}

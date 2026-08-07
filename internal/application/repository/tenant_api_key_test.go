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

// TestTenantAPIKeyRepositoryUpdateKnowledgeBasesIsTenantScoped 验证更新条件不会越权。
// 输入为同租户 scoped Key、其他租户 scoped Key 和 full-access Key；仅第一个允许更新并返回新范围。
func TestTenantAPIKeyRepositoryUpdateKnowledgeBasesIsTenantScoped(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.TenantAPIKey{}))
	repo := NewTenantAPIKeyRepository(db)
	ctx := context.Background()
	tenant42, tenant43 := uint64(42), uint64(43)
	keys := []*types.TenantAPIKey{
		{TenantID: &tenant42, ScopeType: types.APIKeyScopeTenant, Name: "scoped", KeyHash: "hash-scoped", APIKey: "sk-scoped"},
		{TenantID: &tenant43, ScopeType: types.APIKeyScopeTenant, Name: "other", KeyHash: "hash-other", APIKey: "sk-other"},
		{TenantID: &tenant42, ScopeType: types.APIKeyScopeTenant, Name: "full", KeyHash: "hash-full", APIKey: "sk-full", FullAccess: true},
	}
	for _, key := range keys {
		require.NoError(t, repo.CreateAPIKey(ctx, key))
	}

	updated, err := repo.UpdateAPIKeyKnowledgeBases(ctx, tenant42, keys[0].ID, types.StringArray{"kb-1", "kb-2"})
	require.NoError(t, err)
	require.Equal(t, types.StringArray{"kb-1", "kb-2"}, updated.KnowledgeBaseIDs)

	_, err = repo.UpdateAPIKeyKnowledgeBases(ctx, tenant42, keys[1].ID, types.StringArray{"kb-1"})
	require.ErrorIs(t, err, ErrTenantAPIKeyNotFound)
	_, err = repo.UpdateAPIKeyKnowledgeBases(ctx, tenant42, keys[2].ID, types.StringArray{"kb-1"})
	require.ErrorIs(t, err, ErrTenantAPIKeyNotFound)
}

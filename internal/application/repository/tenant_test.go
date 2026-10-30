package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupTestDB creates an in-memory SQLite database with tenant table.
func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.Tenant{}, &types.TenantMember{}))
	return db
}

func TestDeleteTenant_SoftDeletesMemberships(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()
	repo := NewTenantRepository(db)

	tenant := &types.Tenant{Name: "gone", Status: "active"}
	require.NoError(t, db.Create(tenant).Error)

	member := &types.TenantMember{
		UserID:   "user-1",
		TenantID: tenant.ID,
		Role:     types.TenantRoleOwner,
		Status:   types.TenantMemberStatusActive,
	}
	require.NoError(t, db.Create(member).Error)

	require.NoError(t, repo.DeleteTenant(ctx, tenant.ID))

	var tenantCount int64
	require.NoError(t, db.Model(&types.Tenant{}).Count(&tenantCount).Error)
	assert.Equal(t, int64(0), tenantCount)

	var memberCount int64
	require.NoError(t, db.Model(&types.TenantMember{}).Count(&memberCount).Error)
	assert.Equal(t, int64(0), memberCount)

	// Unscoped: rows still exist but are soft-deleted.
	var rawTenantCount int64
	require.NoError(t, db.Unscoped().Model(&types.Tenant{}).Count(&rawTenantCount).Error)
	assert.Equal(t, int64(1), rawTenantCount)

	var rawMemberCount int64
	require.NoError(t, db.Unscoped().Model(&types.TenantMember{}).Count(&rawMemberCount).Error)
	assert.Equal(t, int64(1), rawMemberCount)
}

func TestUpdateTenantChatHistoryOnlyPreservesUnrelatedSettings(t *testing.T) {
	db := setupTestDB(t)
	tenant := &types.Tenant{
		ID: 1, Name: "workspace", Status: "active", StorageQuota: 1000, StorageUsed: 42,
		ChatHistoryConfig: &types.ChatHistoryConfig{
			Enabled: true, EmbeddingModelID: "embedding", KnowledgeBaseID: "history",
		},
	}
	require.NoError(t, db.Create(tenant).Error)
	repo := NewTenantRepository(db)
	require.NoError(t, repo.SetChatHistoryConfig(t.Context(), 1,
		&types.ChatHistoryConfig{EmbeddingModelID: "embedding"}))
	got, err := repo.GetTenantByID(t.Context(), 1)
	require.NoError(t, err)
	require.False(t, got.ChatHistoryConfig.Enabled)
	require.Empty(t, got.ChatHistoryConfig.KnowledgeBaseID)
	require.Equal(t, "embedding", got.ChatHistoryConfig.EmbeddingModelID)
	require.Equal(t, "workspace", got.Name)
	require.EqualValues(t, 1000, got.StorageQuota)
	require.EqualValues(t, 42, got.StorageUsed)
}

func TestUpdateTenantCannotRestoreStaleChatHistoryConfiguration(t *testing.T) {
	db := setupTestDB(t)
	tenant := &types.Tenant{
		ID: 1, Name: "workspace", ChatHistoryConfig: &types.ChatHistoryConfig{
			Enabled: true, EmbeddingModelID: "embedding", KnowledgeBaseID: "old-history",
		},
	}
	require.NoError(t, db.Create(tenant).Error)
	repo := NewTenantRepository(db)
	stale, err := repo.GetTenantByID(t.Context(), 1)
	require.NoError(t, err)
	// A cleanup completes while an unrelated settings request still holds
	// its earlier snapshot of the whole tenant.
	require.NoError(t, repo.SetChatHistoryConfig(t.Context(), 1,
		&types.ChatHistoryConfig{EmbeddingModelID: "embedding"}))
	stale.Name = "renamed"
	require.NoError(t, repo.UpdateTenant(t.Context(), stale))
	got, err := repo.GetTenantByID(t.Context(), 1)
	require.NoError(t, err)
	require.Equal(t, "renamed", got.Name)
	require.False(t, got.ChatHistoryConfig.Enabled, "unrelated settings cannot resume a cleared index")
	require.Empty(t, got.ChatHistoryConfig.KnowledgeBaseID, "an old KB generation must not become active again")
}

func TestUpdateTenantProfileAndChatHistoryConfigPreserveEachOther(t *testing.T) {
	for _, historyFirst := range []bool{false, true} {
		order := "profile then history"
		if historyFirst {
			order = "history then profile"
		}
		t.Run(order, func(t *testing.T) {
			db := setupTestDB(t)
			tenant := &types.Tenant{
				ID: 1, Name: "workspace", Description: "old description",
				StorageQuota: 1000, StorageUsed: 42,
				ChatHistoryConfig: &types.ChatHistoryConfig{
					Enabled: true, EmbeddingModelID: "embedding", KnowledgeBaseID: "old-history",
				},
			}
			require.NoError(t, db.Create(tenant).Error)
			repo := NewTenantRepository(db)
			name, description := "renamed workspace", ""
			updateProfile := func() {
				require.NoError(t, repo.UpdateTenantProfile(t.Context(), 1, &name, &description))
			}
			clearHistory := func() {
				require.NoError(t, repo.SetChatHistoryConfig(t.Context(), 1,
					&types.ChatHistoryConfig{EmbeddingModelID: "embedding"}))
			}
			if historyFirst {
				clearHistory()
				updateProfile()
			} else {
				updateProfile()
				clearHistory()
			}

			got, err := repo.GetTenantByID(t.Context(), 1)
			require.NoError(t, err)
			require.Equal(t, name, got.Name)
			require.Empty(t, got.Description, "profile updates must still clear an explicit empty description")
			require.NotNil(t, got.ChatHistoryConfig)
			require.False(t, got.ChatHistoryConfig.Enabled)
			require.Empty(t, got.ChatHistoryConfig.KnowledgeBaseID)
			require.Equal(t, "embedding", got.ChatHistoryConfig.EmbeddingModelID)
			require.EqualValues(t, 1000, got.StorageQuota)
			require.EqualValues(t, 42, got.StorageUsed)
		})
	}
}

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
	require.NoError(t, db.AutoMigrate(&types.Tenant{}, &types.TenantMember{}, &types.User{}))
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

func TestDeleteTenant_ClearsHomeTenantPointer(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()
	repo := NewTenantRepository(db)

	tenant := &types.Tenant{Name: "home", Status: "active"}
	require.NoError(t, db.Create(tenant).Error)

	user := &types.User{
		ID:           "home-owner",
		Username:     "home-owner",
		Email:        "home-owner@example.com",
		PasswordHash: "hashed",
		TenantID:     tenant.ID,
		IsActive:     true,
	}
	require.NoError(t, db.Create(user).Error)

	require.NoError(t, repo.DeleteTenant(ctx, tenant.ID))

	// The dangling home pointer is cleared so login never resolves to the
	// soft-deleted tenant; the read hydrates SQL NULL back as the zero value.
	var stored types.User
	require.NoError(t, db.First(&stored, "id = ?", user.ID).Error)
	assert.Equal(t, uint64(0), stored.TenantID)

	// Stored as NULL, not 0 — users.tenant_id is nullable and FK-checked in
	// PostgreSQL (see userRepository.UpdateUser).
	var nullCount int64
	require.NoError(t, db.Table("users").
		Where("id = ? AND tenant_id IS NULL", user.ID).Count(&nullCount).Error)
	assert.Equal(t, int64(1), nullCount)
}

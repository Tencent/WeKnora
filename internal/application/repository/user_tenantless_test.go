package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestUserRepositoryTenantlessCreateAndUpdateKeepNullTenantID(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:user_tenantless?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&types.Tenant{}, &types.User{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	repo := NewUserRepository(db)
	user := &types.User{
		ID:           "tenantless-user",
		Username:     "before-update",
		Email:        "tenantless@example.com",
		PasswordHash: "hashed",
		TenantID:     0,
		IsActive:     true,
	}
	if err := repo.CreateUser(context.Background(), user); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	assertNullTenantID(t, db, user.ID)

	user.Username = "after-update"
	if err := repo.UpdateUser(context.Background(), user); err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}
	assertNullTenantID(t, db, user.ID)

	var stored types.User
	if err := db.First(&stored, "id = ?", user.ID).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if stored.Username != "after-update" || stored.TenantID != 0 {
		t.Fatalf("stored user = %#v", stored)
	}
}

func assertNullTenantID(t *testing.T, db *gorm.DB, userID string) {
	t.Helper()
	var count int64
	if err := db.Table("users").Where("id = ? AND tenant_id IS NULL", userID).Count(&count).Error; err != nil {
		t.Fatalf("check tenant_id: %v", err)
	}
	if count != 1 {
		t.Fatalf("tenant_id for user %s is not NULL", userID)
	}
}

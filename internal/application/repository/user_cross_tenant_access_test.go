package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestUserRepositoryCrossTenantAccessLifecycle(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:user_cross_tenant_access?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&types.Tenant{}, &types.User{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	repo := NewUserRepository(db)
	ctx := context.Background()
	users := []*types.User{
		{ID: "actor", Username: "actor", Email: "actor@example.com", PasswordHash: "hash", CanAccessAllTenants: true},
		{ID: "target", Username: "target", Email: "target@example.com", PasswordHash: "hash"},
		{ID: "peer", Username: "peer", Email: "peer@example.com", PasswordHash: "hash", CanAccessAllTenants: true},
	}
	for _, user := range users {
		if err := repo.CreateUser(ctx, user); err != nil {
			t.Fatalf("create %s: %v", user.ID, err)
		}
	}

	listed, total, err := repo.ListCrossTenantAccessUsers(ctx, 0, 10)
	if err != nil || total != 2 || len(listed) != 2 {
		t.Fatalf("initial list = users:%d total:%d err:%v", len(listed), total, err)
	}

	granted, changed, err := repo.GrantCrossTenantAccess(ctx, "target")
	if err != nil || !changed || !granted.CanAccessAllTenants {
		t.Fatalf("grant = user:%+v changed:%v err:%v", granted, changed, err)
	}
	_, changed, err = repo.GrantCrossTenantAccess(ctx, "target")
	if err != nil || changed {
		t.Fatalf("idempotent grant changed=%v err=%v", changed, err)
	}

	revoked, changed, err := repo.RevokeCrossTenantAccess(ctx, "target", "actor")
	if err != nil || !changed || revoked.CanAccessAllTenants {
		t.Fatalf("revoke = user:%+v changed:%v err:%v", revoked, changed, err)
	}
	_, changed, err = repo.RevokeCrossTenantAccess(ctx, "target", "actor")
	if err != nil || changed {
		t.Fatalf("idempotent revoke changed=%v err=%v", changed, err)
	}

	_, _, err = repo.RevokeCrossTenantAccess(ctx, "actor", "actor")
	if !errors.Is(err, ErrCannotRevokeOwnCrossTenantAccess) {
		t.Fatalf("self revoke error = %v", err)
	}
}

func TestCountCrossTenantAccessManagersOnlyCountsActiveDualPrivilegeUsers(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:count_cross_tenant_access_managers?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&types.Tenant{}, &types.User{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	repo := NewUserRepository(db)
	ctx := context.Background()
	users := []*types.User{
		{ID: "manager", Username: "manager", Email: "manager@example.com", PasswordHash: "hash", IsSystemAdmin: true, CanAccessAllTenants: true},
		{ID: "admin-only", Username: "admin-only", Email: "admin-only@example.com", PasswordHash: "hash", IsSystemAdmin: true},
		{ID: "cross-only", Username: "cross-only", Email: "cross-only@example.com", PasswordHash: "hash", CanAccessAllTenants: true},
		{ID: "deleted-manager", Username: "deleted-manager", Email: "deleted@example.com", PasswordHash: "hash", IsSystemAdmin: true, CanAccessAllTenants: true},
	}
	for _, user := range users {
		if err := repo.CreateUser(ctx, user); err != nil {
			t.Fatalf("create %s: %v", user.ID, err)
		}
	}
	if err := repo.DeleteUser(ctx, "deleted-manager"); err != nil {
		t.Fatalf("delete manager: %v", err)
	}

	total, err := repo.CountCrossTenantAccessManagers(ctx)
	if err != nil || total != 1 {
		t.Fatalf("manager count=%d err=%v", total, err)
	}
}

package repository

import (
	"context"
	"errors"
	"testing"
	"time"

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

	listed, next, err := repo.ListCrossTenantAccessUsers(ctx, nil, 10)
	if err != nil || next != nil || len(listed) != 2 {
		t.Fatalf("initial list = users:%d next:%+v err:%v", len(listed), next, err)
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

func TestListCrossTenantAccessUsersCursorSurvivesHeadRevocation(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:cross_tenant_cursor?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&types.Tenant{}, &types.User{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	repo := NewUserRepository(db)
	ctx := context.Background()
	newest := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	tied := newest.Add(-time.Minute)
	users := []*types.User{
		{ID: "a", Username: "a", Email: "a@example.com", PasswordHash: "hash", CanAccessAllTenants: true, CreatedAt: newest},
		{ID: "b", Username: "b", Email: "b@example.com", PasswordHash: "hash", CanAccessAllTenants: true, CreatedAt: tied},
		{ID: "c", Username: "c", Email: "c@example.com", PasswordHash: "hash", CanAccessAllTenants: true, CreatedAt: tied},
		{ID: "d", Username: "d", Email: "d@example.com", PasswordHash: "hash", CanAccessAllTenants: true, CreatedAt: tied.Add(-time.Minute)},
	}
	for _, user := range users {
		if err := repo.CreateUser(ctx, user); err != nil {
			t.Fatalf("create %s: %v", user.ID, err)
		}
	}

	first, cursor, err := repo.ListCrossTenantAccessUsers(ctx, nil, 2)
	if err != nil || cursor == nil || len(first) != 2 || first[0].ID != "a" || first[1].ID != "b" {
		t.Fatalf("first page users=%v cursor=%+v err=%v", userIDs(first), cursor, err)
	}
	if _, changed, err := repo.RevokeCrossTenantAccess(ctx, "a", "operator"); err != nil || !changed {
		t.Fatalf("revoke head changed=%v err=%v", changed, err)
	}

	second, next, err := repo.ListCrossTenantAccessUsers(ctx, cursor, 2)
	if err != nil || next != nil || len(second) != 2 || second[0].ID != "c" || second[1].ID != "d" {
		t.Fatalf("second page users=%v next=%+v err=%v", userIDs(second), next, err)
	}
}

func userIDs(users []*types.User) []string {
	ids := make([]string, 0, len(users))
	for _, user := range users {
		ids = append(ids, user.ID)
	}
	return ids
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
		{ID: "manager", Username: "manager", Email: "manager@example.com", PasswordHash: "hash", IsActive: true, IsSystemAdmin: true, CanAccessAllTenants: true},
		{ID: "admin-only", Username: "admin-only", Email: "admin-only@example.com", PasswordHash: "hash", IsActive: true, IsSystemAdmin: true},
		{ID: "cross-only", Username: "cross-only", Email: "cross-only@example.com", PasswordHash: "hash", IsActive: true, CanAccessAllTenants: true},
		{ID: "inactive-manager", Username: "inactive-manager", Email: "inactive@example.com", PasswordHash: "hash", IsActive: true, IsSystemAdmin: true, CanAccessAllTenants: true},
		{ID: "deleted-manager", Username: "deleted-manager", Email: "deleted@example.com", PasswordHash: "hash", IsActive: true, IsSystemAdmin: true, CanAccessAllTenants: true},
	}
	for _, user := range users {
		if err := repo.CreateUser(ctx, user); err != nil {
			t.Fatalf("create %s: %v", user.ID, err)
		}
	}
	inactiveManager, err := repo.GetUserByID(ctx, "inactive-manager")
	if err != nil {
		t.Fatalf("load inactive manager: %v", err)
	}
	inactiveManager.IsActive = false
	if err := repo.UpdateUser(ctx, inactiveManager); err != nil {
		t.Fatalf("disable manager: %v", err)
	}
	if err := repo.DeleteUser(ctx, "deleted-manager"); err != nil {
		t.Fatalf("delete manager: %v", err)
	}

	total, err := repo.CountCrossTenantAccessManagers(ctx)
	if err != nil || total != 1 {
		t.Fatalf("manager count=%d err=%v", total, err)
	}
	activeAdmins, err := repo.CountActiveSystemAdmins(ctx)
	if err != nil || activeAdmins != 2 {
		t.Fatalf("active system admin count=%d err=%v", activeAdmins, err)
	}
}

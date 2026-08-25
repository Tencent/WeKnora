package repository

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newPrivilegeInvariantRepository(t *testing.T) interfaces.UserRepository {
	t.Helper()
	databaseName := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(sqlite.Open("file:"+databaseName+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql database: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&types.Tenant{}, &types.User{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.Create(&types.Tenant{ID: 1, Name: "test-tenant"}).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	return NewUserRepository(db)
}

func createPrivilegeInvariantUsers(t *testing.T, repo interfaces.UserRepository, users ...*types.User) {
	t.Helper()
	for _, user := range users {
		if err := repo.CreateUser(context.Background(), user); err != nil {
			t.Fatalf("create user %s: %v", user.ID, err)
		}
	}
}

func privilegeUser(id string, isSystemAdmin, canAccessAllTenants bool) *types.User {
	return &types.User{
		ID:                  id,
		Username:            id,
		Email:               id + "@example.com",
		PasswordHash:        "hash",
		IsSystemAdmin:       isSystemAdmin,
		CanAccessAllTenants: canAccessAllTenants,
	}
}

func TestPrivilegeRevocationPreservesLastCrossTenantAccessManager(t *testing.T) {
	repo := newPrivilegeInvariantRepository(t)
	createPrivilegeInvariantUsers(t, repo,
		privilegeUser("manager", true, true),
		privilegeUser("admin", true, false),
	)
	ctx := context.Background()

	if _, err := repo.RevokeSystemAdmin(ctx, "manager", "admin"); !errors.Is(err, ErrLastCrossTenantAccessManager) {
		t.Fatalf("revoke system admin error = %v", err)
	}
	if _, _, err := repo.RevokeCrossTenantAccess(ctx, "manager", "admin"); !errors.Is(err, ErrLastCrossTenantAccessManager) {
		t.Fatalf("revoke cross-tenant access error = %v", err)
	}

	manager, err := repo.GetUserByID(ctx, "manager")
	if err != nil {
		t.Fatalf("get manager: %v", err)
	}
	if !manager.IsSystemAdmin || !manager.CanAccessAllTenants {
		t.Fatalf("last manager privileges changed: %+v", manager)
	}
}

func TestPrivilegeRevocationAllowsSafeTargets(t *testing.T) {
	t.Run("system admin flag with another manager", func(t *testing.T) {
		repo := newPrivilegeInvariantRepository(t)
		createPrivilegeInvariantUsers(t, repo,
			privilegeUser("manager-a", true, true),
			privilegeUser("manager-b", true, true),
			privilegeUser("actor", true, false),
		)

		user, err := repo.RevokeSystemAdmin(context.Background(), "manager-a", "actor")
		if err != nil || user.IsSystemAdmin {
			t.Fatalf("revoke system admin = user:%+v err:%v", user, err)
		}
	})

	t.Run("cross-tenant flag with another manager", func(t *testing.T) {
		repo := newPrivilegeInvariantRepository(t)
		createPrivilegeInvariantUsers(t, repo,
			privilegeUser("manager-a", true, true),
			privilegeUser("manager-b", true, true),
		)

		user, changed, err := repo.RevokeCrossTenantAccess(context.Background(), "manager-a", "manager-b")
		if err != nil || !changed || user.CanAccessAllTenants {
			t.Fatalf("revoke cross-tenant access = user:%+v changed:%v err:%v", user, changed, err)
		}
	})

	t.Run("admin-only target", func(t *testing.T) {
		repo := newPrivilegeInvariantRepository(t)
		createPrivilegeInvariantUsers(t, repo,
			privilegeUser("manager", true, true),
			privilegeUser("admin", true, false),
		)

		user, err := repo.RevokeSystemAdmin(context.Background(), "admin", "manager")
		if err != nil || user.IsSystemAdmin {
			t.Fatalf("revoke admin-only target = user:%+v err:%v", user, err)
		}
	})
}

func TestConcurrentCrossTenantRevokesLeaveOneManager(t *testing.T) {
	repo := newPrivilegeInvariantRepository(t)
	createPrivilegeInvariantUsers(t, repo,
		privilegeUser("manager-a", true, true),
		privilegeUser("manager-b", true, true),
	)

	start := make(chan struct{})
	errorsByRequest := make(chan error, 2)
	var wg sync.WaitGroup
	for _, ids := range [][2]string{{"manager-a", "manager-b"}, {"manager-b", "manager-a"}} {
		wg.Add(1)
		go func(targetID, actorID string) {
			defer wg.Done()
			<-start
			_, _, err := repo.RevokeCrossTenantAccess(context.Background(), targetID, actorID)
			errorsByRequest <- err
		}(ids[0], ids[1])
	}
	close(start)
	wg.Wait()
	close(errorsByRequest)

	successes := 0
	protected := 0
	for err := range errorsByRequest {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrLastCrossTenantAccessManager):
			protected++
		default:
			t.Fatalf("unexpected concurrent revoke error: %v", err)
		}
	}
	if successes != 1 || protected != 1 {
		t.Fatalf("concurrent outcomes = successes:%d protected:%d", successes, protected)
	}

	users, total, err := repo.ListCrossTenantAccessUsers(context.Background(), 0, 10)
	if err != nil || total != 1 || len(users) != 1 {
		t.Fatalf("remaining managers = users:%d total:%d err:%v", len(users), total, err)
	}
}

func TestUpdateUserCannotOverwritePlatformPrivilegesFromStaleSnapshot(t *testing.T) {
	repo := newPrivilegeInvariantRepository(t)
	managerA := privilegeUser("manager-a", false, false)
	managerA.TenantID = 1
	managerB := privilegeUser("manager-b", true, true)
	managerB.TenantID = 1
	createPrivilegeInvariantUsers(t, repo,
		managerA,
		managerB,
	)
	ctx := context.Background()

	staleManager, err := repo.GetUserByID(ctx, "manager-a")
	if err != nil {
		t.Fatalf("load stale manager snapshot: %v", err)
	}
	if _, changed, err := repo.GrantSystemAdmin(ctx, "manager-a"); err != nil || !changed {
		t.Fatalf("grant system admin changed=%v err=%v", changed, err)
	}
	if _, changed, err := repo.GrantCrossTenantAccess(ctx, "manager-a"); err != nil || !changed {
		t.Fatalf("grant cross-tenant access changed=%v err=%v", changed, err)
	}
	if _, changed, err := repo.RevokeCrossTenantAccess(ctx, "manager-b", "manager-a"); err != nil || !changed {
		t.Fatalf("revoke peer cross-tenant access changed=%v err=%v", changed, err)
	}

	staleManager.Username = "updated-profile"
	if err := repo.UpdateUser(ctx, staleManager); err != nil {
		t.Fatalf("save stale manager snapshot: %v", err)
	}

	stored, err := repo.GetUserByID(ctx, "manager-a")
	if err != nil {
		t.Fatalf("reload manager: %v", err)
	}
	if stored.Username != "updated-profile" {
		t.Fatalf("ordinary field was not updated: %+v", stored)
	}
	if !stored.IsSystemAdmin || !stored.CanAccessAllTenants {
		t.Fatalf("stale update overwrote platform privileges: %+v", stored)
	}
	_, total, err := repo.ListCrossTenantAccessUsers(ctx, 0, 10)
	if err != nil || total != 1 {
		t.Fatalf("cross-tenant access users total=%d err=%v", total, err)
	}
}

func TestUpdateUserCannotEscalatePlatformPrivileges(t *testing.T) {
	repo := newPrivilegeInvariantRepository(t)
	createPrivilegeInvariantUsers(t, repo, privilegeUser("regular", false, false))
	ctx := context.Background()

	regular, err := repo.GetUserByID(ctx, "regular")
	if err != nil {
		t.Fatalf("load regular user: %v", err)
	}
	regular.IsSystemAdmin = true
	regular.CanAccessAllTenants = true
	regular.Username = "updated-regular"
	if err := repo.UpdateUser(ctx, regular); err != nil {
		t.Fatalf("update regular user: %v", err)
	}

	stored, err := repo.GetUserByID(ctx, "regular")
	if err != nil {
		t.Fatalf("reload regular user: %v", err)
	}
	if stored.Username != "updated-regular" || stored.IsSystemAdmin || stored.CanAccessAllTenants {
		t.Fatalf("ordinary update escalated platform privileges: %+v", stored)
	}
}

func TestGrantSystemAdminIsIdempotent(t *testing.T) {
	repo := newPrivilegeInvariantRepository(t)
	createPrivilegeInvariantUsers(t, repo, privilegeUser("target", false, false))
	ctx := context.Background()

	granted, changed, err := repo.GrantSystemAdmin(ctx, "target")
	if err != nil || !changed || !granted.IsSystemAdmin {
		t.Fatalf("first grant = user:%+v changed:%v err:%v", granted, changed, err)
	}
	granted, changed, err = repo.GrantSystemAdmin(ctx, "target")
	if err != nil || changed || !granted.IsSystemAdmin {
		t.Fatalf("idempotent grant = user:%+v changed:%v err:%v", granted, changed, err)
	}
}

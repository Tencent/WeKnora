package main

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type bootstrapUserService struct {
	interfaces.UserService
	user             *types.User
	managerCount     int64
	countErr         error
	lookupErr        error
	grantAdminErr    error
	grantCrossErr    error
	activeAdminCount int64
	countAdminErr    error
	lookupCalls      int
	countAdminCalls  int
	grantAdminCalls  int
	grantCrossCalls  int
}

func (s *bootstrapUserService) CountCrossTenantAccessManagers(context.Context) (int64, error) {
	return s.managerCount, s.countErr
}

func (s *bootstrapUserService) GetUserByEmail(context.Context, string) (*types.User, error) {
	s.lookupCalls++
	return s.user, s.lookupErr
}

func (s *bootstrapUserService) CountActiveSystemAdmins(context.Context) (int64, error) {
	s.countAdminCalls++
	return s.activeAdminCount, s.countAdminErr
}

func (s *bootstrapUserService) GrantSystemAdmin(context.Context, string) (*types.User, bool, error) {
	s.grantAdminCalls++
	if s.grantAdminErr != nil {
		return nil, false, s.grantAdminErr
	}
	s.user.IsSystemAdmin = true
	return s.user, true, nil
}

func (s *bootstrapUserService) GrantCrossTenantAccess(context.Context, string) (*types.User, bool, error) {
	s.grantCrossCalls++
	if s.grantCrossErr != nil {
		return nil, false, s.grantCrossErr
	}
	s.user.CanAccessAllTenants = true
	return s.user, true, nil
}

func TestBootstrapSystemAdminEstablishesFirstCrossTenantAccessManager(t *testing.T) {
	user := &types.User{ID: "bootstrap-user", Email: "admin@example.com", IsActive: true}
	svc := &bootstrapUserService{user: user}

	bootstrapSystemAdmin(context.Background(), svc, user.Email)

	if !user.IsSystemAdmin || !user.CanAccessAllTenants {
		t.Fatalf("bootstrap user privileges = admin:%t cross-tenant:%t", user.IsSystemAdmin, user.CanAccessAllTenants)
	}
	if svc.grantAdminCalls != 1 || svc.grantCrossCalls != 1 {
		t.Fatalf("grant calls = admin:%d cross-tenant:%d", svc.grantAdminCalls, svc.grantCrossCalls)
	}
}

func TestBootstrapSystemAdminRepairsExistingAdminWithoutCrossTenantAccess(t *testing.T) {
	user := &types.User{ID: "bootstrap-user", Email: "admin@example.com", IsActive: true, IsSystemAdmin: true}
	svc := &bootstrapUserService{user: user}

	bootstrapSystemAdmin(context.Background(), svc, user.Email)

	if !user.CanAccessAllTenants || svc.grantAdminCalls != 0 || svc.grantCrossCalls != 1 {
		t.Fatalf("repair failed: user=%+v admin calls=%d cross calls=%d", user, svc.grantAdminCalls, svc.grantCrossCalls)
	}
}

func TestBootstrapSystemAdminDoesNotPromoteRegularUserWhenAdminExists(t *testing.T) {
	user := &types.User{ID: "regular-user", Email: "regular@example.com", IsActive: true}
	svc := &bootstrapUserService{user: user, activeAdminCount: 1}

	bootstrapSystemAdmin(context.Background(), svc, user.Email)

	if user.IsSystemAdmin || user.CanAccessAllTenants || svc.grantAdminCalls != 0 || svc.grantCrossCalls != 0 {
		t.Fatalf("existing admin should prevent regular-user promotion: user=%+v service=%+v", user, svc)
	}
}

func TestBootstrapSystemAdminFailsClosedWhenSystemAdminListFails(t *testing.T) {
	user := &types.User{ID: "regular-user", Email: "regular@example.com", IsActive: true}
	svc := &bootstrapUserService{user: user, countAdminErr: errors.New("database unavailable")}

	bootstrapSystemAdmin(context.Background(), svc, user.Email)

	if svc.grantAdminCalls != 0 || svc.grantCrossCalls != 0 {
		t.Fatalf("system-admin list failure should prevent writes: %+v", svc)
	}
}

func TestBootstrapSystemAdminStopsWhenManagerExists(t *testing.T) {
	svc := &bootstrapUserService{managerCount: 1}

	bootstrapSystemAdmin(context.Background(), svc, "unused@example.com")

	if svc.lookupCalls != 0 || svc.grantAdminCalls != 0 || svc.grantCrossCalls != 0 {
		t.Fatalf("existing manager should make bootstrap a no-op: %+v", svc)
	}
}

func TestBootstrapSystemAdminRejectsDisabledRecoveryTarget(t *testing.T) {
	user := &types.User{ID: "disabled-admin", Email: "disabled@example.com", IsSystemAdmin: true}
	svc := &bootstrapUserService{user: user}

	bootstrapSystemAdmin(context.Background(), svc, user.Email)

	if svc.countAdminCalls != 0 || svc.grantAdminCalls != 0 || svc.grantCrossCalls != 0 {
		t.Fatalf("disabled target should not receive privileges: %+v", svc)
	}
}

func TestBootstrapSystemAdminIgnoresDisabledExistingAdmins(t *testing.T) {
	user := &types.User{ID: "active-user", Email: "active@example.com", IsActive: true}
	svc := &bootstrapUserService{user: user, activeAdminCount: 0}

	bootstrapSystemAdmin(context.Background(), svc, user.Email)

	if !user.IsSystemAdmin || !user.CanAccessAllTenants {
		t.Fatalf("active user was not bootstrapped: %+v", user)
	}
}

func TestBootstrapSystemAdminFailsClosedWhenManagerCountFails(t *testing.T) {
	svc := &bootstrapUserService{countErr: errors.New("database unavailable")}

	bootstrapSystemAdmin(context.Background(), svc, "admin@example.com")

	if svc.lookupCalls != 0 || svc.grantAdminCalls != 0 || svc.grantCrossCalls != 0 {
		t.Fatalf("count failure should prevent privilege writes: %+v", svc)
	}
}

func TestBootstrapSystemAdminDoesNotGrantCrossTenantAccessAfterAdminGrantFailure(t *testing.T) {
	user := &types.User{ID: "bootstrap-user", Email: "admin@example.com", IsActive: true}
	svc := &bootstrapUserService{user: user, grantAdminErr: errors.New("write failed")}

	bootstrapSystemAdmin(context.Background(), svc, user.Email)

	if svc.grantCrossCalls != 0 {
		t.Fatalf("cross-tenant grant calls = %d, want 0", svc.grantCrossCalls)
	}
}

func TestBootstrapSystemAdminRetriesAfterCrossTenantGrantFailure(t *testing.T) {
	user := &types.User{ID: "bootstrap-user", Email: "admin@example.com", IsActive: true}
	svc := &bootstrapUserService{user: user, grantCrossErr: errors.New("write failed")}

	bootstrapSystemAdmin(context.Background(), svc, user.Email)
	if !user.IsSystemAdmin || user.CanAccessAllTenants {
		t.Fatalf("first attempt user privileges = admin:%t cross-tenant:%t", user.IsSystemAdmin, user.CanAccessAllTenants)
	}

	svc.grantCrossErr = nil
	bootstrapSystemAdmin(context.Background(), svc, user.Email)
	if !user.CanAccessAllTenants || svc.grantCrossCalls != 2 {
		t.Fatalf("retry failed: user=%+v cross calls=%d", user, svc.grantCrossCalls)
	}
}

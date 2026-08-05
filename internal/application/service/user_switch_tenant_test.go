package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type switchTenantMemberService struct {
	interfaces.TenantMemberService
	member   *types.TenantMember
	getCalls int
}

func (s *switchTenantMemberService) GetMembership(
	_ context.Context,
	userID string,
	tenantID uint64,
) (*types.TenantMember, error) {
	s.getCalls++
	if s.member == nil ||
		s.member.UserID != userID ||
		s.member.TenantID != tenantID {
		return nil, nil
	}
	copy := *s.member
	return &copy, nil
}

func (s *switchTenantMemberService) ListByUser(
	_ context.Context,
	userID string,
) ([]*types.TenantMember, error) {
	if s.member == nil || s.member.UserID != userID {
		return []*types.TenantMember{}, nil
	}
	copy := *s.member
	return []*types.TenantMember{&copy}, nil
}

type switchTenantService struct {
	interfaces.TenantService
}

func (s *switchTenantService) GetTenantByID(
	_ context.Context,
	tenantID uint64,
) (*types.Tenant, error) {
	return &types.Tenant{ID: tenantID, Name: "target"}, nil
}

type switchTenantTokenRepo struct {
	interfaces.AuthTokenRepository
	createCalls int
}

func (r *switchTenantTokenRepo) CreateToken(_ context.Context, _ *types.AuthToken) error {
	r.createCalls++
	return nil
}

type switchTenantUserRepo struct {
	interfaces.UserRepository
	updateCalls int
}

func (r *switchTenantUserRepo) UpdateUser(_ context.Context, _ *types.User) error {
	r.updateCalls++
	return nil
}

func switchTenantConfig(enabled bool) *config.Config {
	return &config.Config{Tenant: &config.TenantConfig{
		EnableCrossTenantAccess: enabled,
	}}
}

func newSwitchTenantService(
	enabled bool,
	member *types.TenantMember,
) (*userService, *switchTenantMemberService, *switchTenantTokenRepo) {
	memberSvc := &switchTenantMemberService{member: member}
	tokenRepo := &switchTenantTokenRepo{}
	return &userService{
		tokenRepo:     tokenRepo,
		tenantService: &switchTenantService{},
		memberService: memberSvc,
		config:        switchTenantConfig(enabled),
	}, memberSvc, tokenRepo
}

func TestSwitchTenant_CrossTenantSuperuserBlockedWhenGlobalFlagDisabled(t *testing.T) {
	svc, memberSvc, tokenRepo := newSwitchTenantService(false, nil)
	user := &types.User{ID: "super", TenantID: 1, CanAccessAllTenants: true}

	_, err := svc.SwitchTenant(context.Background(), user, 2, "")
	if !errors.Is(err, ErrMembershipNotFound) {
		t.Fatalf("SwitchTenant() error = %v, want ErrMembershipNotFound", err)
	}
	if memberSvc.getCalls != 1 {
		t.Fatalf("disabled bypass must check active membership, got %d lookups", memberSvc.getCalls)
	}
	if tokenRepo.createCalls != 0 {
		t.Fatalf("disabled bypass must not issue tokens, got %d writes", tokenRepo.createCalls)
	}
}

func TestSwitchTenant_CrossTenantSuperuserAllowedWhenGlobalFlagEnabled(t *testing.T) {
	svc, memberSvc, tokenRepo := newSwitchTenantService(true, nil)
	user := &types.User{ID: "super", TenantID: 1, CanAccessAllTenants: true}

	resp, err := svc.SwitchTenant(context.Background(), user, 2, "")
	if err != nil {
		t.Fatalf("SwitchTenant() error = %v", err)
	}
	if resp == nil || resp.ActiveTenant == nil || resp.ActiveTenant.ID != 2 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if memberSvc.getCalls != 0 {
		t.Fatalf("enabled superuser bypass should not require membership, got %d lookups", memberSvc.getCalls)
	}
	if tokenRepo.createCalls != 2 {
		t.Fatalf("token pair should be persisted, got %d token writes", tokenRepo.createCalls)
	}
}

func TestSwitchTenant_ActiveMemberAllowedWhenGlobalFlagDisabled(t *testing.T) {
	member := &types.TenantMember{
		UserID:   "super",
		TenantID: 2,
		Role:     types.TenantRoleAdmin,
		Status:   types.TenantMemberStatusActive,
	}
	svc, memberSvc, tokenRepo := newSwitchTenantService(false, member)
	user := &types.User{ID: "super", TenantID: 1, CanAccessAllTenants: true}

	resp, err := svc.SwitchTenant(context.Background(), user, 2, "")
	if err != nil {
		t.Fatalf("SwitchTenant() error = %v", err)
	}
	if resp == nil || resp.ActiveTenant == nil || resp.ActiveTenant.ID != 2 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if memberSvc.getCalls != 1 {
		t.Fatalf("member path should perform one membership lookup, got %d", memberSvc.getCalls)
	}
	if tokenRepo.createCalls != 2 {
		t.Fatalf("token pair should be persisted, got %d token writes", tokenRepo.createCalls)
	}
}

func TestResolveLoginTenantID_CrossTenantPreferenceRespectsGlobalFlag(t *testing.T) {
	for _, tc := range []struct {
		name          string
		enabled       bool
		wantTenantID  uint64
		wantCleared   bool
		wantGetCalls  int
		wantUserWrite int
	}{
		{
			name:          "disabled",
			enabled:       false,
			wantTenantID:  1,
			wantCleared:   true,
			wantGetCalls:  1,
			wantUserWrite: 1,
		},
		{
			name:          "enabled",
			enabled:       true,
			wantTenantID:  2,
			wantCleared:   false,
			wantGetCalls:  0,
			wantUserWrite: 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			preferred := uint64(2)
			user := &types.User{
				ID:                  "super",
				TenantID:            1,
				CanAccessAllTenants: true,
				Preferences: types.UserPreferences{
					LastActiveTenantID: &preferred,
				},
			}
			memberSvc := &switchTenantMemberService{}
			userRepo := &switchTenantUserRepo{}
			svc := &userService{
				userRepo:      userRepo,
				tenantService: &switchTenantService{},
				memberService: memberSvc,
				config:        switchTenantConfig(tc.enabled),
			}

			got := svc.resolveLoginTenantID(context.Background(), user)
			if got != tc.wantTenantID {
				t.Fatalf("resolveLoginTenantID() = %d, want %d", got, tc.wantTenantID)
			}
			if (user.Preferences.LastActiveTenantID == nil) != tc.wantCleared {
				t.Fatalf(
					"preference cleared = %t, want %t",
					user.Preferences.LastActiveTenantID == nil,
					tc.wantCleared,
				)
			}
			if memberSvc.getCalls != tc.wantGetCalls {
				t.Fatalf("membership lookups = %d, want %d", memberSvc.getCalls, tc.wantGetCalls)
			}
			if userRepo.updateCalls != tc.wantUserWrite {
				t.Fatalf("user updates = %d, want %d", userRepo.updateCalls, tc.wantUserWrite)
			}
		})
	}
}

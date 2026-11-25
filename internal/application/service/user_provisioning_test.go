package service

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type provisioningUserRepo struct {
	interfaces.UserRepository
	created       *types.User
	updatedTenant uint64
	listAdminErr  error
}

func (r *provisioningUserRepo) ListSystemAdmins(context.Context, int, int) ([]*types.User, int64, error) {
	return nil, 0, r.listAdminErr
}

func (r *provisioningUserRepo) GetUserByEmail(context.Context, string) (*types.User, error) {
	return nil, nil
}

func (r *provisioningUserRepo) GetUserByUsername(context.Context, string) (*types.User, error) {
	return nil, nil
}

func (r *provisioningUserRepo) CreateUser(_ context.Context, user *types.User) error {
	copy := *user
	r.created = &copy
	return nil
}

func (r *provisioningUserRepo) UpdateUser(_ context.Context, user *types.User) error {
	r.updatedTenant = user.TenantID
	return nil
}

type provisioningTenantService struct {
	interfaces.TenantService
	createCalls int
	getErrIDs   map[uint64]bool
}

func (s *provisioningTenantService) CreateTenant(context.Context, *types.Tenant) (*types.Tenant, error) {
	s.createCalls++
	return &types.Tenant{ID: 99}, nil
}

func (s *provisioningTenantService) GetTenantByID(_ context.Context, id uint64) (*types.Tenant, error) {
	if s.getErrIDs != nil && s.getErrIDs[id] {
		return nil, errors.New("tenant not found")
	}
	return &types.Tenant{ID: id}, nil
}

type provisioningMemberService struct {
	interfaces.TenantMemberService
	members  []*types.TenantMember
	addCalls []provisioningAddCall
	addErr   error
}

type provisioningAddCall struct {
	userID   string
	tenantID uint64
	role     types.TenantRole
}

func (s *provisioningMemberService) ListByUser(context.Context, string) ([]*types.TenantMember, error) {
	return s.members, nil
}

func (s *provisioningMemberService) EnsureOwner(_ context.Context, userID string, tenantID uint64) (*types.TenantMember, error) {
	return &types.TenantMember{UserID: userID, TenantID: tenantID, Role: types.TenantRoleOwner, Status: types.TenantMemberStatusActive}, nil
}

func (s *provisioningMemberService) AddMember(_ context.Context, userID string, tenantID uint64, role types.TenantRole, _ *string) (*types.TenantMember, error) {
	s.addCalls = append(s.addCalls, provisioningAddCall{userID: userID, tenantID: tenantID, role: role})
	if s.addErr != nil {
		return nil, s.addErr
	}
	return &types.TenantMember{UserID: userID, TenantID: tenantID, Role: role, Status: types.TenantMemberStatusActive}, nil
}

// stubDefaultSharedSpaceSettings serves a fixed tenant.default_shared_space_id
// value through the SystemSettingService interface.
type stubDefaultSharedSpaceSettings struct {
	interfaces.SystemSettingService
	value string
}

func (s *stubDefaultSharedSpaceSettings) GetString(context.Context, string, string, string) string {
	return s.value
}

func TestUserServiceRegisterTenantlessSkipsTenantCreation(t *testing.T) {
	repo := &provisioningUserRepo{}
	tenantSvc := &provisioningTenantService{}
	svc := &userService{userRepo: repo, tenantService: tenantSvc}

	user, err := svc.Register(context.Background(), &types.RegisterRequest{
		Username:           "alice",
		Email:              "alice@example.com",
		Password:           "supersecret",
		TenantProvisioning: types.TenantProvisioningTenantless,
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if tenantSvc.createCalls != 0 {
		t.Fatalf("tenant create calls = %d, want 0", tenantSvc.createCalls)
	}
	if user.TenantID != 0 || repo.created == nil || repo.created.TenantID != 0 {
		t.Fatalf("tenantless user persisted with tenant: user=%d created=%v", user.TenantID, repo.created)
	}
}

func TestUserServiceRegisterDefaultSharedSpace(t *testing.T) {
	ctx := context.Background()
	newSvc := func(settingsValue string, memberSvc *provisioningMemberService, tenantSvc *provisioningTenantService) *userService {
		svc := &userService{
			userRepo:      &provisioningUserRepo{},
			tenantService: tenantSvc,
			memberService: memberSvc,
		}
		if settingsValue != "" || memberSvc != nil {
			// Attach the stub whenever a member service is present so the
			// "setting unset" case is exercised through the same path.
			svc.systemSettingSvc = &stubDefaultSharedSpaceSettings{value: settingsValue}
		}
		return svc
	}
	register := func(t *testing.T, svc *userService, provisioning types.TenantProvisioningMode) *types.User {
		t.Helper()
		user, err := svc.Register(ctx, &types.RegisterRequest{
			Username:           "alice",
			Email:              "alice@example.com",
			Password:           "supersecret",
			TenantProvisioning: provisioning,
		})
		if err != nil {
			t.Fatalf("Register: %v", err)
		}
		return user
	}

	t.Run("setting unset grants nothing", func(t *testing.T) {
		memberSvc := &provisioningMemberService{}
		user := register(t, newSvc("", memberSvc, &provisioningTenantService{}), types.TenantProvisioningTenantless)
		if len(memberSvc.addCalls) != 0 {
			t.Fatalf("AddMember calls = %+v, want none", memberSvc.addCalls)
		}
		if user == nil || user.ID == "" {
			t.Fatal("expected a registered user")
		}
	})

	t.Run("setting set grants viewer membership to exactly that space", func(t *testing.T) {
		memberSvc := &provisioningMemberService{}
		user := register(t, newSvc("42", memberSvc, &provisioningTenantService{}), types.TenantProvisioningTenantless)
		if len(memberSvc.addCalls) != 1 {
			t.Fatalf("AddMember calls = %d, want 1", len(memberSvc.addCalls))
		}
		call := memberSvc.addCalls[0]
		if call.userID != user.ID || call.tenantID != 42 || call.role != types.TenantRoleViewer {
			t.Fatalf("AddMember call = %+v, want user=%s tenant=42 role=viewer", call, user.ID)
		}
	})

	t.Run("non-existent space grants nothing but registration succeeds", func(t *testing.T) {
		memberSvc := &provisioningMemberService{}
		tenantSvc := &provisioningTenantService{getErrIDs: map[uint64]bool{42: true}}
		register(t, newSvc("42", memberSvc, tenantSvc), types.TenantProvisioningTenantless)
		if len(memberSvc.addCalls) != 0 {
			t.Fatalf("AddMember calls = %+v, want none", memberSvc.addCalls)
		}
	})

	t.Run("invalid setting value grants nothing but registration succeeds", func(t *testing.T) {
		memberSvc := &provisioningMemberService{}
		register(t, newSvc("not-a-workspace-id", memberSvc, &provisioningTenantService{}), types.TenantProvisioningTenantless)
		if len(memberSvc.addCalls) != 0 {
			t.Fatalf("AddMember calls = %+v, want none", memberSvc.addCalls)
		}
	})

	t.Run("grant failure does not block registration", func(t *testing.T) {
		memberSvc := &provisioningMemberService{addErr: errors.New("db unavailable")}
		register(t, newSvc("42", memberSvc, &provisioningTenantService{}), types.TenantProvisioningTenantless)
		if len(memberSvc.addCalls) != 1 {
			t.Fatalf("AddMember calls = %d, want 1 attempted grant", len(memberSvc.addCalls))
		}
	})

	t.Run("already a member is a no-op success", func(t *testing.T) {
		memberSvc := &provisioningMemberService{addErr: ErrMembershipAlreadyExists}
		register(t, newSvc("42", memberSvc, &provisioningTenantService{}), types.TenantProvisioningTenantless)
		if len(memberSvc.addCalls) != 1 {
			t.Fatalf("AddMember calls = %d, want 1", len(memberSvc.addCalls))
		}
	})

	t.Run("default space equal to the new personal space is skipped", func(t *testing.T) {
		// provisioningTenantService.CreateTenant always returns ID 99.
		memberSvc := &provisioningMemberService{}
		user := register(t, newSvc("99", memberSvc, &provisioningTenantService{}), types.TenantProvisioningCreatePersonal)
		if user.TenantID != 99 {
			t.Fatalf("personal tenant = %d, want 99", user.TenantID)
		}
		if len(memberSvc.addCalls) != 0 {
			t.Fatalf("AddMember calls = %+v, want none (already owner)", memberSvc.addCalls)
		}
	})
}

func TestResolveLoginTenantIDRepairsTenantlessUserWithMembership(t *testing.T) {
	repo := &provisioningUserRepo{}
	tenantSvc := &provisioningTenantService{}
	memberSvc := &provisioningMemberService{members: []*types.TenantMember{
		{TenantID: 42, Status: types.TenantMemberStatusActive},
	}}
	svc := &userService{userRepo: repo, tenantService: tenantSvc, memberService: memberSvc}
	user := &types.User{ID: "alice", TenantID: 0}

	if got := svc.resolveLoginTenantID(context.Background(), user); got != 42 {
		t.Fatalf("resolved tenant = %d, want 42", got)
	}
	if repo.updatedTenant != 42 || user.TenantID != 42 {
		t.Fatalf("repair was not persisted: repo=%d user=%d", repo.updatedTenant, user.TenantID)
	}
}

// membershipLookupService lets tests control GetMembership independently
// of ListByUser so we can simulate "home tenant pointer is stale after
// RemoveMember" without also inventing residual membership rows.
type membershipLookupService struct {
	interfaces.TenantMemberService
	members  []*types.TenantMember
	byTenant map[uint64]*types.TenantMember
	listErr  error
	getErr   error
}

func (s *membershipLookupService) ListByUser(context.Context, string) ([]*types.TenantMember, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.members, nil
}

func (s *membershipLookupService) GetMembership(_ context.Context, _ string, tenantID uint64) (*types.TenantMember, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	if s.byTenant != nil {
		return s.byTenant[tenantID], nil
	}
	for _, m := range s.members {
		if m != nil && m.TenantID == tenantID {
			return m, nil
		}
	}
	return nil, nil
}

func TestHomeOrFirstMembershipTenantClearsStaleHome(t *testing.T) {
	repo := &provisioningUserRepo{}
	tenantSvc := &provisioningTenantService{}
	// Home still points at tenant 7, but the membership is gone. Another
	// active membership on tenant 42 must become the new home.
	memberSvc := &membershipLookupService{
		members: []*types.TenantMember{
			{TenantID: 42, Status: types.TenantMemberStatusActive},
		},
		byTenant: map[uint64]*types.TenantMember{},
	}
	svc := &userService{userRepo: repo, tenantService: tenantSvc, memberService: memberSvc}
	stalePref := uint64(7)
	user := &types.User{
		ID:       "alice",
		TenantID: 7,
		Preferences: types.UserPreferences{
			LastActiveTenantID: &stalePref,
		},
	}

	if got := svc.homeOrFirstMembershipTenant(context.Background(), user); got != 42 {
		t.Fatalf("resolved tenant = %d, want 42", got)
	}
	if user.TenantID != 42 {
		t.Fatalf("in-memory TenantID = %d, want 42 after repair", user.TenantID)
	}
	if user.Preferences.LastActiveTenantID != nil {
		t.Fatalf("LastActiveTenantID = %v, want nil after stale home clear", user.Preferences.LastActiveTenantID)
	}
	if repo.updatedTenant != 42 {
		t.Fatalf("persisted tenant = %d, want 42", repo.updatedTenant)
	}
}

func TestHomeOrFirstMembershipTenantKeepsValidHome(t *testing.T) {
	memberSvc := &membershipLookupService{
		byTenant: map[uint64]*types.TenantMember{
			7: {TenantID: 7, Status: types.TenantMemberStatusActive},
		},
	}
	svc := &userService{memberService: memberSvc}
	user := &types.User{ID: "alice", TenantID: 7}

	if got := svc.homeOrFirstMembershipTenant(context.Background(), user); got != 7 {
		t.Fatalf("resolved tenant = %d, want 7", got)
	}
	if user.TenantID != 7 {
		t.Fatalf("TenantID mutated to %d, want 7", user.TenantID)
	}
}

func TestBuildLoginMembershipsDoesNotSynthFromStaleHome(t *testing.T) {
	// Reproduces #2586: membership table is authoritative and empty after
	// RemoveMember, but users.tenant_id still points at the removed space.
	// The space switcher must NOT receive a synthesised membership row.
	memberSvc := &membershipLookupService{members: nil}
	svc := &userService{memberService: memberSvc}
	user := &types.User{ID: "alice", TenantID: 7}
	active := &types.Tenant{ID: 7, Name: "Removed Space"}

	got := svc.BuildLoginMemberships(context.Background(), user, active)
	if got == nil {
		t.Fatal("memberships must be non-nil (empty array contract)")
	}
	if len(got) != 0 {
		t.Fatalf("memberships = %#v, want empty (no synth from stale TenantID)", got)
	}
}

func TestBuildLoginMembershipsSynthsWhenMemberServiceMissing(t *testing.T) {
	// Partial DI graphs (tests / legacy wiring) still get the single-row
	// fallback so login responses keep a stable shape.
	svc := &userService{}
	user := &types.User{ID: "alice", TenantID: 7}
	active := &types.Tenant{ID: 7, Name: "Home"}

	got := svc.BuildLoginMemberships(context.Background(), user, active)
	if len(got) != 1 || got[0].TenantID != 7 || got[0].TenantName != "Home" {
		t.Fatalf("fallback memberships = %#v", got)
	}
}

func newBootstrapUserRepo(t *testing.T) interfaces.UserRepository {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "users.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&types.User{}); err != nil {
		t.Fatalf("migrate users: %v", err)
	}
	return apprepo.NewUserRepository(db)
}

func TestUserServiceRegisterPromotesBootstrapSystemAdmin(t *testing.T) {
	ctx := context.Background()

	t.Run("promotes matching email case-insensitively when no system admin exists", func(t *testing.T) {
		t.Setenv(BootstrapSystemAdminEmailEnv, "  Admin@Example.com ")
		repo := newBootstrapUserRepo(t)
		svc := &userService{userRepo: repo, tenantService: &provisioningTenantService{}}

		user, err := svc.Register(ctx, &types.RegisterRequest{
			Username:           "admin",
			Email:              "admin@example.com",
			Password:           "password123",
			TenantProvisioning: types.TenantProvisioningTenantless,
		})
		if err != nil {
			t.Fatalf("Register failed: %v", err)
		}
		if !user.IsSystemAdmin {
			t.Fatal("expected returned user to be promoted to system admin")
		}
		stored, err := repo.GetUserByEmail(ctx, "admin@example.com")
		if err != nil {
			t.Fatalf("GetUserByEmail: %v", err)
		}
		if !stored.IsSystemAdmin {
			t.Fatal("expected persisted user row to have is_system_admin=true")
		}
	})

	t.Run("does not promote non-matching email", func(t *testing.T) {
		t.Setenv(BootstrapSystemAdminEmailEnv, "admin@example.com")
		repo := newBootstrapUserRepo(t)
		svc := &userService{userRepo: repo, tenantService: &provisioningTenantService{}}

		user, err := svc.Register(ctx, &types.RegisterRequest{
			Username:           "alice",
			Email:              "alice@example.com",
			Password:           "password123",
			TenantProvisioning: types.TenantProvisioningTenantless,
		})
		if err != nil {
			t.Fatalf("Register failed: %v", err)
		}
		if user.IsSystemAdmin {
			t.Fatal("non-matching email should not be promoted")
		}
	})

	t.Run("does not promote when a system admin already exists", func(t *testing.T) {
		t.Setenv(BootstrapSystemAdminEmailEnv, "second-admin@example.com")
		repo := newBootstrapUserRepo(t)
		if err := repo.CreateUser(ctx, &types.User{
			ID:            "existing-admin",
			Username:      "existing",
			Email:         "existing@example.com",
			PasswordHash:  "hash",
			IsActive:      true,
			IsSystemAdmin: true,
		}); err != nil {
			t.Fatalf("seed existing admin: %v", err)
		}
		svc := &userService{userRepo: repo, tenantService: &provisioningTenantService{}}

		user, err := svc.Register(ctx, &types.RegisterRequest{
			Username:           "second",
			Email:              "second-admin@example.com",
			Password:           "password123",
			TenantProvisioning: types.TenantProvisioningTenantless,
		})
		if err != nil {
			t.Fatalf("Register failed: %v", err)
		}
		if user.IsSystemAdmin {
			t.Fatal("matching email must not be promoted when an admin already exists")
		}
	})

	t.Run("fails closed when ListSystemAdmins returns an error", func(t *testing.T) {
		t.Setenv(BootstrapSystemAdminEmailEnv, "admin@example.com")
		repo := &provisioningUserRepo{listAdminErr: errors.New("db unavailable")}
		svc := &userService{userRepo: repo, tenantService: &provisioningTenantService{}}

		user, err := svc.Register(ctx, &types.RegisterRequest{
			Username:           "admin",
			Email:              "admin@example.com",
			Password:           "password123",
			TenantProvisioning: types.TenantProvisioningTenantless,
		})
		if err != nil {
			t.Fatalf("Register should still succeed on best-effort bootstrap error: %v", err)
		}
		if user.IsSystemAdmin {
			t.Fatal("bootstrap check error must fail closed without granting admin")
		}
	})
}

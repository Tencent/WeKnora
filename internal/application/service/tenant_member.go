package service

import (
	"context"
	"errors"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// Sentinel errors returned by tenantMemberService. Callers compare with
// errors.Is to render appropriate HTTP responses (404 / 409 / 403).
var (
	// ErrMembershipNotFound is returned when no active membership row
	// matches the (user, tenant) pair the caller requested.
	ErrMembershipNotFound = errors.New("tenant membership not found")

	// ErrMembershipAlreadyExists is returned by AddMember when the
	// (user, tenant) pair already has an active membership.
	ErrMembershipAlreadyExists = errors.New("tenant membership already exists")

	// ErrInvalidTenantRole is returned when the caller passes a role
	// value that is not one of the four defined TenantRole constants.
	ErrInvalidTenantRole = errors.New("invalid tenant role")

	// ErrLastOwner is returned when an operation would leave the tenant
	// without an active Owner. Demoting the last Owner or removing them
	// is forbidden; an explicit ownership transfer must happen first.
	ErrLastOwner = errors.New("cannot demote or remove the last active owner of the tenant")
)

// tenantMemberService implements interfaces.TenantMemberService.
type tenantMemberService struct {
	repo interfaces.TenantMemberRepository
}

// NewTenantMemberService constructs the service. Wired up via the DI
// container alongside the other application services.
func NewTenantMemberService(repo interfaces.TenantMemberRepository) interfaces.TenantMemberService {
	return &tenantMemberService{repo: repo}
}

// AddMember inserts a new active membership row. Returns
// ErrMembershipAlreadyExists if the user is already an active member of
// the tenant, and ErrInvalidTenantRole for unknown roles.
func (s *tenantMemberService) AddMember(
	ctx context.Context,
	userID string,
	tenantID uint64,
	role types.TenantRole,
	invitedBy *string,
) (*types.TenantMember, error) {
	if !role.IsValid() {
		return nil, ErrInvalidTenantRole
	}
	existing, err := s.repo.Get(ctx, userID, tenantID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, ErrMembershipAlreadyExists
	}
	member := &types.TenantMember{
		UserID:    userID,
		TenantID:  tenantID,
		Role:      role,
		Status:    types.TenantMemberStatusActive,
		InvitedBy: invitedBy,
		JoinedAt:  time.Now(),
	}
	if err := s.repo.Create(ctx, member); err != nil {
		return nil, err
	}
	return member, nil
}

// EnsureOwner is idempotent: if the user already has an active membership
// in the tenant it is returned unchanged; otherwise a new owner row is
// created. Used by Register/OIDC paths so re-running Register on an
// existing user (e.g. after a partial failure) does not double-insert.
func (s *tenantMemberService) EnsureOwner(
	ctx context.Context,
	userID string,
	tenantID uint64,
) (*types.TenantMember, error) {
	existing, err := s.repo.Get(ctx, userID, tenantID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}
	member := &types.TenantMember{
		UserID:   userID,
		TenantID: tenantID,
		Role:     types.TenantRoleOwner,
		Status:   types.TenantMemberStatusActive,
		JoinedAt: time.Now(),
	}
	if err := s.repo.Create(ctx, member); err != nil {
		return nil, err
	}
	logger.Infof(ctx, "Bootstrapped owner membership for user=%s tenant=%d", userID, tenantID)
	return member, nil
}

// GetMembership returns the active membership or (nil, nil) when absent.
func (s *tenantMemberService) GetMembership(
	ctx context.Context,
	userID string,
	tenantID uint64,
) (*types.TenantMember, error) {
	return s.repo.Get(ctx, userID, tenantID)
}

// ListByUser proxies to the repository; included on the service so HTTP
// handlers depend only on the service interface.
func (s *tenantMemberService) ListByUser(ctx context.Context, userID string) ([]*types.TenantMember, error) {
	return s.repo.ListByUser(ctx, userID)
}

// ListByTenant proxies to the repository.
func (s *tenantMemberService) ListByTenant(ctx context.Context, tenantID uint64) ([]*types.TenantMember, error) {
	return s.repo.ListByTenant(ctx, tenantID)
}

// HasAnyMembers proxies to the repository.
func (s *tenantMemberService) HasAnyMembers(ctx context.Context, tenantID uint64) (bool, error) {
	return s.repo.HasAnyMembers(ctx, tenantID)
}

// UpdateRole enforces the "cannot demote the last Owner" invariant before
// delegating to the repository. Re-promoting an existing Owner is a no-op
// from the invariant's perspective.
func (s *tenantMemberService) UpdateRole(
	ctx context.Context,
	userID string,
	tenantID uint64,
	newRole types.TenantRole,
) error {
	if !newRole.IsValid() {
		return ErrInvalidTenantRole
	}
	current, err := s.repo.Get(ctx, userID, tenantID)
	if err != nil {
		return err
	}
	if current == nil {
		return ErrMembershipNotFound
	}
	if current.Role == newRole {
		return nil
	}
	// Owner demotion is the dangerous path: two concurrent demotions of
	// two different Owners with the old "Get → Count → Update" sequence
	// could each observe count=2 and both commit, leaving the tenant
	// ownerless. Route through the repo's atomic helper instead, which
	// takes a row-level UPDATE lock on every other active Owner before
	// committing the role change.
	if current.Role == types.TenantRoleOwner && newRole != types.TenantRoleOwner {
		err := s.repo.DemoteOwnerAtomically(ctx, userID, tenantID, newRole)
		switch {
		case errors.Is(err, apprepo.ErrLastOwner):
			return ErrLastOwner
		case err != nil:
			return err
		}
		return nil
	}
	return s.repo.UpdateRole(ctx, userID, tenantID, newRole)
}

// RemoveMember enforces the "cannot remove the last Owner" invariant
// before soft-deleting the membership. For Owner removals it routes
// through the repo's transactional helper so the count + delete commit
// atomically (no TOCTOU between checking owner count and deleting).
func (s *tenantMemberService) RemoveMember(ctx context.Context, userID string, tenantID uint64) error {
	current, err := s.repo.Get(ctx, userID, tenantID)
	if err != nil {
		return err
	}
	if current == nil {
		return ErrMembershipNotFound
	}
	if current.Role == types.TenantRoleOwner {
		err := s.repo.RemoveOwnerAtomically(ctx, userID, tenantID)
		switch {
		case errors.Is(err, apprepo.ErrLastOwner):
			return ErrLastOwner
		case err != nil:
			return err
		}
		return nil
	}
	return s.repo.SoftDelete(ctx, userID, tenantID)
}

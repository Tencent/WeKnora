package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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

const (
	listMembersDefaultPageSize = 20
	listMembersMaxPageSize     = 100
)

// tenantMemberService implements interfaces.TenantMemberService.
type tenantMemberService struct {
	repo  interfaces.TenantMemberRepository
	audit interfaces.AuditLogService // optional; nil ⇒ no audit, business ops still succeed
}

// NewTenantMemberService constructs the service. Wired up via the DI
// container alongside the other application services. The auditService
// is optional — passing nil disables durable audit but keeps the
// underlying mutations working, so a container reshuffle that
// constructs tenant_member before audit_log won't crash and tests
// don't need to stub the dependency unless they care about audit
// behaviour.
func NewTenantMemberService(
	repo interfaces.TenantMemberRepository,
	audit interfaces.AuditLogService,
) interfaces.TenantMemberService {
	return &tenantMemberService{repo: repo, audit: audit}
}

// emitAudit is the per-mutation audit hook. Best-effort: a nil audit
// service or a write failure is logged inside the audit service itself
// and never bubbles up to the caller. RBAC mutations succeed even if
// audit is unavailable; the alternative (failing the business op when
// the audit table is down) is far worse.
func (s *tenantMemberService) emitAudit(ctx context.Context, entry *types.AuditLog) {
	if s.audit == nil {
		return
	}
	_ = s.audit.Log(ctx, entry)
}

// auditActorRole picks up the caller's role at write-time. Empty if
// auth middleware didn't set it (e.g. service-internal flows like
// EnsureOwner during register, where there is no "caller").
func auditActorRole(ctx context.Context) string {
	return string(types.TenantRoleFromContext(ctx))
}

// auditActor returns the calling user id from context, "" when no
// authenticated caller is present (service-internal paths).
func auditActor(ctx context.Context) string {
	uid, _ := types.UserIDFromContext(ctx)
	return uid
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
	s.emitAudit(ctx, &types.AuditLog{
		TenantID:     tenantID,
		ActorUserID:  auditActor(ctx),
		ActorRole:    auditActorRole(ctx),
		Action:       types.AuditActionMemberAdded,
		TargetType:   "tenant_member",
		TargetUserID: userID,
		Outcome:      types.AuditOutcomeSuccess,
	})
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

// ListMembersPage returns a slice plus total matching query (handlers parse
// page/page_size; defensive clamps here mirror list handler limits).
func (s *tenantMemberService) ListMembersPage(
	ctx context.Context,
	tenantID uint64,
	query string,
	page, pageSize int,
) ([]*types.TenantMember, int64, error) {
	query = strings.TrimSpace(query)
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = listMembersDefaultPageSize
	}
	if pageSize > listMembersMaxPageSize {
		pageSize = listMembersMaxPageSize
	}
	total, err := s.repo.CountFilteredByTenant(ctx, tenantID, query)
	if err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	members, err := s.repo.ListPagedByTenant(ctx, tenantID, query, offset, pageSize)
	if err != nil {
		return nil, 0, err
	}
	return members, total, nil
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
	oldRole := current.Role
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
		s.emitRoleChangeAudit(ctx, tenantID, userID, oldRole, newRole)
		return nil
	}
	if err := s.repo.UpdateRole(ctx, userID, tenantID, newRole); err != nil {
		return err
	}
	s.emitRoleChangeAudit(ctx, tenantID, userID, oldRole, newRole)
	return nil
}

// emitRoleChangeAudit packs the old/new role into Details so the
// audit-log UI can render "promoted Alice from contributor to admin"
// without a separate column per role transition.
func (s *tenantMemberService) emitRoleChangeAudit(
	ctx context.Context,
	tenantID uint64,
	targetUserID string,
	oldRole, newRole types.TenantRole,
) {
	details, _ := json.Marshal(map[string]string{
		"old_role": string(oldRole),
		"new_role": string(newRole),
	})
	s.emitAudit(ctx, &types.AuditLog{
		TenantID:     tenantID,
		ActorUserID:  auditActor(ctx),
		ActorRole:    auditActorRole(ctx),
		Action:       types.AuditActionMemberRoleChanged,
		TargetType:   "tenant_member",
		TargetUserID: targetUserID,
		Outcome:      types.AuditOutcomeSuccess,
		Details:      types.JSON(details),
	})
}

// RemoveMember enforces the "cannot remove the last Owner" invariant
// before soft-deleting the membership. For Owner removals it routes
// through the repo's transactional helper so the count + delete commit
// atomically (no TOCTOU between checking owner count and deleting).
//
// The audit row distinguishes "voluntary leave" (caller == target,
// driven by POST /leave) from "kicked" (caller != target, driven by
// DELETE /tenants/:id/members/:user_id). Both go through this same
// service method but the recorded action differs so an audit reader
// can tell the two apart.
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
		s.emitRemovalAudit(ctx, tenantID, userID)
		return nil
	}
	if err := s.repo.SoftDelete(ctx, userID, tenantID); err != nil {
		return err
	}
	s.emitRemovalAudit(ctx, tenantID, userID)
	return nil
}

// emitRemovalAudit picks AuditActionMemberLeft when the caller is
// removing themselves, AuditActionMemberRemoved otherwise. Caller
// detection uses the user-id from the request context — the same
// source the LeaveTenant handler uses to derive its `userID` arg.
func (s *tenantMemberService) emitRemovalAudit(
	ctx context.Context,
	tenantID uint64,
	targetUserID string,
) {
	action := types.AuditActionMemberRemoved
	if actor := auditActor(ctx); actor != "" && actor == targetUserID {
		action = types.AuditActionMemberLeft
	}
	s.emitAudit(ctx, &types.AuditLog{
		TenantID:     tenantID,
		ActorUserID:  auditActor(ctx),
		ActorRole:    auditActorRole(ctx),
		Action:       action,
		TargetType:   "tenant_member",
		TargetUserID: targetUserID,
		Outcome:      types.AuditOutcomeSuccess,
	})
}

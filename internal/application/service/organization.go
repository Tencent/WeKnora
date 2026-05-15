package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

// Default invite code validity in days; allowed values: 0 (never), 1, 7, 30
const DefaultInviteCodeValidityDays = 7

// DefaultMemberLimit is the default max tenant-members per organization (0 = unlimited)
const DefaultMemberLimit = 200

// ValidInviteCodeValidityDays are the allowed values for invite_code_validity_days
var ValidInviteCodeValidityDays = map[int]bool{0: true, 1: true, 7: true, 30: true}

var (
	ErrOrgNotFound           = errors.New("organization not found")
	ErrOrgPermissionDenied   = errors.New("permission denied for this organization")
	ErrCannotRemoveOwner     = errors.New("cannot remove organization owner tenant")
	ErrCannotChangeOwnerRole = errors.New("cannot change organization owner tenant role")
	ErrTenantNotInOrg        = errors.New("tenant is not a member of this organization")
	ErrInvalidRole           = errors.New("invalid role")
	ErrInviteCodeExpired     = errors.New("invite code has expired")
	ErrInvalidValidityDays   = errors.New("invite_code_validity_days must be 0, 1, 7, or 30")
	ErrOrgMemberLimitReached = errors.New("organization member limit reached")
	ErrOrgMemberLimitTooLow  = errors.New("member limit cannot be lower than current member count")
)

// organizationService implements OrganizationService.
//
// Plan 3 of #1303: a "member" of an org is a tenant, identified by
// (org_id, tenant_id). The user_id of the requester is recorded only as
// the representative for UI/audit; permission decisions ride on the
// tenant's role inside the org.
type organizationService struct {
	orgRepo        interfaces.OrganizationRepository
	userRepo       interfaces.UserRepository
	shareRepo      interfaces.KBShareRepository
	agentShareRepo interfaces.AgentShareRepository
}

// NewOrganizationService creates a new organization service
func NewOrganizationService(
	orgRepo interfaces.OrganizationRepository,
	userRepo interfaces.UserRepository,
	shareRepo interfaces.KBShareRepository,
	agentShareRepo interfaces.AgentShareRepository,
) interfaces.OrganizationService {
	return &organizationService{
		orgRepo:        orgRepo,
		userRepo:       userRepo,
		shareRepo:      shareRepo,
		agentShareRepo: agentShareRepo,
	}
}

// resolveInviteExpiry returns expiresAt for the given validity days (0 = never, nil expiresAt).
func resolveInviteExpiry(validityDays int, now time.Time) *time.Time {
	if validityDays == 0 {
		return nil
	}
	t := now.AddDate(0, 0, validityDays)
	return &t
}

// CreateOrganization creates a new organization. The creator's tenant
// is enrolled at admin role and userID is recorded as the representative.
func (s *organizationService) CreateOrganization(ctx context.Context, userID string, tenantID uint64, req *types.CreateOrganizationRequest) (*types.Organization, error) {
	logger.Infof(ctx, "Creating organization: %s by user: %s in tenant: %d", req.Name, userID, tenantID)

	validityDays := DefaultInviteCodeValidityDays
	if req.InviteCodeValidityDays != nil {
		if !ValidInviteCodeValidityDays[*req.InviteCodeValidityDays] {
			return nil, ErrInvalidValidityDays
		}
		validityDays = *req.InviteCodeValidityDays
	}
	memberLimit := DefaultMemberLimit
	if req.MemberLimit != nil {
		if *req.MemberLimit < 0 {
			return nil, errors.New("member_limit must be >= 0")
		}
		memberLimit = *req.MemberLimit
	}

	now := time.Now()
	org := &types.Organization{
		ID:                     uuid.New().String(),
		Name:                   req.Name,
		Description:            req.Description,
		Avatar:                 strings.TrimSpace(req.Avatar),
		OwnerID:                userID,
		InviteCode:             generateInviteCode(),
		InviteCodeExpiresAt:    resolveInviteExpiry(validityDays, now),
		InviteCodeValidityDays: validityDays,
		MemberLimit:            memberLimit,
		CreatedAt:              now,
		UpdatedAt:              now,
	}

	if err := s.orgRepo.Create(ctx, org); err != nil {
		logger.Errorf(ctx, "Failed to create organization: %v", err)
		return nil, err
	}

	// Enrol the creator's tenant as admin. Owner tenant is encoded by
	// (Organization.OwnerID points to a user, the user's tenant is the
	// owning tenant); the membership row gives that tenant the same
	// role plumbing as everyone else for join/leave/role-change UX.
	joinedAt := now
	member := &types.OrganizationTenantMember{
		ID:                   uuid.New().String(),
		OrganizationID:       org.ID,
		TenantID:             tenantID,
		Role:                 types.OrgRoleAdmin,
		RepresentativeUserID: userID,
		JoinedAt:             &joinedAt,
		CreatedAt:            now,
		UpdatedAt:            now,
	}

	if err := s.orgRepo.AddTenantMember(ctx, member); err != nil {
		logger.Errorf(ctx, "Failed to add creator tenant as member: %v", err)
		// Rollback organization creation
		_ = s.orgRepo.Delete(ctx, org.ID)
		return nil, err
	}

	logger.Infof(ctx, "Organization created successfully: %s", org.ID)
	return org, nil
}

// GetOrganization gets an organization by ID
func (s *organizationService) GetOrganization(ctx context.Context, id string) (*types.Organization, error) {
	org, err := s.orgRepo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrOrganizationNotFound) {
			return nil, ErrOrgNotFound
		}
		return nil, err
	}
	return org, nil
}

// GetOrganizationByInviteCode gets an organization by invite code
func (s *organizationService) GetOrganizationByInviteCode(ctx context.Context, inviteCode string) (*types.Organization, error) {
	org, err := s.orgRepo.GetByInviteCode(ctx, inviteCode)
	if err != nil {
		if errors.Is(err, repository.ErrInviteCodeNotFound) {
			return nil, ErrOrgNotFound
		}
		if errors.Is(err, repository.ErrInviteCodeExpired) {
			return nil, ErrInviteCodeExpired
		}
		return nil, err
	}
	return org, nil
}

// ListTenantOrganizations lists all organizations the tenant participates in.
func (s *organizationService) ListTenantOrganizations(ctx context.Context, tenantID uint64) ([]*types.Organization, error) {
	return s.orgRepo.ListByTenantID(ctx, tenantID)
}

// UpdateOrganization updates an organization. Operator's tenant must be
// admin in this org; the operator user identity is logged for audit but
// not used as the permission key.
func (s *organizationService) UpdateOrganization(ctx context.Context, id string, userID string, tenantID uint64, req *types.UpdateOrganizationRequest) (*types.Organization, error) {
	isAdmin, err := s.IsTenantOrgAdmin(ctx, id, tenantID)
	if err != nil {
		return nil, err
	}
	if !isAdmin {
		return nil, ErrOrgPermissionDenied
	}

	org, err := s.orgRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if req.Name != nil {
		org.Name = *req.Name
	}
	if req.Description != nil {
		org.Description = *req.Description
	}
	if req.Avatar != nil {
		org.Avatar = strings.TrimSpace(*req.Avatar)
	}
	if req.RequireApproval != nil {
		org.RequireApproval = *req.RequireApproval
	}
	if req.Searchable != nil {
		org.Searchable = *req.Searchable
	}
	if req.InviteCodeValidityDays != nil {
		if !ValidInviteCodeValidityDays[*req.InviteCodeValidityDays] {
			return nil, ErrInvalidValidityDays
		}
		org.InviteCodeValidityDays = *req.InviteCodeValidityDays
	}
	if req.MemberLimit != nil {
		if *req.MemberLimit < 0 {
			return nil, errors.New("member_limit must be >= 0")
		}
		if *req.MemberLimit > 0 {
			count, err := s.orgRepo.CountTenantMembers(ctx, id)
			if err != nil {
				return nil, err
			}
			if int64(*req.MemberLimit) < count {
				return nil, ErrOrgMemberLimitTooLow
			}
		}
		org.MemberLimit = *req.MemberLimit
	}
	org.UpdatedAt = time.Now()

	_ = userID // recorded by the caller for audit; not used here
	if err := s.orgRepo.Update(ctx, org); err != nil {
		return nil, err
	}

	return org, nil
}

// SearchSearchableOrganizations returns searchable (discoverable) organizations
// keyed on the caller's tenant — IsAlreadyMember reflects tenant-level membership.
func (s *organizationService) SearchSearchableOrganizations(ctx context.Context, tenantID uint64, query string, limit int) (*types.ListSearchableOrganizationsResponse, error) {
	if limit <= 0 {
		limit = 20
	}
	orgs, err := s.orgRepo.ListSearchable(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	memberCounts := make(map[string]int64)
	shareCounts := make(map[string]int64)
	agentShareCounts := make(map[string]int)
	memberOrgIDs := make(map[string]bool)
	for _, org := range orgs {
		if mc, err := s.orgRepo.CountTenantMembers(ctx, org.ID); err == nil {
			memberCounts[org.ID] = mc
		}
		shares, _ := s.shareRepo.ListByOrganization(ctx, org.ID)
		shareCounts[org.ID] = int64(len(shares))
		if agentShares, err := s.agentShareRepo.ListByOrganization(ctx, org.ID); err == nil {
			agentShareCounts[org.ID] = len(agentShares)
		}
		_, err := s.orgRepo.GetTenantMember(ctx, org.ID, tenantID)
		memberOrgIDs[org.ID] = (err == nil)
	}
	items := make([]types.SearchableOrganizationItem, 0, len(orgs))
	for _, org := range orgs {
		items = append(items, types.SearchableOrganizationItem{
			ID:              org.ID,
			Name:            org.Name,
			Description:     org.Description,
			Avatar:          org.Avatar,
			MemberCount:     int(memberCounts[org.ID]),
			MemberLimit:     org.MemberLimit,
			ShareCount:      int(shareCounts[org.ID]),
			AgentShareCount: agentShareCounts[org.ID],
			IsAlreadyMember: memberOrgIDs[org.ID],
			RequireApproval: org.RequireApproval,
		})
	}
	return &types.ListSearchableOrganizationsResponse{
		Organizations: items,
		Total:         int64(len(items)),
	}, nil
}

// JoinByOrganizationID joins a searchable organization by ID (no invite
// code required). Plan 3: the *tenant* joins, with the calling user as
// representative.
func (s *organizationService) JoinByOrganizationID(ctx context.Context, orgID string, userID string, tenantID uint64, message string, requestedRole types.OrgMemberRole) (*types.Organization, error) {
	org, err := s.orgRepo.GetByID(ctx, orgID)
	if err != nil {
		if errors.Is(err, repository.ErrOrganizationNotFound) {
			return nil, ErrOrgNotFound
		}
		return nil, err
	}
	if !org.Searchable {
		return nil, ErrOrgPermissionDenied
	}
	_, err = s.orgRepo.GetTenantMember(ctx, orgID, tenantID)
	if err == nil {
		return org, nil // tenant already member; idempotent
	}
	if requestedRole != "" && !requestedRole.IsValid() {
		return nil, ErrInvalidRole
	}
	if requestedRole == "" {
		requestedRole = types.OrgRoleViewer
	}
	if org.RequireApproval {
		_, err = s.SubmitJoinRequest(ctx, orgID, userID, tenantID, message, requestedRole)
		if err != nil {
			return nil, err
		}
		return org, nil
	}
	if err := s.joinAsViewerWithChecks(ctx, org, userID, tenantID); err != nil {
		return nil, err
	}
	logger.Infof(ctx, "Tenant %d joined organization %s via searchable join (rep user %s)", tenantID, org.ID, userID)
	return org, nil
}

// DeleteOrganization deletes an organization. Only the owner user may delete.
// (We keep the user-level check here intentionally — Org ownership is
// product-defined as the user who created it, not the tenant they were
// in at the time.)
func (s *organizationService) DeleteOrganization(ctx context.Context, id string, userID string, tenantID uint64) error {
	org, err := s.orgRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}

	if org.OwnerID != userID {
		return ErrOrgPermissionDenied
	}
	_ = tenantID // accepted for handler symmetry; not part of the gate

	if err := s.shareRepo.DeleteByOrganizationID(ctx, id); err != nil {
		logger.Warnf(ctx, "Failed to delete KB shares for organization %s: %v", id, err)
	}
	if err := s.agentShareRepo.DeleteByOrganizationID(ctx, id); err != nil {
		logger.Warnf(ctx, "Failed to delete agent shares for organization %s: %v", id, err)
	}

	return s.orgRepo.Delete(ctx, id)
}

// AddTenantMember enrols a tenant as a member of an organization.
func (s *organizationService) AddTenantMember(ctx context.Context, orgID string, tenantID uint64, representativeUserID string, role types.OrgMemberRole) error {
	if !role.IsValid() {
		return ErrInvalidRole
	}

	org, err := s.orgRepo.GetByID(ctx, orgID)
	if err != nil {
		return err
	}
	if org.MemberLimit > 0 {
		count, errCount := s.orgRepo.CountTenantMembers(ctx, orgID)
		if errCount != nil {
			return errCount
		}
		if count >= int64(org.MemberLimit) {
			return ErrOrgMemberLimitReached
		}
	}

	now := time.Now()
	member := &types.OrganizationTenantMember{
		ID:                   uuid.New().String(),
		OrganizationID:       orgID,
		TenantID:             tenantID,
		Role:                 role,
		RepresentativeUserID: representativeUserID,
		JoinedAt:             &now,
		CreatedAt:            now,
		UpdatedAt:            now,
	}

	return s.orgRepo.AddTenantMember(ctx, member)
}

// RemoveTenantMember removes a tenant from an organization.
// When operatorTenantID == memberTenantID, it's "leave" (self-removal).
// Otherwise the operator's tenant must be admin in the org.
func (s *organizationService) RemoveTenantMember(ctx context.Context, orgID string, memberTenantID uint64, operatorUserID string, operatorTenantID uint64) error {
	org, err := s.orgRepo.GetByID(ctx, orgID)
	if err != nil {
		return err
	}
	// Owner-tenant resolution: derive owner tenant from Organization.OwnerID's
	// own tenant. If the OwnerID-user has been deleted (very rare during the
	// org's lifetime) we fall through and refuse the destructive action.
	if isOwnerTenant, err := s.isOwnerTenant(ctx, org, memberTenantID); err == nil && isOwnerTenant {
		return ErrCannotRemoveOwner
	}

	if operatorTenantID == memberTenantID {
		// Self-removal: any tenant can leave on their own behalf.
		return s.orgRepo.RemoveTenantMember(ctx, orgID, memberTenantID)
	}

	isAdmin, err := s.IsTenantOrgAdmin(ctx, orgID, operatorTenantID)
	if err != nil {
		return err
	}
	if !isAdmin {
		return ErrOrgPermissionDenied
	}
	_ = operatorUserID

	return s.orgRepo.RemoveTenantMember(ctx, orgID, memberTenantID)
}

// UpdateTenantMemberRole updates the role for a (org, tenant) membership.
func (s *organizationService) UpdateTenantMemberRole(ctx context.Context, orgID string, memberTenantID uint64, role types.OrgMemberRole, operatorUserID string, operatorTenantID uint64) error {
	if !role.IsValid() {
		return ErrInvalidRole
	}

	isAdmin, err := s.IsTenantOrgAdmin(ctx, orgID, operatorTenantID)
	if err != nil {
		return err
	}
	if !isAdmin {
		return ErrOrgPermissionDenied
	}

	org, err := s.orgRepo.GetByID(ctx, orgID)
	if err != nil {
		return err
	}
	if isOwnerTenant, err := s.isOwnerTenant(ctx, org, memberTenantID); err == nil && isOwnerTenant {
		return ErrCannotChangeOwnerRole
	}
	_ = operatorUserID

	return s.orgRepo.UpdateTenantMemberRole(ctx, orgID, memberTenantID, role)
}

// ListTenantMembers lists all tenant memberships for an organization.
func (s *organizationService) ListTenantMembers(ctx context.Context, orgID string) ([]*types.OrganizationTenantMember, error) {
	return s.orgRepo.ListTenantMembers(ctx, orgID)
}

// GetTenantMember returns the (org, tenant) membership row.
func (s *organizationService) GetTenantMember(ctx context.Context, orgID string, tenantID uint64) (*types.OrganizationTenantMember, error) {
	member, err := s.orgRepo.GetTenantMember(ctx, orgID, tenantID)
	if err != nil {
		if errors.Is(err, repository.ErrOrgMemberNotFound) {
			return nil, ErrTenantNotInOrg
		}
		return nil, err
	}
	return member, nil
}

// GenerateInviteCode generates a new invite code for an organization.
// Operator's tenant must be admin in the org.
func (s *organizationService) GenerateInviteCode(ctx context.Context, orgID string, userID string, tenantID uint64) (string, error) {
	isAdmin, err := s.IsTenantOrgAdmin(ctx, orgID, tenantID)
	if err != nil {
		return "", err
	}
	if !isAdmin {
		return "", ErrOrgPermissionDenied
	}

	org, err := s.orgRepo.GetByID(ctx, orgID)
	if err != nil {
		return "", err
	}

	validityDays := org.InviteCodeValidityDays
	if validityDays != 0 && !ValidInviteCodeValidityDays[validityDays] {
		validityDays = DefaultInviteCodeValidityDays
	}

	inviteCode := generateInviteCode()
	now := time.Now()
	expiresAt := resolveInviteExpiry(validityDays, now)
	if err := s.orgRepo.UpdateInviteCode(ctx, orgID, inviteCode, expiresAt); err != nil {
		return "", err
	}
	_ = userID

	return inviteCode, nil
}

// joinAsViewerWithChecks adds the tenant as viewer when not already a
// member (enforces member limit). representativeUserID is the user
// kicking off the join; recorded for audit only.
func (s *organizationService) joinAsViewerWithChecks(ctx context.Context, org *types.Organization, representativeUserID string, tenantID uint64) error {
	_, err := s.orgRepo.GetTenantMember(ctx, org.ID, tenantID)
	if err == nil {
		return nil
	}
	if !errors.Is(err, repository.ErrOrgMemberNotFound) {
		return err
	}

	if org.MemberLimit > 0 {
		count, errCount := s.orgRepo.CountTenantMembers(ctx, org.ID)
		if errCount != nil {
			return errCount
		}
		if count >= int64(org.MemberLimit) {
			return ErrOrgMemberLimitReached
		}
	}

	now := time.Now()
	member := &types.OrganizationTenantMember{
		ID:                   uuid.New().String(),
		OrganizationID:       org.ID,
		TenantID:             tenantID,
		Role:                 types.OrgRoleViewer,
		RepresentativeUserID: representativeUserID,
		JoinedAt:             &now,
		CreatedAt:            now,
		UpdatedAt:            now,
	}

	return s.orgRepo.AddTenantMember(ctx, member)
}

// JoinByInviteCode allows a tenant to join via invite code.
func (s *organizationService) JoinByInviteCode(ctx context.Context, inviteCode string, userID string, tenantID uint64) (*types.Organization, error) {
	org, err := s.orgRepo.GetByInviteCode(ctx, inviteCode)
	if err != nil {
		if errors.Is(err, repository.ErrInviteCodeNotFound) {
			return nil, ErrOrgNotFound
		}
		if errors.Is(err, repository.ErrInviteCodeExpired) {
			return nil, ErrInviteCodeExpired
		}
		return nil, err
	}

	if org.RequireApproval {
		logger.Infof(ctx, "Organization %s requires approval", org.ID)
		return nil, ErrOrgPermissionDenied
	}

	if err := s.joinAsViewerWithChecks(ctx, org, userID, tenantID); err != nil {
		return nil, err
	}

	logger.Infof(ctx, "Tenant %d joined organization %s via invite code (rep user %s)", tenantID, org.ID, userID)
	return org, nil
}

// IsTenantOrgAdmin reports whether the tenant has admin role in the org.
func (s *organizationService) IsTenantOrgAdmin(ctx context.Context, orgID string, tenantID uint64) (bool, error) {
	member, err := s.orgRepo.GetTenantMember(ctx, orgID, tenantID)
	if err != nil {
		if errors.Is(err, repository.ErrOrgMemberNotFound) {
			return false, nil
		}
		return false, err
	}
	return member.Role == types.OrgRoleAdmin, nil
}

// GetTenantRoleInOrg gets a tenant's role in an organization.
func (s *organizationService) GetTenantRoleInOrg(ctx context.Context, orgID string, tenantID uint64) (types.OrgMemberRole, error) {
	member, err := s.orgRepo.GetTenantMember(ctx, orgID, tenantID)
	if err != nil {
		if errors.Is(err, repository.ErrOrgMemberNotFound) {
			return "", ErrTenantNotInOrg
		}
		return "", err
	}
	return member.Role, nil
}

// isOwnerTenant returns true when the given tenant is the home tenant of
// org.OwnerID. The owner tenant is undeletable / unchangeable in the
// org membership table — that lets us preserve the "owner can never be
// orphaned" invariant without adding a separate column.
func (s *organizationService) isOwnerTenant(ctx context.Context, org *types.Organization, tenantID uint64) (bool, error) {
	if s.userRepo == nil || org == nil {
		return false, nil
	}
	owner, err := s.userRepo.GetUserByID(ctx, org.OwnerID)
	if err != nil || owner == nil {
		return false, err
	}
	return owner.TenantID == tenantID, nil
}

// generateInviteCode generates a random 16-character invite code
func generateInviteCode() string {
	bytes := make([]byte, 8)
	_, _ = rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

// ----------------
// Join Requests
// ----------------

var (
	ErrPendingRequestExists    = errors.New("pending request already exists")
	ErrJoinRequestNotFound     = errors.New("join request not found")
	ErrCannotUpgradeToSameRole = errors.New("cannot request upgrade to same or lower role")
	ErrAlreadyAdmin            = errors.New("tenant is already an admin")
)

// SubmitJoinRequest submits a request for the caller's tenant to join an organization.
// Dedup is now per-tenant: any user from a tenant already with a pending join
// request is rejected (the same tenant can't queue two simultaneous joins).
func (s *organizationService) SubmitJoinRequest(ctx context.Context, orgID string, userID string, tenantID uint64, message string, requestedRole types.OrgMemberRole) (*types.OrganizationJoinRequest, error) {
	logger.Infof(ctx, "Tenant %d (rep user %s) submitting join request for organization %s", tenantID, userID, orgID)

	existing, err := s.orgRepo.GetPendingRequestByTenantAndType(ctx, orgID, tenantID, types.JoinRequestTypeJoin)
	if err == nil && existing != nil {
		return nil, ErrPendingRequestExists
	}

	org, err := s.orgRepo.GetByID(ctx, orgID)
	if err != nil {
		if errors.Is(err, repository.ErrOrganizationNotFound) {
			return nil, ErrOrgNotFound
		}
		return nil, err
	}
	if org.MemberLimit > 0 {
		count, errCount := s.orgRepo.CountTenantMembers(ctx, orgID)
		if errCount != nil {
			return nil, errCount
		}
		if count >= int64(org.MemberLimit) {
			return nil, ErrOrgMemberLimitReached
		}
	}

	if requestedRole == "" || !requestedRole.IsValid() {
		requestedRole = types.OrgRoleViewer
	}

	request := &types.OrganizationJoinRequest{
		ID:             uuid.New().String(),
		OrganizationID: orgID,
		UserID:         userID,
		TenantID:       tenantID,
		RequestType:    types.JoinRequestTypeJoin,
		RequestedRole:  requestedRole,
		Status:         types.JoinRequestStatusPending,
		Message:        message,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	if err := s.orgRepo.CreateJoinRequest(ctx, request); err != nil {
		return nil, err
	}

	logger.Infof(ctx, "Join request %s created for organization %s by tenant %d", request.ID, orgID, tenantID)
	return request, nil
}

// ListJoinRequests lists all join requests for an organization
func (s *organizationService) ListJoinRequests(ctx context.Context, orgID string) ([]*types.OrganizationJoinRequest, error) {
	return s.orgRepo.ListJoinRequests(ctx, orgID, "")
}

// CountPendingJoinRequests returns the number of pending join requests for an organization
func (s *organizationService) CountPendingJoinRequests(ctx context.Context, orgID string) (int64, error) {
	return s.orgRepo.CountJoinRequests(ctx, orgID, types.JoinRequestStatusPending)
}

// ReviewJoinRequest reviews a join request or upgrade request (approve or reject).
// On approve the targeted tenant gets the assigned role; reviewerTenantID is
// only used for audit (the gate is the route-level Admin guard).
func (s *organizationService) ReviewJoinRequest(ctx context.Context, orgID string, requestID string, approved bool, reviewerID string, reviewerTenantID uint64, message string, assignRole *types.OrgMemberRole) error {
	request, err := s.orgRepo.GetJoinRequestByID(ctx, requestID)
	if err != nil {
		return ErrJoinRequestNotFound
	}
	if request.OrganizationID != orgID {
		return ErrJoinRequestNotFound
	}

	if request.Status != types.JoinRequestStatusPending {
		return errors.New("request has already been reviewed")
	}

	var status types.JoinRequestStatus
	if approved {
		status = types.JoinRequestStatusApproved

		role := types.OrgRoleViewer
		if assignRole != nil && assignRole.IsValid() {
			role = *assignRole
		} else if request.RequestedRole != "" && request.RequestedRole.IsValid() {
			role = request.RequestedRole
		}

		if request.RequestType == types.JoinRequestTypeUpgrade {
			if err := s.orgRepo.UpdateTenantMemberRole(ctx, request.OrganizationID, request.TenantID, role); err != nil {
				return err
			}
			logger.Infof(ctx, "Upgrade request %s approved, tenant %d role updated to %s in organization %s", requestID, request.TenantID, role, request.OrganizationID)
		} else {
			org, errOrg := s.orgRepo.GetByID(ctx, request.OrganizationID)
			if errOrg != nil {
				return errOrg
			}
			if org.MemberLimit > 0 {
				count, errCount := s.orgRepo.CountTenantMembers(ctx, request.OrganizationID)
				if errCount != nil {
					return errCount
				}
				if count >= int64(org.MemberLimit) {
					return ErrOrgMemberLimitReached
				}
			}
			now := time.Now()
			member := &types.OrganizationTenantMember{
				ID:                   uuid.New().String(),
				OrganizationID:       request.OrganizationID,
				TenantID:             request.TenantID,
				Role:                 role,
				RepresentativeUserID: request.UserID,
				JoinedAt:             &now,
				CreatedAt:            now,
				UpdatedAt:            now,
			}
			if err := s.orgRepo.AddTenantMember(ctx, member); err != nil {
				return err
			}
			logger.Infof(ctx, "Join request %s approved, tenant %d added to organization %s with role %s", requestID, request.TenantID, request.OrganizationID, role)
		}
	} else {
		status = types.JoinRequestStatusRejected
		logger.Infof(ctx, "Request %s rejected for tenant %d", requestID, request.TenantID)
	}
	_ = reviewerTenantID

	return s.orgRepo.UpdateJoinRequestStatus(ctx, requestID, status, reviewerID, message)
}

// RequestRoleUpgrade submits a request to upgrade the caller's tenant's role.
func (s *organizationService) RequestRoleUpgrade(ctx context.Context, orgID string, userID string, tenantID uint64, requestedRole types.OrgMemberRole, message string) (*types.OrganizationJoinRequest, error) {
	logger.Infof(ctx, "Tenant %d (rep user %s) requesting role upgrade for organization %s to role %s", tenantID, userID, orgID, requestedRole)

	member, err := s.orgRepo.GetTenantMember(ctx, orgID, tenantID)
	if err != nil {
		if errors.Is(err, repository.ErrOrgMemberNotFound) {
			return nil, ErrTenantNotInOrg
		}
		return nil, err
	}

	if !requestedRole.IsValid() {
		return nil, ErrInvalidRole
	}

	if member.Role == types.OrgRoleAdmin {
		return nil, ErrAlreadyAdmin
	}

	if !requestedRole.HasPermission(member.Role) || requestedRole == member.Role {
		return nil, ErrCannotUpgradeToSameRole
	}

	existing, err := s.orgRepo.GetPendingRequestByTenantAndType(ctx, orgID, tenantID, types.JoinRequestTypeUpgrade)
	if err == nil && existing != nil {
		return nil, ErrPendingRequestExists
	}

	request := &types.OrganizationJoinRequest{
		ID:             uuid.New().String(),
		OrganizationID: orgID,
		UserID:         userID,
		TenantID:       tenantID,
		RequestType:    types.JoinRequestTypeUpgrade,
		PrevRole:       member.Role,
		RequestedRole:  requestedRole,
		Status:         types.JoinRequestStatusPending,
		Message:        message,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	if err := s.orgRepo.CreateJoinRequest(ctx, request); err != nil {
		return nil, err
	}

	logger.Infof(ctx, "Role upgrade request %s created for organization %s by tenant %d (from %s to %s)", request.ID, orgID, tenantID, member.Role, requestedRole)
	return request, nil
}

// GetPendingUpgradeRequest gets a pending upgrade request for a tenant in an organization
func (s *organizationService) GetPendingUpgradeRequest(ctx context.Context, orgID string, tenantID uint64) (*types.OrganizationJoinRequest, error) {
	request, err := s.orgRepo.GetPendingRequestByTenantAndType(ctx, orgID, tenantID, types.JoinRequestTypeUpgrade)
	if err != nil {
		if errors.Is(err, repository.ErrJoinRequestNotFound) {
			return nil, ErrJoinRequestNotFound
		}
		return nil, err
	}
	return request, nil
}

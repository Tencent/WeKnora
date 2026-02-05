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

// DefaultMemberLimit is the default max members per organization (0 = unlimited)
const DefaultMemberLimit = 200

// ValidInviteCodeValidityDays are the allowed values for invite_code_validity_days
var ValidInviteCodeValidityDays = map[int]bool{0: true, 1: true, 7: true, 30: true}

var (
	ErrOrgNotFound           = errors.New("organization not found")
	ErrOrgPermissionDenied   = errors.New("permission denied for this organization")
	ErrCannotRemoveOwner     = errors.New("cannot remove organization owner")
	ErrCannotChangeOwnerRole = errors.New("cannot change organization owner role")
	ErrUserNotInOrg          = errors.New("user is not a member of this organization")
	ErrInvalidRole           = errors.New("invalid role")
	ErrInviteCodeExpired     = errors.New("invite code has expired")
	ErrInvalidValidityDays   = errors.New("invite_code_validity_days must be 0, 1, 7, or 30")
	ErrOrgMemberLimitReached = errors.New("organization member limit reached")
	ErrOrgMemberLimitTooLow  = errors.New("member limit cannot be lower than current member count")
)

// organizationService implements OrganizationService interface
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

// CreateOrganization creates a new organization
func (s *organizationService) CreateOrganization(ctx context.Context, userID string, tenantID uint64, req *types.CreateOrganizationRequest) (*types.Organization, error) {
	logger.Infof(ctx, "Creating organization: %s by user: %s", req.Name, userID)

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

	// Add the creator as admin member
	member := &types.OrganizationMember{
		ID:             uuid.New().String(),
		OrganizationID: org.ID,
		UserID:         userID,
		TenantID:       tenantID,
		Role:           types.OrgRoleAdmin,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	if err := s.orgRepo.AddMember(ctx, member); err != nil {
		logger.Errorf(ctx, "Failed to add creator as member: %v", err)
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

// ListUserOrganizations lists all organizations that a user belongs to
func (s *organizationService) ListUserOrganizations(ctx context.Context, userID string) ([]*types.Organization, error) {
	return s.orgRepo.ListByUserID(ctx, userID)
}

// UpdateOrganization updates an organization
func (s *organizationService) UpdateOrganization(ctx context.Context, id string, userID string, req *types.UpdateOrganizationRequest) (*types.Organization, error) {
	// Check if user is admin
	isAdmin, err := s.IsOrgAdmin(ctx, id, userID)
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
			count, err := s.orgRepo.CountMembers(ctx, id)
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

	if err := s.orgRepo.Update(ctx, org); err != nil {
		return nil, err
	}

	return org, nil
}

// SearchSearchableOrganizations returns searchable (discoverable) organizations for the current user
func (s *organizationService) SearchSearchableOrganizations(ctx context.Context, userID string, query string, limit int) (*types.ListSearchableOrganizationsResponse, error) {
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
		if mc, err := s.orgRepo.CountMembers(ctx, org.ID); err == nil {
			memberCounts[org.ID] = mc
		}
		shares, _ := s.shareRepo.ListByOrganization(ctx, org.ID)
		shareCounts[org.ID] = int64(len(shares))
		if agentShares, err := s.agentShareRepo.ListByOrganization(ctx, org.ID); err == nil {
			agentShareCounts[org.ID] = len(agentShares)
		}
		_, err := s.orgRepo.GetMember(ctx, org.ID, userID)
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

// JoinByOrganizationID joins a searchable organization by ID (no invite code required)
func (s *organizationService) JoinByOrganizationID(ctx context.Context, orgID string, userID string, tenantID uint64, message string, requestedRole types.OrgMemberRole) (*types.Organization, error) {
	org, err := s.orgRepo.GetByID(ctx, orgID)
	if err != nil {
		if errors.Is(err, repository.ErrOrganizationNotFound) {
			return nil, ErrOrgNotFound
		}
		return nil, err
	}
	if !org.Searchable {
		return nil, ErrOrgPermissionDenied // or a dedicated "org not discoverable" error
	}
	_, err = s.orgRepo.GetMember(ctx, orgID, userID)
	if err == nil {
		return org, nil // already member
	}
	// Validate requested role if provided
	if requestedRole != "" && !requestedRole.IsValid() {
		return nil, ErrInvalidRole
	}
	// Default to viewer if not specified
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
	// Direct join using invite code flow logic (add member)
	_, err = s.JoinByInviteCode(ctx, org.InviteCode, userID, tenantID)
	if err != nil {
		return nil, err
	}
	return org, nil
}

// DeleteOrganization deletes an organization
func (s *organizationService) DeleteOrganization(ctx context.Context, id string, userID string) error {
	org, err := s.orgRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}

	// Only owner can delete organization
	if org.OwnerID != userID {
		return ErrOrgPermissionDenied
	}

	// Remove all KB shares for this org so members no longer see associated knowledge bases
	if err := s.shareRepo.DeleteByOrganizationID(ctx, id); err != nil {
		logger.Warnf(ctx, "Failed to delete KB shares for organization %s: %v", id, err)
	}
	if err := s.agentShareRepo.DeleteByOrganizationID(ctx, id); err != nil {
		logger.Warnf(ctx, "Failed to delete agent shares for organization %s: %v", id, err)
	}

	return s.orgRepo.Delete(ctx, id)
}

// AddMember adds a member to an organization
func (s *organizationService) AddMember(ctx context.Context, orgID string, userID string, tenantID uint64, role types.OrgMemberRole) error {
	if !role.IsValid() {
		return ErrInvalidRole
	}

	org, err := s.orgRepo.GetByID(ctx, orgID)
	if err != nil {
		return err
	}
	if org.MemberLimit > 0 {
		count, errCount := s.orgRepo.CountMembers(ctx, orgID)
		if errCount != nil {
			return errCount
		}
		if count >= int64(org.MemberLimit) {
			return ErrOrgMemberLimitReached
		}
	}

	member := &types.OrganizationMember{
		ID:             uuid.New().String(),
		OrganizationID: orgID,
		UserID:         userID,
		TenantID:       tenantID,
		Role:           role,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	return s.orgRepo.AddMember(ctx, member)
}

// RemoveMember removes a member from an organization.
// When operatorUserID == memberUserID, it is "leave" (self-removal) and does not require admin.
// When removing another member, operator must be admin.
func (s *organizationService) RemoveMember(ctx context.Context, orgID string, memberUserID string, operatorUserID string) error {
	// Check if trying to remove owner
	org, err := s.orgRepo.GetByID(ctx, orgID)
	if err != nil {
		return err
	}
	if org.OwnerID == memberUserID {
		return ErrCannotRemoveOwner
	}

	// Self-removal (leave): allow any member to leave
	if operatorUserID == memberUserID {
		return s.orgRepo.RemoveMember(ctx, orgID, memberUserID)
	}

	// Removing another member: require operator to be admin
	isAdmin, err := s.IsOrgAdmin(ctx, orgID, operatorUserID)
	if err != nil {
		return err
	}
	if !isAdmin {
		return ErrOrgPermissionDenied
	}

	return s.orgRepo.RemoveMember(ctx, orgID, memberUserID)
}

// UpdateMemberRole updates a member's role
func (s *organizationService) UpdateMemberRole(ctx context.Context, orgID string, memberUserID string, role types.OrgMemberRole, operatorUserID string) error {
	if !role.IsValid() {
		return ErrInvalidRole
	}

	// Check if operator is admin
	isAdmin, err := s.IsOrgAdmin(ctx, orgID, operatorUserID)
	if err != nil {
		return err
	}
	if !isAdmin {
		return ErrOrgPermissionDenied
	}

	// Check if trying to change owner's role
	org, err := s.orgRepo.GetByID(ctx, orgID)
	if err != nil {
		return err
	}
	if org.OwnerID == memberUserID {
		return ErrCannotChangeOwnerRole
	}

	return s.orgRepo.UpdateMemberRole(ctx, orgID, memberUserID, role)
}

// ListMembers lists all members of an organization
func (s *organizationService) ListMembers(ctx context.Context, orgID string) ([]*types.OrganizationMember, error) {
	return s.orgRepo.ListMembers(ctx, orgID)
}

// GetMember gets a specific member of an organization
func (s *organizationService) GetMember(ctx context.Context, orgID string, userID string) (*types.OrganizationMember, error) {
	member, err := s.orgRepo.GetMember(ctx, orgID, userID)
	if err != nil {
		if errors.Is(err, repository.ErrOrgMemberNotFound) {
			return nil, ErrUserNotInOrg
		}
		return nil, err
	}
	return member, nil
}

// GenerateInviteCode generates a new invite code for an organization
func (s *organizationService) GenerateInviteCode(ctx context.Context, orgID string, userID string) (string, error) {
	// Check if user is admin
	isAdmin, err := s.IsOrgAdmin(ctx, orgID, userID)
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
	// 0 = never expire (expiresAt nil); 1/7/30 = that many days

	inviteCode := generateInviteCode()
	now := time.Now()
	expiresAt := resolveInviteExpiry(validityDays, now)
	if err := s.orgRepo.UpdateInviteCode(ctx, orgID, inviteCode, expiresAt); err != nil {
		return "", err
	}

	return inviteCode, nil
}

// JoinByInviteCode allows a user to join an organization via invite code
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

	// check if the organization need approval
	if org.RequireApproval {
		logger.Infof(ctx, "Organization %s requires approval", org.ID)
		return nil, ErrOrgPermissionDenied
	}

	// Check if user is already a member
	_, err = s.orgRepo.GetMember(ctx, org.ID, userID)
	if err == nil {
		// User is already a member, just return the organization
		return org, nil
	}
	if !errors.Is(err, repository.ErrOrgMemberNotFound) {
		return nil, err
	}

	// Check member limit (0 = unlimited)
	if org.MemberLimit > 0 {
		count, errCount := s.orgRepo.CountMembers(ctx, org.ID)
		if errCount != nil {
			return nil, errCount
		}
		if count >= int64(org.MemberLimit) {
			return nil, ErrOrgMemberLimitReached
		}
	}

	// Add user as viewer by default
	member := &types.OrganizationMember{
		ID:             uuid.New().String(),
		OrganizationID: org.ID,
		UserID:         userID,
		TenantID:       tenantID,
		Role:           types.OrgRoleViewer,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	if err := s.orgRepo.AddMember(ctx, member); err != nil {
		return nil, err
	}

	logger.Infof(ctx, "User %s joined organization %s via invite code", userID, org.ID)
	return org, nil
}

// IsOrgAdmin checks if a user is an admin of an organization
func (s *organizationService) IsOrgAdmin(ctx context.Context, orgID string, userID string) (bool, error) {
	member, err := s.orgRepo.GetMember(ctx, orgID, userID)
	if err != nil {
		if errors.Is(err, repository.ErrOrgMemberNotFound) {
			return false, nil
		}
		return false, err
	}
	return member.Role == types.OrgRoleAdmin, nil
}

// GetUserRoleInOrg gets a user's role in an organization
func (s *organizationService) GetUserRoleInOrg(ctx context.Context, orgID string, userID string) (types.OrgMemberRole, error) {
	member, err := s.orgRepo.GetMember(ctx, orgID, userID)
	if err != nil {
		if errors.Is(err, repository.ErrOrgMemberNotFound) {
			return "", ErrUserNotInOrg
		}
		return "", err
	}
	return member.Role, nil
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
	ErrAlreadyAdmin            = errors.New("user is already an admin")
)

// SubmitJoinRequest submits a request to join an organization
func (s *organizationService) SubmitJoinRequest(ctx context.Context, orgID string, userID string, tenantID uint64, message string, requestedRole types.OrgMemberRole) (*types.OrganizationJoinRequest, error) {
	logger.Infof(ctx, "User %s submitting join request for organization %s", userID, orgID)

	// Check if there's already a pending join request
	existing, err := s.orgRepo.GetPendingRequestByType(ctx, orgID, userID, types.JoinRequestTypeJoin)
	if err == nil && existing != nil {
		return nil, ErrPendingRequestExists
	}

	// Reject if organization is already at member limit
	org, err := s.orgRepo.GetByID(ctx, orgID)
	if err != nil {
		if errors.Is(err, repository.ErrOrganizationNotFound) {
			return nil, ErrOrgNotFound
		}
		return nil, err
	}
	if org.MemberLimit > 0 {
		count, errCount := s.orgRepo.CountMembers(ctx, orgID)
		if errCount != nil {
			return nil, errCount
		}
		if count >= int64(org.MemberLimit) {
			return nil, ErrOrgMemberLimitReached
		}
	}

	// Default to viewer if role is empty or invalid
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

	logger.Infof(ctx, "Join request %s created for organization %s by user %s", request.ID, orgID, userID)
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
// When approving, assignRole overrides the applicant's requested role if set; otherwise uses request.RequestedRole or viewer.
func (s *organizationService) ReviewJoinRequest(ctx context.Context, orgID string, requestID string, approved bool, reviewerID string, message string, assignRole *types.OrgMemberRole) error {
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

		// Role to assign: admin override > applicant's requested role > viewer
		role := types.OrgRoleViewer
		if assignRole != nil && assignRole.IsValid() {
			role = *assignRole
		} else if request.RequestedRole != "" && request.RequestedRole.IsValid() {
			role = request.RequestedRole
		}

		// Handle based on request type
		if request.RequestType == types.JoinRequestTypeUpgrade {
			// Upgrade: update existing member's role
			if err := s.orgRepo.UpdateMemberRole(ctx, request.OrganizationID, request.UserID, role); err != nil {
				return err
			}
			logger.Infof(ctx, "Upgrade request %s approved, user %s role updated to %s in organization %s", requestID, request.UserID, role, request.OrganizationID)
		} else {
			// Join: check member limit then add new member
			org, errOrg := s.orgRepo.GetByID(ctx, request.OrganizationID)
			if errOrg != nil {
				return errOrg
			}
			if org.MemberLimit > 0 {
				count, errCount := s.orgRepo.CountMembers(ctx, request.OrganizationID)
				if errCount != nil {
					return errCount
				}
				if count >= int64(org.MemberLimit) {
					return ErrOrgMemberLimitReached
				}
			}
			member := &types.OrganizationMember{
				ID:             uuid.New().String(),
				OrganizationID: request.OrganizationID,
				UserID:         request.UserID,
				TenantID:       request.TenantID,
				Role:           role,
				CreatedAt:      time.Now(),
				UpdatedAt:      time.Now(),
			}
			if err := s.orgRepo.AddMember(ctx, member); err != nil {
				return err
			}
			logger.Infof(ctx, "Join request %s approved, user %s added to organization %s with role %s", requestID, request.UserID, request.OrganizationID, role)
		}
	} else {
		status = types.JoinRequestStatusRejected
		logger.Infof(ctx, "Request %s rejected for user %s", requestID, request.UserID)
	}

	return s.orgRepo.UpdateJoinRequestStatus(ctx, requestID, status, reviewerID, message)
}

// RequestRoleUpgrade submits a request to upgrade role in an organization
func (s *organizationService) RequestRoleUpgrade(ctx context.Context, orgID string, userID string, tenantID uint64, requestedRole types.OrgMemberRole, message string) (*types.OrganizationJoinRequest, error) {
	logger.Infof(ctx, "User %s submitting role upgrade request for organization %s to role %s", userID, orgID, requestedRole)

	// Check if user is a member
	member, err := s.orgRepo.GetMember(ctx, orgID, userID)
	if err != nil {
		if errors.Is(err, repository.ErrOrgMemberNotFound) {
			return nil, ErrUserNotInOrg
		}
		return nil, err
	}

	// Validate the requested role
	if !requestedRole.IsValid() {
		return nil, ErrInvalidRole
	}

	// Check if already admin
	if member.Role == types.OrgRoleAdmin {
		return nil, ErrAlreadyAdmin
	}

	// Check if requested role is higher than current role
	if !requestedRole.HasPermission(member.Role) || requestedRole == member.Role {
		return nil, ErrCannotUpgradeToSameRole
	}

	// Check if there's already a pending upgrade request
	existing, err := s.orgRepo.GetPendingRequestByType(ctx, orgID, userID, types.JoinRequestTypeUpgrade)
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

	logger.Infof(ctx, "Role upgrade request %s created for organization %s by user %s (from %s to %s)", request.ID, orgID, userID, member.Role, requestedRole)
	return request, nil
}

// GetPendingUpgradeRequest gets a pending upgrade request for a user in an organization
func (s *organizationService) GetPendingUpgradeRequest(ctx context.Context, orgID string, userID string) (*types.OrganizationJoinRequest, error) {
	request, err := s.orgRepo.GetPendingRequestByType(ctx, orgID, userID, types.JoinRequestTypeUpgrade)
	if err != nil {
		if errors.Is(err, repository.ErrJoinRequestNotFound) {
			return nil, ErrJoinRequestNotFound
		}
		return nil, err
	}
	return request, nil
}

package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

var (
	ErrOrganizationNotFound   = errors.New("organization not found")
	ErrOrgMemberNotFound      = errors.New("organization member not found")
	ErrOrgMemberAlreadyExists = errors.New("member already exists in organization")
	ErrInviteCodeNotFound     = errors.New("invite code not found")
	ErrInviteCodeExpired      = errors.New("invite code has expired")
)

// organizationRepository implements OrganizationRepository interface
type organizationRepository struct {
	db *gorm.DB
}

// NewOrganizationRepository creates a new organization repository
func NewOrganizationRepository(db *gorm.DB) interfaces.OrganizationRepository {
	return &organizationRepository{db: db}
}

// Create creates a new organization
func (r *organizationRepository) Create(ctx context.Context, org *types.Organization) error {
	return r.db.WithContext(ctx).Create(org).Error
}

// GetByID gets an organization by ID
func (r *organizationRepository) GetByID(ctx context.Context, id string) (*types.Organization, error) {
	var org types.Organization
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&org).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrOrganizationNotFound
		}
		return nil, err
	}
	return &org, nil
}

// GetByInviteCode gets an organization by invite code (returns ErrInviteCodeExpired if code has expired)
func (r *organizationRepository) GetByInviteCode(ctx context.Context, inviteCode string) (*types.Organization, error) {
	var org types.Organization
	if err := r.db.WithContext(ctx).Where("invite_code = ?", inviteCode).First(&org).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrInviteCodeNotFound
		}
		return nil, err
	}
	if org.InviteCodeExpiresAt != nil && org.InviteCodeExpiresAt.Before(time.Now()) {
		return nil, ErrInviteCodeExpired
	}
	return &org, nil
}

// ListByUserID lists organizations that a user belongs to
func (r *organizationRepository) ListByUserID(ctx context.Context, userID string) ([]*types.Organization, error) {
	var orgs []*types.Organization

	// Get organizations where user is a member
	err := r.db.WithContext(ctx).
		Joins("JOIN organization_members ON organization_members.organization_id = organizations.id").
		Where("organization_members.user_id = ?", userID).
		Order("organizations.created_at DESC").
		Find(&orgs).Error

	if err != nil {
		return nil, err
	}
	return orgs, nil
}

// ListSearchable lists organizations that are searchable (open for discovery), optionally filtered by name/description/ID
func (r *organizationRepository) ListSearchable(ctx context.Context, query string, limit int) ([]*types.Organization, error) {
	if limit <= 0 {
		limit = 20
	}
	var orgs []*types.Organization
	q := r.db.WithContext(ctx).Where("searchable = ?", true)
	if query != "" {
		pattern := "%" + query + "%"
		// 支持按名称、描述或空间 ID 搜索，便于区分同名空间
		q = q.Where("name ILIKE ? OR description ILIKE ? OR id::text ILIKE ?", pattern, pattern, pattern)
	}
	err := q.Order("created_at DESC").Limit(limit).Find(&orgs).Error
	if err != nil {
		return nil, err
	}
	return orgs, nil
}

// Update updates an organization (Select ensures zero values like invite_code_validity_days=0 are persisted)
func (r *organizationRepository) Update(ctx context.Context, org *types.Organization) error {
	return r.db.WithContext(ctx).Model(&types.Organization{}).Where("id = ?", org.ID).
		Select("name", "description", "avatar", "require_approval", "searchable", "invite_code_validity_days", "member_limit", "updated_at").
		Updates(org).Error
}

// Delete soft deletes an organization
func (r *organizationRepository) Delete(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Where("id = ?", id).Delete(&types.Organization{}).Error
}

// AddMember adds a member to an organization
func (r *organizationRepository) AddMember(ctx context.Context, member *types.OrganizationMember) error {
	// Check if member already exists
	var count int64
	r.db.WithContext(ctx).Model(&types.OrganizationMember{}).
		Where("organization_id = ? AND user_id = ?", member.OrganizationID, member.UserID).
		Count(&count)

	if count > 0 {
		return ErrOrgMemberAlreadyExists
	}

	return r.db.WithContext(ctx).Create(member).Error
}

// RemoveMember removes a member from an organization
func (r *organizationRepository) RemoveMember(ctx context.Context, orgID string, userID string) error {
	result := r.db.WithContext(ctx).
		Where("organization_id = ? AND user_id = ?", orgID, userID).
		Delete(&types.OrganizationMember{})

	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrOrgMemberNotFound
	}
	return nil
}

// UpdateMemberRole updates a member's role in an organization
func (r *organizationRepository) UpdateMemberRole(ctx context.Context, orgID string, userID string, role types.OrgMemberRole) error {
	result := r.db.WithContext(ctx).
		Model(&types.OrganizationMember{}).
		Where("organization_id = ? AND user_id = ?", orgID, userID).
		Update("role", role)

	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrOrgMemberNotFound
	}
	return nil
}

// ListMembers lists all members of an organization
func (r *organizationRepository) ListMembers(ctx context.Context, orgID string) ([]*types.OrganizationMember, error) {
	var members []*types.OrganizationMember
	err := r.db.WithContext(ctx).
		Preload("User").
		Where("organization_id = ?", orgID).
		Order("created_at ASC").
		Find(&members).Error

	if err != nil {
		return nil, err
	}
	return members, nil
}

// GetMember gets a specific member of an organization
func (r *organizationRepository) GetMember(ctx context.Context, orgID string, userID string) (*types.OrganizationMember, error) {
	var member types.OrganizationMember
	err := r.db.WithContext(ctx).
		Where("organization_id = ? AND user_id = ?", orgID, userID).
		First(&member).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrOrgMemberNotFound
		}
		return nil, err
	}
	return &member, nil
}

// ListMembersByUserForOrgs returns one member record per org where the user is a member (batch).
func (r *organizationRepository) ListMembersByUserForOrgs(ctx context.Context, userID string, orgIDs []string) (map[string]*types.OrganizationMember, error) {
	if len(orgIDs) == 0 {
		return make(map[string]*types.OrganizationMember), nil
	}
	var members []*types.OrganizationMember
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND organization_id IN ?", userID, orgIDs).
		Find(&members).Error
	if err != nil {
		return nil, err
	}
	out := make(map[string]*types.OrganizationMember, len(members))
	for _, m := range members {
		if m != nil {
			out[m.OrganizationID] = m
		}
	}
	return out, nil
}

// CountMembers counts the number of members in an organization
func (r *organizationRepository) CountMembers(ctx context.Context, orgID string) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&types.OrganizationMember{}).
		Where("organization_id = ?", orgID).
		Count(&count).Error
	return count, err
}

// UpdateInviteCode updates the invite code and optional expiry for an organization (expiresAt nil = never expire)
func (r *organizationRepository) UpdateInviteCode(ctx context.Context, orgID string, inviteCode string, expiresAt *time.Time) error {
	updates := map[string]interface{}{"invite_code": inviteCode, "invite_code_expires_at": expiresAt}
	return r.db.WithContext(ctx).
		Model(&types.Organization{}).
		Where("id = ?", orgID).
		Updates(updates).Error
}

// ----------------
// Join Requests
// ----------------

var ErrJoinRequestNotFound = errors.New("join request not found")

// CreateJoinRequest creates a new join request
func (r *organizationRepository) CreateJoinRequest(ctx context.Context, request *types.OrganizationJoinRequest) error {
	return r.db.WithContext(ctx).Create(request).Error
}

// GetJoinRequestByID gets a join request by ID
func (r *organizationRepository) GetJoinRequestByID(ctx context.Context, id string) (*types.OrganizationJoinRequest, error) {
	var request types.OrganizationJoinRequest
	err := r.db.WithContext(ctx).
		Preload("User").
		Where("id = ?", id).
		First(&request).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrJoinRequestNotFound
		}
		return nil, err
	}
	return &request, nil
}

// GetPendingJoinRequest gets a pending join request for a user in an organization (any type)
func (r *organizationRepository) GetPendingJoinRequest(ctx context.Context, orgID string, userID string) (*types.OrganizationJoinRequest, error) {
	var request types.OrganizationJoinRequest
	err := r.db.WithContext(ctx).
		Where("organization_id = ? AND user_id = ? AND status = ?", orgID, userID, types.JoinRequestStatusPending).
		First(&request).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrJoinRequestNotFound
		}
		return nil, err
	}
	return &request, nil
}

// GetPendingRequestByType gets a pending request for a user filtered by request type
func (r *organizationRepository) GetPendingRequestByType(ctx context.Context, orgID string, userID string, requestType types.JoinRequestType) (*types.OrganizationJoinRequest, error) {
	var request types.OrganizationJoinRequest
	err := r.db.WithContext(ctx).
		Where("organization_id = ? AND user_id = ? AND status = ? AND request_type = ?", orgID, userID, types.JoinRequestStatusPending, requestType).
		First(&request).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrJoinRequestNotFound
		}
		return nil, err
	}
	return &request, nil
}

// ListJoinRequests lists join requests for an organization
func (r *organizationRepository) ListJoinRequests(ctx context.Context, orgID string, status types.JoinRequestStatus) ([]*types.OrganizationJoinRequest, error) {
	var requests []*types.OrganizationJoinRequest
	query := r.db.WithContext(ctx).
		Preload("User").
		Where("organization_id = ?", orgID)

	if status != "" {
		query = query.Where("status = ?", status)
	}

	err := query.Order("created_at DESC").Find(&requests).Error
	if err != nil {
		return nil, err
	}
	return requests, nil
}

// CountJoinRequests counts join requests for an organization by status
func (r *organizationRepository) CountJoinRequests(ctx context.Context, orgID string, status types.JoinRequestStatus) (int64, error) {
	var count int64
	query := r.db.WithContext(ctx).Model(&types.OrganizationJoinRequest{}).Where("organization_id = ?", orgID)
	if status != "" {
		query = query.Where("status = ?", status)
	}
	err := query.Count(&count).Error
	return count, err
}

// UpdateJoinRequestStatus updates the status of a join request
func (r *organizationRepository) UpdateJoinRequestStatus(ctx context.Context, id string, status types.JoinRequestStatus, reviewedBy string, reviewMessage string) error {
	return r.db.WithContext(ctx).
		Model(&types.OrganizationJoinRequest{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":         status,
			"reviewed_by":    reviewedBy,
			"reviewed_at":    gorm.Expr("NOW()"),
			"review_message": reviewMessage,
		}).Error
}

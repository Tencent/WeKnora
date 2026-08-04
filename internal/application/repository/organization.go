package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/database"
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

// organizationRepository implements OrganizationRepository.
//
// Plan 3 of #1303 lifts membership from per-user to per-tenant (see
// migration 000045). All "member" methods on this repo now operate on
// the (org, tenant) tuple; the underlying table is
// organization_tenant_members.
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

// ListByTenantID lists organizations whose tenant participates as a member.
func (r *organizationRepository) ListByTenantID(ctx context.Context, tenantID uint64) ([]*types.Organization, error) {
	var orgs []*types.Organization

	err := r.db.WithContext(ctx).
		Joins("JOIN organization_tenant_members otm ON otm.organization_id = organizations.id").
		Where("otm.tenant_id = ?", tenantID).
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
		dialect := r.db.Dialector.Name()
		q = q.Where(
			database.CaseInsensitiveMatch(dialect, "name")+" OR "+
				database.CaseInsensitiveMatch(dialect, "description")+" OR "+
				database.CaseInsensitiveMatch(dialect, "id"),
			pattern, pattern, pattern,
		)
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

// AddTenantMember inserts a new (org, tenant) membership row. Returns
// ErrOrgMemberAlreadyExists if a row already exists for this tuple — the
// service layer treats that as a no-op when the caller is just confirming
// an idempotent join.
func (r *organizationRepository) AddTenantMember(ctx context.Context, member *types.OrganizationTenantMember) error {
	var count int64
	r.db.WithContext(ctx).Model(&types.OrganizationTenantMember{}).
		Where("organization_id = ? AND tenant_id = ?", member.OrganizationID, member.TenantID).
		Count(&count)

	if count > 0 {
		return ErrOrgMemberAlreadyExists
	}

	return r.db.WithContext(ctx).Create(member).Error
}

// RemoveTenantMember removes the (org, tenant) membership row.
func (r *organizationRepository) RemoveTenantMember(ctx context.Context, orgID string, tenantID uint64) error {
	result := r.db.WithContext(ctx).
		Where("organization_id = ? AND tenant_id = ?", orgID, tenantID).
		Delete(&types.OrganizationTenantMember{})

	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrOrgMemberNotFound
	}
	return nil
}

// UpdateTenantMemberRole updates the role for a (org, tenant) membership.
func (r *organizationRepository) UpdateTenantMemberRole(ctx context.Context, orgID string, tenantID uint64, role types.OrgMemberRole) error {
	result := r.db.WithContext(ctx).
		Model(&types.OrganizationTenantMember{}).
		Where("organization_id = ? AND tenant_id = ?", orgID, tenantID).
		Update("role", role)

	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrOrgMemberNotFound
	}
	return nil
}

// ListTenantMembers lists all tenant memberships for an organization.
func (r *organizationRepository) ListTenantMembers(ctx context.Context, orgID string) ([]*types.OrganizationTenantMember, error) {
	var members []*types.OrganizationTenantMember
	err := r.db.WithContext(ctx).
		Preload("RepresentativeUser").
		Where("organization_id = ?", orgID).
		Order("created_at ASC").
		Find(&members).Error

	if err != nil {
		return nil, err
	}
	return members, nil
}

// GetTenantMember returns the (org, tenant) membership row, or
// ErrOrgMemberNotFound when missing. This is the canonical permission
// check primitive — callers compose this with the share permission
// to compute effective access.
func (r *organizationRepository) GetTenantMember(ctx context.Context, orgID string, tenantID uint64) (*types.OrganizationTenantMember, error) {
	var member types.OrganizationTenantMember
	err := r.db.WithContext(ctx).
		Where("organization_id = ? AND tenant_id = ?", orgID, tenantID).
		First(&member).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrOrgMemberNotFound
		}
		return nil, err
	}
	return &member, nil
}

// ListTenantMembersByTenantForOrgs returns one membership row per org where
// the tenant participates (batch).
func (r *organizationRepository) ListTenantMembersByTenantForOrgs(ctx context.Context, tenantID uint64, orgIDs []string) (map[string]*types.OrganizationTenantMember, error) {
	if len(orgIDs) == 0 {
		return make(map[string]*types.OrganizationTenantMember), nil
	}
	var members []*types.OrganizationTenantMember
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND organization_id IN ?", tenantID, orgIDs).
		Find(&members).Error
	if err != nil {
		return nil, err
	}
	out := make(map[string]*types.OrganizationTenantMember, len(members))
	for _, m := range members {
		if m != nil {
			out[m.OrganizationID] = m
		}
	}
	return out, nil
}

// CountTenantMembers counts the number of tenant members in an organization.
func (r *organizationRepository) CountTenantMembers(ctx context.Context, orgID string) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&types.OrganizationTenantMember{}).
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

// GetPendingJoinRequestByTenant returns a pending request for the given
// (org, tenant). After Plan 3 the dedup key is per-tenant, not per-user
// — the user requesting and the user reviewing may be different humans
// in the same tenant, but the resource being granted is tenant-level
// access.
func (r *organizationRepository) GetPendingJoinRequestByTenant(ctx context.Context, orgID string, tenantID uint64) (*types.OrganizationJoinRequest, error) {
	var request types.OrganizationJoinRequest
	err := r.db.WithContext(ctx).
		Where("organization_id = ? AND tenant_id = ? AND status = ?", orgID, tenantID, types.JoinRequestStatusPending).
		First(&request).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrJoinRequestNotFound
		}
		return nil, err
	}
	return &request, nil
}

// GetPendingRequestByTenantAndType narrows the (org, tenant) pending
// dedup query to a specific request_type (join | upgrade).
func (r *organizationRepository) GetPendingRequestByTenantAndType(ctx context.Context, orgID string, tenantID uint64, requestType types.JoinRequestType) (*types.OrganizationJoinRequest, error) {
	var request types.OrganizationJoinRequest
	err := r.db.WithContext(ctx).
		Where("organization_id = ? AND tenant_id = ? AND status = ? AND request_type = ?", orgID, tenantID, types.JoinRequestStatusPending, requestType).
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

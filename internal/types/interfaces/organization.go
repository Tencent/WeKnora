package interfaces

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// OrganizationService defines the organization service interface
type OrganizationService interface {
	// Organization CRUD
	CreateOrganization(ctx context.Context, userID string, tenantID uint64, req *types.CreateOrganizationRequest) (*types.Organization, error)
	GetOrganization(ctx context.Context, id string) (*types.Organization, error)
	GetOrganizationByInviteCode(ctx context.Context, inviteCode string) (*types.Organization, error)
	ListUserOrganizations(ctx context.Context, userID string) ([]*types.Organization, error)
	UpdateOrganization(ctx context.Context, id string, userID string, req *types.UpdateOrganizationRequest) (*types.Organization, error)
	DeleteOrganization(ctx context.Context, id string, userID string) error

	// Member Management
	AddMember(ctx context.Context, orgID string, userID string, tenantID uint64, role types.OrgMemberRole) error
	RemoveMember(ctx context.Context, orgID string, memberUserID string, operatorUserID string) error
	UpdateMemberRole(ctx context.Context, orgID string, memberUserID string, role types.OrgMemberRole, operatorUserID string) error
	ListMembers(ctx context.Context, orgID string) ([]*types.OrganizationMember, error)
	GetMember(ctx context.Context, orgID string, userID string) (*types.OrganizationMember, error)

	// Invite Code
	GenerateInviteCode(ctx context.Context, orgID string, userID string) (string, error)
	JoinByInviteCode(ctx context.Context, inviteCode string, userID string, tenantID uint64) (*types.Organization, error)
	// Searchable organizations (discovery)
	SearchSearchableOrganizations(ctx context.Context, userID string, query string, limit int) (*types.ListSearchableOrganizationsResponse, error)
	JoinByOrganizationID(ctx context.Context, orgID string, userID string, tenantID uint64, message string, requestedRole types.OrgMemberRole) (*types.Organization, error)

	// Join Requests (for organizations that require approval)
	SubmitJoinRequest(ctx context.Context, orgID string, userID string, tenantID uint64, message string, requestedRole types.OrgMemberRole) (*types.OrganizationJoinRequest, error)
	ListJoinRequests(ctx context.Context, orgID string) ([]*types.OrganizationJoinRequest, error)
	CountPendingJoinRequests(ctx context.Context, orgID string) (int64, error)
	ReviewJoinRequest(ctx context.Context, orgID string, requestID string, approved bool, reviewerID string, message string, assignRole *types.OrgMemberRole) error

	// Role Upgrade Requests (for existing members to request higher permissions)
	RequestRoleUpgrade(ctx context.Context, orgID string, userID string, tenantID uint64, requestedRole types.OrgMemberRole, message string) (*types.OrganizationJoinRequest, error)
	GetPendingUpgradeRequest(ctx context.Context, orgID string, userID string) (*types.OrganizationJoinRequest, error)

	// Permission Check
	IsOrgAdmin(ctx context.Context, orgID string, userID string) (bool, error)
	GetUserRoleInOrg(ctx context.Context, orgID string, userID string) (types.OrgMemberRole, error)
}

// OrganizationRepository defines the organization repository interface
type OrganizationRepository interface {
	// Organization CRUD
	Create(ctx context.Context, org *types.Organization) error
	GetByID(ctx context.Context, id string) (*types.Organization, error)
	GetByInviteCode(ctx context.Context, inviteCode string) (*types.Organization, error)
	ListByUserID(ctx context.Context, userID string) ([]*types.Organization, error)
	ListSearchable(ctx context.Context, query string, limit int) ([]*types.Organization, error)
	Update(ctx context.Context, org *types.Organization) error
	Delete(ctx context.Context, id string) error

	// Member operations
	AddMember(ctx context.Context, member *types.OrganizationMember) error
	RemoveMember(ctx context.Context, orgID string, userID string) error
	UpdateMemberRole(ctx context.Context, orgID string, userID string, role types.OrgMemberRole) error
	ListMembers(ctx context.Context, orgID string) ([]*types.OrganizationMember, error)
	GetMember(ctx context.Context, orgID string, userID string) (*types.OrganizationMember, error)
	CountMembers(ctx context.Context, orgID string) (int64, error)

	// Invite code
	UpdateInviteCode(ctx context.Context, orgID string, inviteCode string, expiresAt *time.Time) error

	// Join requests
	CreateJoinRequest(ctx context.Context, request *types.OrganizationJoinRequest) error
	GetJoinRequestByID(ctx context.Context, id string) (*types.OrganizationJoinRequest, error)
	GetPendingJoinRequest(ctx context.Context, orgID string, userID string) (*types.OrganizationJoinRequest, error)
	GetPendingRequestByType(ctx context.Context, orgID string, userID string, requestType types.JoinRequestType) (*types.OrganizationJoinRequest, error)
	ListJoinRequests(ctx context.Context, orgID string, status types.JoinRequestStatus) ([]*types.OrganizationJoinRequest, error)
	CountJoinRequests(ctx context.Context, orgID string, status types.JoinRequestStatus) (int64, error)
	UpdateJoinRequestStatus(ctx context.Context, id string, status types.JoinRequestStatus, reviewedBy string, reviewMessage string) error
}

// KBShareService defines the knowledge base sharing service interface
type KBShareService interface {
	// Share Management
	ShareKnowledgeBase(ctx context.Context, kbID string, orgID string, userID string, tenantID uint64, permission types.OrgMemberRole) (*types.KnowledgeBaseShare, error)
	UpdateSharePermission(ctx context.Context, shareID string, permission types.OrgMemberRole, userID string) error
	RemoveShare(ctx context.Context, shareID string, userID string) error

	// Query
	ListSharesByKnowledgeBase(ctx context.Context, kbID string) ([]*types.KnowledgeBaseShare, error)
	ListSharesByOrganization(ctx context.Context, orgID string) ([]*types.KnowledgeBaseShare, error)
	ListSharedKnowledgeBases(ctx context.Context, userID string, currentTenantID uint64) ([]*types.SharedKnowledgeBaseInfo, error)
	GetShare(ctx context.Context, shareID string) (*types.KnowledgeBaseShare, error)
	GetShareByKBAndOrg(ctx context.Context, kbID string, orgID string) (*types.KnowledgeBaseShare, error)

	// Permission Check
	CheckUserKBPermission(ctx context.Context, kbID string, userID string) (types.OrgMemberRole, bool, error)
	HasKBPermission(ctx context.Context, kbID string, userID string, requiredRole types.OrgMemberRole) (bool, error)

	// Get source tenant for cross-tenant embedding
	GetKBSourceTenant(ctx context.Context, kbID string) (uint64, error)

	// Count shares for knowledge bases
	CountSharesByKnowledgeBaseIDs(ctx context.Context, kbIDs []string) (map[string]int64, error)
}

// KBShareRepository defines the knowledge base sharing repository interface
type KBShareRepository interface {
	// CRUD
	Create(ctx context.Context, share *types.KnowledgeBaseShare) error
	GetByID(ctx context.Context, id string) (*types.KnowledgeBaseShare, error)
	GetByKBAndOrg(ctx context.Context, kbID string, orgID string) (*types.KnowledgeBaseShare, error)
	Update(ctx context.Context, share *types.KnowledgeBaseShare) error
	Delete(ctx context.Context, id string) error
	// DeleteByKnowledgeBaseID soft-deletes all shares for a knowledge base (e.g. when KB is deleted)
	DeleteByKnowledgeBaseID(ctx context.Context, kbID string) error
	// DeleteByOrganizationID soft-deletes all shares for an organization (e.g. when the org is deleted)
	DeleteByOrganizationID(ctx context.Context, orgID string) error

	// List
	ListByKnowledgeBase(ctx context.Context, kbID string) ([]*types.KnowledgeBaseShare, error)
	ListByOrganization(ctx context.Context, orgID string) ([]*types.KnowledgeBaseShare, error)

	// Query for user's accessible shared knowledge bases
	ListSharedKBsForUser(ctx context.Context, userID string) ([]*types.KnowledgeBaseShare, error)

	// Count shares
	CountSharesByKnowledgeBaseID(ctx context.Context, kbID string) (int64, error)
	CountSharesByKnowledgeBaseIDs(ctx context.Context, kbIDs []string) (map[string]int64, error)
}

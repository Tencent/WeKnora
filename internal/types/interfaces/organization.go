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
	ListMembersByUserForOrgs(ctx context.Context, userID string, orgIDs []string) (map[string]*types.OrganizationMember, error)
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
	// ListSharesByKnowledgeBase lists shares for a KB; tenantID must own the KB (authz check).
	ListSharesByKnowledgeBase(ctx context.Context, kbID string, tenantID uint64) ([]*types.KnowledgeBaseShare, error)
	ListSharesByOrganization(ctx context.Context, orgID string) ([]*types.KnowledgeBaseShare, error)
	ListSharedKnowledgeBases(ctx context.Context, userID string, currentTenantID uint64) ([]*types.SharedKnowledgeBaseInfo, error)
	ListSharedKnowledgeBasesInOrganization(ctx context.Context, orgID string, userID string, currentTenantID uint64) ([]*types.OrganizationSharedKnowledgeBaseItem, error)
	// ListSharedKnowledgeBaseIDsByOrganizations returns per-org direct shared KB IDs (batch, for sidebar count).
	ListSharedKnowledgeBaseIDsByOrganizations(ctx context.Context, orgIDs []string, userID string) (map[string][]string, error)
	GetShare(ctx context.Context, shareID string) (*types.KnowledgeBaseShare, error)
	GetShareByKBAndOrg(ctx context.Context, kbID string, orgID string) (*types.KnowledgeBaseShare, error)

	// Permission Check
	CheckUserKBPermission(ctx context.Context, kbID string, userID string) (types.OrgMemberRole, bool, error)
	HasKBPermission(ctx context.Context, kbID string, userID string, requiredRole types.OrgMemberRole) (bool, error)

	// Get source tenant for cross-tenant embedding
	GetKBSourceTenant(ctx context.Context, kbID string) (uint64, error)

	// Count shares for knowledge bases
	CountSharesByKnowledgeBaseIDs(ctx context.Context, kbIDs []string) (map[string]int64, error)
	// CountByOrganizations returns share counts per organization (for sidebar); excludes deleted KBs
	CountByOrganizations(ctx context.Context, orgIDs []string) (map[string]int64, error)
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
	ListByOrganizations(ctx context.Context, orgIDs []string) ([]*types.KnowledgeBaseShare, error)
	CountByOrganizations(ctx context.Context, orgIDs []string) (map[string]int64, error)

	// Query for user's accessible shared knowledge bases
	ListSharedKBsForUser(ctx context.Context, userID string) ([]*types.KnowledgeBaseShare, error)

	// Count shares
	CountSharesByKnowledgeBaseID(ctx context.Context, kbID string) (int64, error)
	CountSharesByKnowledgeBaseIDs(ctx context.Context, kbIDs []string) (map[string]int64, error)
}

// AgentShareService defines the agent sharing service interface
type AgentShareService interface {
	ShareAgent(ctx context.Context, agentID string, orgID string, userID string, tenantID uint64, permission types.OrgMemberRole) (*types.AgentShare, error)
	RemoveShare(ctx context.Context, shareID string, userID string) error
	ListSharesByAgent(ctx context.Context, agentID string) ([]*types.AgentShare, error)
	ListSharesByOrganization(ctx context.Context, orgID string) ([]*types.AgentShare, error)
	ListSharedAgents(ctx context.Context, userID string, currentTenantID uint64) ([]*types.SharedAgentInfo, error)
	ListSharedAgentsInOrganization(ctx context.Context, orgID string, userID string, currentTenantID uint64) ([]*types.OrganizationSharedAgentItem, error)
	// ListSharedAgentsInOrganizations returns per-org agent list (batch, for sidebar count merge).
	ListSharedAgentsInOrganizations(ctx context.Context, orgIDs []string, userID string, currentTenantID uint64) (map[string][]*types.OrganizationSharedAgentItem, error)
	// SetSharedAgentDisabledByMe sets whether the current tenant has "disabled" this shared agent for their conversation dropdown (per-user preference).
	SetSharedAgentDisabledByMe(ctx context.Context, tenantID uint64, agentID string, sourceTenantID uint64, disabled bool) error
	// GetSharedAgentForUser returns the shared agent by agentID if the user has access (source tenant is resolved from share); used to resolve KB scope for @ mention.
	GetSharedAgentForUser(ctx context.Context, userID string, currentTenantID uint64, agentID string) (*types.CustomAgent, error)
	// UserCanAccessKBViaSomeSharedAgent returns true if the user has at least one shared agent that can access the given KB (for opening KB detail from "通过智能体可见" list without passing agent_id).
	UserCanAccessKBViaSomeSharedAgent(ctx context.Context, userID string, currentTenantID uint64, kb *types.KnowledgeBase) (bool, error)
	GetShare(ctx context.Context, shareID string) (*types.AgentShare, error)
	GetShareByAgentAndOrg(ctx context.Context, agentID string, orgID string) (*types.AgentShare, error)
	// GetShareByAgentIDForUser returns one share for the given agentID that the user can access, excluding source_tenant_id == excludeTenantID (e.g. current tenant to get shared-from-other only).
	GetShareByAgentIDForUser(ctx context.Context, userID, agentID string, excludeTenantID uint64) (*types.AgentShare, error)
	// CountByOrganizations returns share counts per organization (for sidebar); excludes deleted agents
	CountByOrganizations(ctx context.Context, orgIDs []string) (map[string]int64, error)
}

// AgentShareRepository defines the agent sharing repository interface
type AgentShareRepository interface {
	Create(ctx context.Context, share *types.AgentShare) error
	GetByID(ctx context.Context, id string) (*types.AgentShare, error)
	GetByAgentAndOrg(ctx context.Context, agentID string, orgID string) (*types.AgentShare, error)
	Update(ctx context.Context, share *types.AgentShare) error
	Delete(ctx context.Context, id string) error
	DeleteByAgentIDAndSourceTenant(ctx context.Context, agentID string, sourceTenantID uint64) error
	DeleteByOrganizationID(ctx context.Context, orgID string) error
	ListByAgent(ctx context.Context, agentID string) ([]*types.AgentShare, error)
	ListByOrganization(ctx context.Context, orgID string) ([]*types.AgentShare, error)
	ListByOrganizations(ctx context.Context, orgIDs []string) ([]*types.AgentShare, error)
	ListSharedAgentsForUser(ctx context.Context, userID string) ([]*types.AgentShare, error)
	CountByOrganizations(ctx context.Context, orgIDs []string) (map[string]int64, error)
	// GetShareByAgentIDForUser returns one share for the given agentID that the user can access (user in org), excluding source_tenant_id == excludeTenantID.
	GetShareByAgentIDForUser(ctx context.Context, userID, agentID string, excludeTenantID uint64) (*types.AgentShare, error)
}

// TenantDisabledSharedAgentRepository stores per-tenant "disabled" agents (hidden from conversation dropdown; own and shared)
type TenantDisabledSharedAgentRepository interface {
	ListByTenantID(ctx context.Context, tenantID uint64) ([]*types.TenantDisabledSharedAgent, error)
	// ListDisabledOwnAgentIDs returns agent IDs that this tenant has disabled for their own agents (source_tenant_id = tenant_id)
	ListDisabledOwnAgentIDs(ctx context.Context, tenantID uint64) ([]string, error)
	Add(ctx context.Context, tenantID uint64, agentID string, sourceTenantID uint64) error
	Remove(ctx context.Context, tenantID uint64, agentID string, sourceTenantID uint64) error
}

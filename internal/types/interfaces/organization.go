package interfaces

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// OrganizationService defines the organization service interface.
//
// Plan 3 of #1303: organization membership is per-tenant, not per-user.
// Adding/removing/updating "members" therefore takes a tenant_id, and
// permission checks key on (org, tenant). RepresentativeUserID is
// informational and not load-bearing.
type OrganizationService interface {
	// Organization CRUD
	CreateOrganization(ctx context.Context, userID string, tenantID uint64, req *types.CreateOrganizationRequest) (*types.Organization, error)
	GetOrganization(ctx context.Context, id string) (*types.Organization, error)
	GetOrganizationByInviteCode(ctx context.Context, inviteCode string) (*types.Organization, error)
	ListTenantOrganizations(ctx context.Context, tenantID uint64) ([]*types.Organization, error)
	UpdateOrganization(ctx context.Context, id string, userID string, tenantID uint64, req *types.UpdateOrganizationRequest) (*types.Organization, error)
	DeleteOrganization(ctx context.Context, id string, userID string, tenantID uint64) error

	// Member Management (Plan 3: members are tenants)
	// AddTenantMember adds a tenant to the org with the given role; representativeUserID
	// is only used for display labelling.
	AddTenantMember(ctx context.Context, orgID string, tenantID uint64, representativeUserID string, role types.OrgMemberRole) error
	RemoveTenantMember(ctx context.Context, orgID string, memberTenantID uint64, operatorUserID string, operatorTenantID uint64) error
	UpdateTenantMemberRole(ctx context.Context, orgID string, memberTenantID uint64, role types.OrgMemberRole, operatorUserID string, operatorTenantID uint64) error
	ListTenantMembers(ctx context.Context, orgID string) ([]*types.OrganizationTenantMember, error)
	GetTenantMember(ctx context.Context, orgID string, tenantID uint64) (*types.OrganizationTenantMember, error)

	// Invite Code
	GenerateInviteCode(ctx context.Context, orgID string, userID string, tenantID uint64) (string, error)
	JoinByInviteCode(ctx context.Context, inviteCode string, userID string, tenantID uint64) (*types.Organization, error)
	// Searchable organizations (discovery)
	SearchSearchableOrganizations(ctx context.Context, tenantID uint64, query string, limit int) (*types.ListSearchableOrganizationsResponse, error)
	JoinByOrganizationID(ctx context.Context, orgID string, userID string, tenantID uint64, message string, requestedRole types.OrgMemberRole) (*types.Organization, error)

	// Join Requests (for organizations that require approval)
	SubmitJoinRequest(ctx context.Context, orgID string, userID string, tenantID uint64, message string, requestedRole types.OrgMemberRole) (*types.OrganizationJoinRequest, error)
	ListJoinRequests(ctx context.Context, orgID string) ([]*types.OrganizationJoinRequest, error)
	CountPendingJoinRequests(ctx context.Context, orgID string) (int64, error)
	ReviewJoinRequest(ctx context.Context, orgID string, requestID string, approved bool, reviewerID string, reviewerTenantID uint64, message string, assignRole *types.OrgMemberRole) error

	// Role Upgrade Requests (for existing tenants to request higher permissions)
	RequestRoleUpgrade(ctx context.Context, orgID string, userID string, tenantID uint64, requestedRole types.OrgMemberRole, message string) (*types.OrganizationJoinRequest, error)
	GetPendingUpgradeRequest(ctx context.Context, orgID string, tenantID uint64) (*types.OrganizationJoinRequest, error)

	// Permission Check (drive purely off the requesting tenant)
	IsTenantOrgAdmin(ctx context.Context, orgID string, tenantID uint64) (bool, error)
	GetTenantRoleInOrg(ctx context.Context, orgID string, tenantID uint64) (types.OrgMemberRole, error)
}

// OrganizationRepository defines the organization repository interface.
//
// Members are stored per-tenant in `organization_tenant_members`.
type OrganizationRepository interface {
	// Organization CRUD
	Create(ctx context.Context, org *types.Organization) error
	GetByID(ctx context.Context, id string) (*types.Organization, error)
	GetByInviteCode(ctx context.Context, inviteCode string) (*types.Organization, error)
	ListByTenantID(ctx context.Context, tenantID uint64) ([]*types.Organization, error)
	ListSearchable(ctx context.Context, query string, limit int) ([]*types.Organization, error)
	Update(ctx context.Context, org *types.Organization) error
	Delete(ctx context.Context, id string) error

	// Tenant member operations
	AddTenantMember(ctx context.Context, member *types.OrganizationTenantMember) error
	RemoveTenantMember(ctx context.Context, orgID string, tenantID uint64) error
	UpdateTenantMemberRole(ctx context.Context, orgID string, tenantID uint64, role types.OrgMemberRole) error
	ListTenantMembers(ctx context.Context, orgID string) ([]*types.OrganizationTenantMember, error)
	GetTenantMember(ctx context.Context, orgID string, tenantID uint64) (*types.OrganizationTenantMember, error)
	ListTenantMembersByTenantForOrgs(ctx context.Context, tenantID uint64, orgIDs []string) (map[string]*types.OrganizationTenantMember, error)
	CountTenantMembers(ctx context.Context, orgID string) (int64, error)

	// Invite code
	UpdateInviteCode(ctx context.Context, orgID string, inviteCode string, expiresAt *time.Time) error

	// Join requests (still keyed on the *requesting* user, but a single
	// pending request per (org, tenant) since the approve action grants
	// the whole tenant access).
	CreateJoinRequest(ctx context.Context, request *types.OrganizationJoinRequest) error
	GetJoinRequestByID(ctx context.Context, id string) (*types.OrganizationJoinRequest, error)
	GetPendingJoinRequestByTenant(ctx context.Context, orgID string, tenantID uint64) (*types.OrganizationJoinRequest, error)
	GetPendingRequestByTenantAndType(ctx context.Context, orgID string, tenantID uint64, requestType types.JoinRequestType) (*types.OrganizationJoinRequest, error)
	ListJoinRequests(ctx context.Context, orgID string, status types.JoinRequestStatus) ([]*types.OrganizationJoinRequest, error)
	CountJoinRequests(ctx context.Context, orgID string, status types.JoinRequestStatus) (int64, error)
	UpdateJoinRequestStatus(ctx context.Context, id string, status types.JoinRequestStatus, reviewedBy string, reviewMessage string) error
}

// KBShareService defines the knowledge base sharing service interface.
//
// Plan 3 of #1303: permission resolution keys on the caller's tenant
// (not user). The 3-dimension cap is applied inside CheckTenantKBPermission
// and is the canonical permission gate for shared KBs:
//
//   effective = min(share.Permission, tenant_org_role, tenant_role_cap)
//
// where tenant_role_cap pins tenant Viewers to OrgRoleViewer regardless
// of the org-level grant — Viewer in your own tenant must always be
// read-only on shared resources.
type KBShareService interface {
	// Share Management
	ShareKnowledgeBase(ctx context.Context, kbID string, orgID string, userID string, tenantID uint64, permission types.OrgMemberRole) (*types.KnowledgeBaseShare, error)
	UpdateSharePermission(ctx context.Context, shareID string, permission types.OrgMemberRole, userID string, tenantID uint64) error
	RemoveShare(ctx context.Context, shareID string, userID string, tenantID uint64) error

	// Query
	// ListSharesByKnowledgeBase lists shares for a KB; tenantID must own the KB (authz check).
	ListSharesByKnowledgeBase(ctx context.Context, kbID string, tenantID uint64) ([]*types.KnowledgeBaseShare, error)
	ListSharesByOrganization(ctx context.Context, orgID string) ([]*types.KnowledgeBaseShare, error)
	ListSharedKnowledgeBases(ctx context.Context, tenantID uint64, callerTenantRole types.TenantRole) ([]*types.SharedKnowledgeBaseInfo, error)
	ListSharedKnowledgeBasesInOrganization(ctx context.Context, orgID string, tenantID uint64, callerTenantRole types.TenantRole) ([]*types.OrganizationSharedKnowledgeBaseItem, error)
	// ListSharedKnowledgeBaseIDsByOrganizations returns per-org direct shared KB IDs (batch, for sidebar count).
	ListSharedKnowledgeBaseIDsByOrganizations(ctx context.Context, orgIDs []string, tenantID uint64) (map[string][]string, error)
	GetShare(ctx context.Context, shareID string) (*types.KnowledgeBaseShare, error)
	GetShareByKBAndOrg(ctx context.Context, kbID string, orgID string) (*types.KnowledgeBaseShare, error)

	// Permission Check (Plan 3: tenant-keyed). Returns the effective
	// OrgMemberRole the caller can use against the shared KB after the
	// 3-D cap, plus a bool indicating whether *any* share matches the
	// caller's tenant. callerTenantRole is the caller's tenant-RBAC
	// role and is used to apply the Viewer cap.
	CheckTenantKBPermission(ctx context.Context, kbID string, callerTenantID uint64, callerTenantRole types.TenantRole) (types.OrgMemberRole, bool, error)
	// HasTenantKBPermission is a thin "do I have at least N" wrapper over
	// CheckTenantKBPermission.
	HasTenantKBPermission(ctx context.Context, kbID string, callerTenantID uint64, callerTenantRole types.TenantRole, requiredRole types.OrgMemberRole) (bool, error)

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

	// Query for tenant's accessible shared knowledge bases
	ListSharedKBsForTenant(ctx context.Context, tenantID uint64) ([]*types.KnowledgeBaseShare, error)

	// Count shares
	CountSharesByKnowledgeBaseID(ctx context.Context, kbID string) (int64, error)
	CountSharesByKnowledgeBaseIDs(ctx context.Context, kbIDs []string) (map[string]int64, error)
}

// AgentShareService defines the agent sharing service interface.
//
// Plan 3 of #1303: visibility and access checks key on the caller's
// tenant. callerTenantRole flows through so the 3-D cap (tenant Viewer
// → at most OrgRoleViewer) can be applied consistently.
type AgentShareService interface {
	ShareAgent(ctx context.Context, agentID string, orgID string, userID string, tenantID uint64, permission types.OrgMemberRole) (*types.AgentShare, error)
	RemoveShare(ctx context.Context, shareID string, userID string, tenantID uint64) error
	ListSharesByAgent(ctx context.Context, agentID string) ([]*types.AgentShare, error)
	ListSharesByOrganization(ctx context.Context, orgID string) ([]*types.AgentShare, error)
	ListSharedAgents(ctx context.Context, tenantID uint64, callerTenantRole types.TenantRole) ([]*types.SharedAgentInfo, error)
	ListSharedAgentsInOrganization(ctx context.Context, orgID string, tenantID uint64, callerTenantRole types.TenantRole) ([]*types.OrganizationSharedAgentItem, error)
	// ListSharedAgentsInOrganizations returns per-org agent list (batch, for sidebar count merge).
	ListSharedAgentsInOrganizations(ctx context.Context, orgIDs []string, tenantID uint64, callerTenantRole types.TenantRole) (map[string][]*types.OrganizationSharedAgentItem, error)
	// SetSharedAgentDisabledByMe sets whether the current tenant has "disabled" this shared agent for their conversation dropdown (per-tenant preference; will be revisited in a follow-up PR).
	SetSharedAgentDisabledByMe(ctx context.Context, tenantID uint64, agentID string, sourceTenantID uint64, disabled bool) error
	// GetSharedAgentForTenant returns the shared agent by agentID if the caller's tenant has access; used to resolve KB scope for @ mention.
	GetSharedAgentForTenant(ctx context.Context, tenantID uint64, callerTenantRole types.TenantRole, agentID string) (*types.CustomAgent, error)
	// TenantCanAccessKBViaSomeSharedAgent returns true if the caller's tenant has at least one shared agent that can access the given KB (for opening KB detail from "通过智能体可见" list without passing agent_id).
	TenantCanAccessKBViaSomeSharedAgent(ctx context.Context, tenantID uint64, callerTenantRole types.TenantRole, kb *types.KnowledgeBase) (bool, error)
	GetShare(ctx context.Context, shareID string) (*types.AgentShare, error)
	GetShareByAgentAndOrg(ctx context.Context, agentID string, orgID string) (*types.AgentShare, error)
	// GetShareByAgentIDForTenant returns one share for the given agentID that the tenant can access, excluding source_tenant_id == excludeTenantID (e.g. caller's own tenant to get shared-from-other only).
	GetShareByAgentIDForTenant(ctx context.Context, tenantID uint64, agentID string, excludeTenantID uint64) (*types.AgentShare, error)
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
	ListSharedAgentsForTenant(ctx context.Context, tenantID uint64) ([]*types.AgentShare, error)
	CountByOrganizations(ctx context.Context, orgIDs []string) (map[string]int64, error)
	// GetShareByAgentIDForTenant returns one share for the given agentID that the tenant can access (tenant in org), excluding source_tenant_id == excludeTenantID.
	GetShareByAgentIDForTenant(ctx context.Context, tenantID uint64, agentID string, excludeTenantID uint64) (*types.AgentShare, error)
}

// TenantDisabledSharedAgentRepository stores per-tenant "disabled" agents (hidden from conversation dropdown; own and shared)
type TenantDisabledSharedAgentRepository interface {
	ListByTenantID(ctx context.Context, tenantID uint64) ([]*types.TenantDisabledSharedAgent, error)
	// ListDisabledOwnAgentIDs returns agent IDs that this tenant has disabled for their own agents (source_tenant_id = tenant_id)
	ListDisabledOwnAgentIDs(ctx context.Context, tenantID uint64) ([]string, error)
	Add(ctx context.Context, tenantID uint64, agentID string, sourceTenantID uint64) error
	Remove(ctx context.Context, tenantID uint64, agentID string, sourceTenantID uint64) error
}

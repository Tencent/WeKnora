package types

import (
	"time"

	"gorm.io/gorm"
)

// OrgMemberRole represents the role of an organization member
type OrgMemberRole string

const (
	// OrgRoleAdmin has full control over the organization and shared knowledge bases
	OrgRoleAdmin OrgMemberRole = "admin"
	// OrgRoleEditor can edit shared knowledge base content but cannot manage settings
	OrgRoleEditor OrgMemberRole = "editor"
	// OrgRoleViewer can only view and search shared knowledge bases
	OrgRoleViewer OrgMemberRole = "viewer"
)

// IsValid checks if the role is valid
func (r OrgMemberRole) IsValid() bool {
	switch r {
	case OrgRoleAdmin, OrgRoleEditor, OrgRoleViewer:
		return true
	default:
		return false
	}
}

// HasPermission checks if this role has at least the required permission level
func (r OrgMemberRole) HasPermission(required OrgMemberRole) bool {
	roleLevel := map[OrgMemberRole]int{
		OrgRoleAdmin:  3,
		OrgRoleEditor: 2,
		OrgRoleViewer: 1,
	}
	return roleLevel[r] >= roleLevel[required]
}

// Organization represents a collaboration organization for cross-tenant sharing
type Organization struct {
	// Unique identifier of the organization
	ID string `json:"id" gorm:"type:varchar(36);primaryKey"`
	// Name of the organization
	Name string `json:"name" gorm:"type:varchar(255);not null"`
	// Description of the organization
	Description string `json:"description" gorm:"type:text"`
	// Avatar URL for display in list and settings
	Avatar string `json:"avatar" gorm:"type:varchar(512)"`
	// User ID of the organization owner
	OwnerID string `json:"owner_id" gorm:"type:varchar(36);not null;index"`
	// Unique invitation code for joining the organization
	InviteCode string `json:"invite_code" gorm:"type:varchar(32);uniqueIndex"`
	// When the current invite code expires; nil means no expiry
	InviteCodeExpiresAt *time.Time `json:"invite_code_expires_at" gorm:"type:timestamp with time zone"`
	// Invite link validity in days: 0=never, 1/7/30
	InviteCodeValidityDays int `json:"invite_code_validity_days" gorm:"default:7"`
	// Whether joining requires admin approval
	RequireApproval bool `json:"require_approval" gorm:"default:false"`
	// Whether the space is open for search (discoverable; non-members can search and join by org ID)
	Searchable bool `json:"searchable" gorm:"default:false"`
	// Max members allowed; 0 means no limit
	MemberLimit int `json:"member_limit" gorm:"default:50"`
	// Creation time
	CreatedAt time.Time `json:"created_at"`
	// Last updated time
	UpdatedAt time.Time `json:"updated_at"`
	// Deletion time (soft delete)
	DeletedAt gorm.DeletedAt `json:"deleted_at" gorm:"index"`

	// Associations (not stored in database)
	Owner   *User                `json:"owner,omitempty" gorm:"foreignKey:OwnerID"`
	Members []OrganizationMember `json:"members,omitempty" gorm:"foreignKey:OrganizationID"`
	Shares  []KnowledgeBaseShare `json:"shares,omitempty" gorm:"foreignKey:OrganizationID"`
}

// TableName returns the table name for GORM
func (Organization) TableName() string {
	return "organizations"
}

// OrganizationMember represents a member of an organization
type OrganizationMember struct {
	// Unique identifier
	ID string `json:"id" gorm:"type:varchar(36);primaryKey"`
	// Organization ID
	OrganizationID string `json:"organization_id" gorm:"type:varchar(36);not null;index"`
	// User ID of the member
	UserID string `json:"user_id" gorm:"type:varchar(36);not null;index"`
	// Tenant ID that the member belongs to
	TenantID uint64 `json:"tenant_id" gorm:"not null;index"`
	// Role in the organization (admin/editor/viewer)
	Role OrgMemberRole `json:"role" gorm:"type:varchar(32);not null;default:'viewer'"`
	// Creation time
	CreatedAt time.Time `json:"created_at"`
	// Last updated time
	UpdatedAt time.Time `json:"updated_at"`

	// Associations (not stored in database)
	Organization *Organization `json:"organization,omitempty" gorm:"foreignKey:OrganizationID"`
	User         *User         `json:"user,omitempty" gorm:"foreignKey:UserID"`
}

// TableName returns the table name for GORM
func (OrganizationMember) TableName() string {
	return "organization_members"
}

// JoinRequestStatus represents the status of a join request
type JoinRequestStatus string

const (
	JoinRequestStatusPending  JoinRequestStatus = "pending"
	JoinRequestStatusApproved JoinRequestStatus = "approved"
	JoinRequestStatusRejected JoinRequestStatus = "rejected"
)

// JoinRequestType represents the type of a join request
type JoinRequestType string

const (
	// JoinRequestTypeJoin is for new member join requests
	JoinRequestTypeJoin JoinRequestType = "join"
	// JoinRequestTypeUpgrade is for role upgrade requests from existing members
	JoinRequestTypeUpgrade JoinRequestType = "upgrade"
)

// OrganizationJoinRequest represents a request to join an organization or upgrade role
type OrganizationJoinRequest struct {
	// Unique identifier
	ID string `json:"id" gorm:"type:varchar(36);primaryKey"`
	// Organization ID
	OrganizationID string `json:"organization_id" gorm:"type:varchar(36);not null;index"`
	// User ID of the requester
	UserID string `json:"user_id" gorm:"type:varchar(36);not null;index"`
	// Tenant ID of the requester
	TenantID uint64 `json:"tenant_id" gorm:"not null"`
	// Type of request: 'join' for new member, 'upgrade' for role upgrade
	RequestType JoinRequestType `json:"request_type" gorm:"type:varchar(32);not null;default:'join';index"`
	// Previous role before upgrade (only for upgrade requests)
	PrevRole OrgMemberRole `json:"prev_role" gorm:"column:prev_role;type:varchar(32)"`
	// Role requested by the applicant (admin/editor/viewer)
	RequestedRole OrgMemberRole `json:"requested_role" gorm:"type:varchar(32);not null;default:'viewer'"`
	// Status of the request
	Status JoinRequestStatus `json:"status" gorm:"type:varchar(32);not null;default:'pending';index"`
	// Optional message from the requester
	Message string `json:"message" gorm:"type:text"`
	// User ID of the admin who reviewed the request
	ReviewedBy string `json:"reviewed_by" gorm:"type:varchar(36)"`
	// Time when the request was reviewed
	ReviewedAt *time.Time `json:"reviewed_at"`
	// Optional message from the reviewer
	ReviewMessage string `json:"review_message" gorm:"type:text"`
	// Creation time
	CreatedAt time.Time `json:"created_at"`
	// Last updated time
	UpdatedAt time.Time `json:"updated_at"`

	// Associations (not stored in database)
	Organization *Organization `json:"organization,omitempty" gorm:"foreignKey:OrganizationID"`
	User         *User         `json:"user,omitempty" gorm:"foreignKey:UserID"`
	Reviewer     *User         `json:"reviewer,omitempty" gorm:"foreignKey:ReviewedBy"`
}

// TableName returns the table name for GORM
func (OrganizationJoinRequest) TableName() string {
	return "organization_join_requests"
}

// KnowledgeBaseShare represents a sharing record of a knowledge base to an organization
type KnowledgeBaseShare struct {
	// Unique identifier
	ID string `json:"id" gorm:"type:varchar(36);primaryKey"`
	// Knowledge base ID being shared
	KnowledgeBaseID string `json:"knowledge_base_id" gorm:"type:varchar(36);not null;index"`
	// Organization ID receiving the share
	OrganizationID string `json:"organization_id" gorm:"type:varchar(36);not null;index"`
	// User ID who shared the knowledge base
	SharedByUserID string `json:"shared_by_user_id" gorm:"type:varchar(36);not null"`
	// Original tenant ID of the knowledge base (for cross-tenant embedding model access)
	SourceTenantID uint64 `json:"source_tenant_id" gorm:"not null;index"`
	// Permission level (admin/editor/viewer)
	Permission OrgMemberRole `json:"permission" gorm:"type:varchar(32);not null;default:'viewer'"`
	// Creation time
	CreatedAt time.Time `json:"created_at"`
	// Last updated time
	UpdatedAt time.Time `json:"updated_at"`
	// Deletion time (soft delete)
	DeletedAt gorm.DeletedAt `json:"deleted_at" gorm:"index"`

	// Associations (not stored in database)
	KnowledgeBase *KnowledgeBase `json:"knowledge_base,omitempty" gorm:"foreignKey:KnowledgeBaseID"`
	Organization  *Organization  `json:"organization,omitempty" gorm:"foreignKey:OrganizationID"`
}

// TableName returns the table name for GORM
func (KnowledgeBaseShare) TableName() string {
	return "kb_shares"
}

// SharedKnowledgeBaseInfo represents a shared knowledge base with additional sharing info
type SharedKnowledgeBaseInfo struct {
	KnowledgeBase  *KnowledgeBase `json:"knowledge_base"`
	ShareID        string         `json:"share_id"`
	OrganizationID string         `json:"organization_id"`
	OrgName        string         `json:"org_name"`
	Permission     OrgMemberRole  `json:"permission"`
	SourceTenantID uint64         `json:"source_tenant_id"`
	SharedAt       time.Time      `json:"shared_at"`
}

// ----------------------
// Request/Response Types
// ----------------------

// CreateOrganizationRequest represents a request to create an organization
type CreateOrganizationRequest struct {
	Name                   string `json:"name" binding:"required,min=1,max=255"`
	Description            string `json:"description" binding:"max=1000"`
	Avatar                 string `json:"avatar" binding:"omitempty,max=512"` // optional avatar URL
	InviteCodeValidityDays *int   `json:"invite_code_validity_days"`          // optional: 0=never, 1, 7, 30; default 7
	MemberLimit            *int   `json:"member_limit"`                       // optional: max members; 0=unlimited; default 50
}

// UpdateOrganizationRequest represents a request to update an organization
type UpdateOrganizationRequest struct {
	Name                   *string `json:"name" binding:"omitempty,min=1,max=255"`
	Description            *string `json:"description" binding:"omitempty,max=1000"`
	Avatar                 *string `json:"avatar" binding:"omitempty,max=512"` // optional avatar URL
	RequireApproval        *bool   `json:"require_approval"`
	Searchable             *bool   `json:"searchable"`             // open for search so others can discover and join
	InviteCodeValidityDays *int    `json:"invite_code_validity_days"` // 0=never, 1, 7, 30
	MemberLimit            *int    `json:"member_limit"`           // max members; 0=unlimited
}

// AddMemberRequest represents a request to add a member to an organization
type AddMemberRequest struct {
	Email string        `json:"email" binding:"required,email"`
	Role  OrgMemberRole `json:"role" binding:"required"`
}

// UpdateMemberRoleRequest represents a request to update a member's role
type UpdateMemberRoleRequest struct {
	Role OrgMemberRole `json:"role" binding:"required"`
}

// JoinOrganizationRequest represents a request to join an organization via invite code
type JoinOrganizationRequest struct {
	InviteCode string `json:"invite_code" binding:"required,min=8,max=32"`
}

// SubmitJoinRequestRequest represents a request to submit a join request for approval
type SubmitJoinRequestRequest struct {
	InviteCode string        `json:"invite_code" binding:"required,min=8,max=32"`
	Message    string        `json:"message" binding:"max=500"`
	Role       OrgMemberRole `json:"role"` // Optional: role the applicant requests (admin/editor/viewer); default viewer
}

// ReviewJoinRequestRequest represents a request to review a join request
type ReviewJoinRequestRequest struct {
	Approved bool          `json:"approved"`
	Message  string        `json:"message" binding:"max=500"`
	Role     OrgMemberRole `json:"role"` // Optional: role to assign when approving; overrides applicant's requested role
}

// RequestRoleUpgradeRequest represents a request to upgrade role in an organization
type RequestRoleUpgradeRequest struct {
	RequestedRole OrgMemberRole `json:"requested_role" binding:"required"` // The role user wants to upgrade to
	Message       string        `json:"message" binding:"max=500"`         // Optional message explaining the reason
}

// InviteMemberRequest represents a request to directly invite a user to organization
type InviteMemberRequest struct {
	UserID string        `json:"user_id" binding:"required"` // User ID to invite
	Role   OrgMemberRole `json:"role" binding:"required"`    // Role to assign: admin/editor/viewer
}

// ShareKnowledgeBaseRequest represents a request to share a knowledge base
type ShareKnowledgeBaseRequest struct {
	OrganizationID string        `json:"organization_id" binding:"required"`
	Permission     OrgMemberRole `json:"permission" binding:"required"`
}

// UpdateSharePermissionRequest represents a request to update share permission
type UpdateSharePermissionRequest struct {
	Permission OrgMemberRole `json:"permission" binding:"required"`
}

// OrganizationResponse represents an organization in API responses
type OrganizationResponse struct {
	ID                      string     `json:"id"`
	Name                    string     `json:"name"`
	Description             string     `json:"description"`
	Avatar                  string     `json:"avatar,omitempty"`
	OwnerID                 string     `json:"owner_id"`
	InviteCode              string     `json:"invite_code,omitempty"`
	InviteCodeExpiresAt     *time.Time `json:"invite_code_expires_at,omitempty"`
	InviteCodeValidityDays  int        `json:"invite_code_validity_days"`
	RequireApproval         bool       `json:"require_approval"`
	Searchable              bool       `json:"searchable"`
	MemberLimit             int        `json:"member_limit"` // 0 = unlimited
	MemberCount             int        `json:"member_count"`
	ShareCount              int        `json:"share_count"`                // 共享到该组织的知识库数量
	PendingJoinRequestCount int        `json:"pending_join_request_count"` // 待审批加入申请数（仅管理员可见）
	IsOwner                 bool       `json:"is_owner"`
	MyRole                  string     `json:"my_role,omitempty"`
	HasPendingUpgrade       bool       `json:"has_pending_upgrade"` // 当前用户是否有待处理的权限升级申请
	CreatedAt               time.Time  `json:"created_at"`
	UpdatedAt               time.Time  `json:"updated_at"`
}

// OrganizationMemberResponse represents a member in API responses
type OrganizationMemberResponse struct {
	ID       string    `json:"id"`
	UserID   string    `json:"user_id"`
	Username string    `json:"username"`
	Email    string    `json:"email"`
	Avatar   string    `json:"avatar"`
	Role     string    `json:"role"`
	TenantID uint64    `json:"tenant_id"`
	JoinedAt time.Time `json:"joined_at"`
}

// KnowledgeBaseShareResponse represents a share record in API responses
type KnowledgeBaseShareResponse struct {
	ID                string    `json:"id"`
	KnowledgeBaseID   string    `json:"knowledge_base_id"`
	KnowledgeBaseName string    `json:"knowledge_base_name"`
	KnowledgeBaseType string    `json:"knowledge_base_type"`
	KnowledgeCount    int64     `json:"knowledge_count"`
	ChunkCount        int64     `json:"chunk_count"`
	OrganizationID    string    `json:"organization_id"`
	OrganizationName  string    `json:"organization_name"`
	SharedByUserID    string    `json:"shared_by_user_id"`
	SharedByUsername  string    `json:"shared_by_username"`
	SourceTenantID    uint64    `json:"source_tenant_id"`
	Permission        string    `json:"permission"`     // Share permission (what the space was granted: viewer/editor)
	MyRoleInOrg       string    `json:"my_role_in_org"` // Current user's role in this organization (admin/editor/viewer)
	MyPermission      string    `json:"my_permission"`  // Effective permission for current user = min(Permission, MyRoleInOrg)
	CreatedAt         time.Time `json:"created_at"`
	RequireApproval   bool      `json:"require_approval"`
}

// ListOrganizationsResponse represents the response for listing organizations
type ListOrganizationsResponse struct {
	Organizations []OrganizationResponse `json:"organizations"`
	Total         int64                  `json:"total"`
}

// SearchableOrganizationItem is a searchable org item for discovery (no invite code)
type SearchableOrganizationItem struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Description       string `json:"description"`
	Avatar            string `json:"avatar,omitempty"`
	MemberCount       int    `json:"member_count"`
	ShareCount        int    `json:"share_count"`
	IsAlreadyMember   bool   `json:"is_already_member"`
	RequireApproval   bool   `json:"require_approval"`
}

// ListSearchableOrganizationsResponse is the response for searching discoverable organizations
type ListSearchableOrganizationsResponse struct {
	Organizations []SearchableOrganizationItem `json:"organizations"`
	Total         int64                         `json:"total"`
}

// JoinByOrganizationIDRequest is used to join a searchable organization by ID (no invite code)
type JoinByOrganizationIDRequest struct {
	OrganizationID string        `json:"organization_id" binding:"required"`
	Message        string        `json:"message" binding:"max=500"`         // Optional message for join request
	Role           OrgMemberRole `json:"role"`                               // Optional: requested role (admin/editor/viewer); default viewer
}

// JoinRequestResponse represents a join request in API responses
type JoinRequestResponse struct {
	ID            string     `json:"id"`
	UserID        string     `json:"user_id"`
	Username      string     `json:"username"`
	Email         string     `json:"email"`
	Message       string     `json:"message"`
	RequestType   string     `json:"request_type"`   // 'join' or 'upgrade'
	PrevRole      string     `json:"prev_role"`      // Previous role (only for upgrade requests)
	RequestedRole string     `json:"requested_role"` // Role the applicant requested (admin/editor/viewer)
	Status        string     `json:"status"`
	CreatedAt     time.Time  `json:"created_at"`
	ReviewedAt    *time.Time `json:"reviewed_at,omitempty"`
}

// ListJoinRequestsResponse represents the response for listing join requests
type ListJoinRequestsResponse struct {
	Requests []JoinRequestResponse `json:"requests"`
	Total    int64                 `json:"total"`
}

// ListMembersResponse represents the response for listing members
type ListMembersResponse struct {
	Members []OrganizationMemberResponse `json:"members"`
	Total   int64                        `json:"total"`
}

// ListSharesResponse represents the response for listing shares
type ListSharesResponse struct {
	Shares []KnowledgeBaseShareResponse `json:"shares"`
	Total  int64                        `json:"total"`
}

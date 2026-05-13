package types

import (
	"time"

	"gorm.io/gorm"
)

// TenantRole represents a user's role inside a single tenant.
//
// Tenant roles govern intra-tenant authority (who can create/edit/delete
// resources, manage tenant settings, etc.) and are orthogonal to the
// OrgMemberRole defined in organization.go, which governs cross-tenant
// sharing. A user may therefore carry different TenantRole values in
// different tenants (one TenantMember row per (user, tenant) pair).
type TenantRole string

const (
	// TenantRoleOwner has full control over the tenant, including tenant
	// deletion, ownership transfer, and rotating the tenant API key.
	TenantRoleOwner TenantRole = "owner"
	// TenantRoleAdmin manages users, integrations, and tenant-scoped
	// configuration such as model providers, vector stores, MCP services
	// and IM channels, but cannot delete the tenant or change Owners.
	TenantRoleAdmin TenantRole = "admin"
	// TenantRoleContributor can create knowledge bases and agents, and edit
	// the ones they created. They have read access to everything else in
	// the tenant.
	TenantRoleContributor TenantRole = "contributor"
	// TenantRoleViewer has read-only access to tenant resources and can
	// run agents that are explicitly marked as runnable by viewers.
	TenantRoleViewer TenantRole = "viewer"
)

// tenantRoleLevel maps each role to a numeric level used for hierarchy
// comparisons. Higher means more privileged. Levels are spaced by 10 so
// new roles can be inserted between existing ones if needed.
var tenantRoleLevel = map[TenantRole]int{
	TenantRoleOwner:       40,
	TenantRoleAdmin:       30,
	TenantRoleContributor: 20,
	TenantRoleViewer:      10,
}

// IsValid reports whether r is one of the four defined tenant roles.
func (r TenantRole) IsValid() bool {
	_, ok := tenantRoleLevel[r]
	return ok
}

// Level returns the numeric privilege level of the role. Unknown roles
// return 0, which is strictly less than any defined role.
func (r TenantRole) Level() int {
	return tenantRoleLevel[r]
}

// HasPermission reports whether r is at least as privileged as required.
// Used by RequireRole-style middleware to gate endpoints.
func (r TenantRole) HasPermission(required TenantRole) bool {
	return r.Level() >= required.Level()
}

// TenantMemberStatus enumerates the lifecycle states of a membership row.
type TenantMemberStatus string

const (
	// TenantMemberStatusActive is the normal membership state; the user
	// can authenticate into the tenant and is subject to their role.
	TenantMemberStatusActive TenantMemberStatus = "active"
	// TenantMemberStatusInvited represents a pending invitation that has
	// not yet been accepted. The auth middleware treats this as "not a
	// member" until the status flips to active.
	TenantMemberStatusInvited TenantMemberStatus = "invited"
	// TenantMemberStatusSuspended is an admin-revoked membership. The
	// row is preserved for audit trail but the user cannot authenticate
	// into the tenant.
	TenantMemberStatusSuspended TenantMemberStatus = "suspended"
)

// TenantMember represents the (user, tenant) membership record that
// carries the user's TenantRole for that specific tenant.
//
// A user has one TenantMember row per tenant they belong to. The home
// tenant recorded on User.TenantID is always one of these rows; additional
// rows are created when an admin adds the user to another tenant.
type TenantMember struct {
	// Surrogate primary key.
	ID uint64 `json:"id" gorm:"primaryKey;autoIncrement"`
	// UserID references users.id. Together with TenantID forms the logical
	// key enforced by the partial unique index uniq_user_tenant.
	UserID string `json:"user_id" gorm:"type:varchar(36);not null;index"`
	// TenantID references tenants.id.
	TenantID uint64 `json:"tenant_id" gorm:"not null;index"`
	// Role held by the user inside this tenant.
	Role TenantRole `json:"role" gorm:"type:varchar(20);not null;default:'contributor'"`
	// Status controls whether this membership is honoured by the auth
	// middleware; see TenantMemberStatus constants.
	Status TenantMemberStatus `json:"status" gorm:"type:varchar(20);not null;default:'active'"`
	// InvitedBy records the user ID of the admin who created this row via
	// an invitation flow. Nil for rows created by self-service registration.
	InvitedBy *string `json:"invited_by,omitempty" gorm:"type:varchar(36)"`
	// JoinedAt is when the membership became active.
	JoinedAt  time.Time      `json:"joined_at"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `json:"deleted_at" gorm:"index"`
}

// TableName binds TenantMember to the tenant_members table.
func (TenantMember) TableName() string {
	return "tenant_members"
}

// Membership is the login-response-friendly projection of a TenantMember
// joined with tenant name. Returned as part of LoginResponse so the
// frontend can render a tenant switcher and gate UI by role.
type Membership struct {
	TenantID   uint64     `json:"tenant_id"`
	TenantName string     `json:"tenant_name"`
	Role       TenantRole `json:"role"`
}

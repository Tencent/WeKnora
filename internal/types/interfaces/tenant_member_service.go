package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// TenantMemberService is the business-logic layer over TenantMemberRepository.
// It enforces tenant-RBAC invariants such as "every tenant with members must
// keep at least one active Owner". HTTP handlers and other services call this
// interface rather than the repository directly so the invariants cannot be
// silently bypassed.
type TenantMemberService interface {
	// AddMember inserts a new active membership row. Returns an error if
	// (user, tenant) already has an active membership.
	AddMember(ctx context.Context, userID string, tenantID uint64, role types.TenantRole, invitedBy *string) (*types.TenantMember, error)

	// EnsureOwner is an idempotent helper used by the registration flow:
	// if the user already has an active membership in the tenant, return
	// it; otherwise create one with role=owner. This is the common path
	// for self-service registration where the registrant becomes the
	// Owner of the tenant their account just created.
	EnsureOwner(ctx context.Context, userID string, tenantID uint64) (*types.TenantMember, error)

	// GetMembership returns the active (user, tenant) membership, or
	// (nil, nil) if no such row exists.
	GetMembership(ctx context.Context, userID string, tenantID uint64) (*types.TenantMember, error)

	// ListByUser returns every active membership owned by the user.
	ListByUser(ctx context.Context, userID string) ([]*types.TenantMember, error)

	// ListByTenant returns every active membership inside the tenant.
	ListByTenant(ctx context.Context, tenantID uint64) ([]*types.TenantMember, error)

	// ListMembersPage lists members with pagination. Query matches email or
	// username case-insensitively; empty query returns full tenant list slice.
	ListMembersPage(ctx context.Context, tenantID uint64, query string, page, pageSize int) ([]*types.TenantMember, int64, error)

	// HasAnyMembers reports whether the tenant has at least one active
	// member. The auth middleware uses this to recover orphan tenants
	// (e.g. API-key-only tenants that never had a human member): the
	// first human authenticating into such a tenant is auto-promoted
	// to Owner.
	HasAnyMembers(ctx context.Context, tenantID uint64) (bool, error)

	// UpdateRole changes the role of an existing membership while
	// enforcing the "cannot demote the last active Owner" invariant.
	UpdateRole(ctx context.Context, userID string, tenantID uint64, newRole types.TenantRole) error

	// RemoveMember soft-deletes the membership while enforcing the
	// "cannot remove the last active Owner" invariant.
	RemoveMember(ctx context.Context, userID string, tenantID uint64) error
}

// MemberPasswordResetAuditor 是 TenantMemberService 实现的可选窄接口：
// 实现它的服务能在成员密码重置成功后落一条 rbac.member_password_reset
// 审计。
//
// 单独成接口而不把方法并进 TenantMemberService，是为了不破坏所有逐方法
// 显式实现该接口的既有实现/测试替身（例如中间件的角色测试）。handler 在
// 重置成功后对该窄接口做类型断言；审计尽力而为，与 emitAudit 的既有
// 语义一致。
type MemberPasswordResetAuditor interface {
	AuditMemberPasswordReset(ctx context.Context, tenantID uint64, targetUserID string, targetRole types.TenantRole)
}

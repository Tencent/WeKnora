package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// TenantMemberRepository persists (user, tenant) membership rows that
// carry the per-tenant TenantRole.
//
// All methods operate on active rows only (deleted_at IS NULL) unless the
// docstring explicitly says otherwise. Soft deletion is handled by GORM
// via the DeletedAt field on TenantMember.
type TenantMemberRepository interface {
	// Create inserts a new active membership row. Caller is responsible
	// for ensuring no other active row exists for the same (user, tenant)
	// pair; the partial unique index will return a conflict error otherwise.
	Create(ctx context.Context, member *types.TenantMember) error

	// Get returns the active membership for the given (user, tenant) pair,
	// or (nil, nil) if no such row exists.
	Get(ctx context.Context, userID string, tenantID uint64) (*types.TenantMember, error)

	// ListByUser returns every active membership owned by the given user,
	// ordered by joined_at ascending.
	ListByUser(ctx context.Context, userID string) ([]*types.TenantMember, error)

	// ListByTenant returns every active membership inside the given tenant,
	// ordered by joined_at ascending.
	ListByTenant(ctx context.Context, tenantID uint64) ([]*types.TenantMember, error)

	// UpdateRole changes the role of an existing active membership. Returns
	// gorm.ErrRecordNotFound if no active row matches.
	UpdateRole(ctx context.Context, userID string, tenantID uint64, role types.TenantRole) error

	// SoftDelete marks the active membership as deleted. The user record
	// itself is untouched.
	SoftDelete(ctx context.Context, userID string, tenantID uint64) error

	// CountActiveOwners reports how many active rows in the tenant carry
	// the owner role. Used by service-layer invariant checks ("cannot
	// remove the last owner").
	CountActiveOwners(ctx context.Context, tenantID uint64) (int64, error)

	// HasAnyMembers reports whether the tenant has at least one active
	// membership. Used by the auth middleware to decide whether to
	// auto-promote the first authenticating human in an API-key-only tenant.
	HasAnyMembers(ctx context.Context, tenantID uint64) (bool, error)
}

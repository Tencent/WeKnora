package dto

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// RoleFromContext returns the caller's tenant role from ctx.
func RoleFromContext(ctx context.Context) types.TenantRole {
	return types.TenantRoleFromContext(ctx)
}

// CanViewIntegrationSecrets is true for Admin+ (includes Owner).
func CanViewIntegrationSecrets(ctx context.Context) bool {
	return RoleFromContext(ctx).HasPermission(types.TenantRoleAdmin)
}

// RoleCanViewTenantAPIKey is true for Owner+ only.
func RoleCanViewTenantAPIKey(role types.TenantRole) bool {
	return role.HasPermission(types.TenantRoleOwner)
}

// CanViewTenantAPIKey is true for Owner+ only.
func CanViewTenantAPIKey(ctx context.Context) bool {
	return RoleCanViewTenantAPIKey(RoleFromContext(ctx))
}

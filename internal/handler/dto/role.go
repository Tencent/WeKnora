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

// CanViewTenantAPIKey is true for Owner+ only.
func CanViewTenantAPIKey(ctx context.Context) bool {
	return RoleFromContext(ctx).HasPermission(types.TenantRoleOwner)
}

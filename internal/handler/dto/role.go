package dto

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// RoleFromContext returns the caller's tenant role from ctx.
func RoleFromContext(ctx context.Context) types.TenantRole {
	return types.TenantRoleFromContext(ctx)
}

// CanViewIntegrationSecrets is true for Admin+ tenant members and for API keys
// with full tenant access or the manage_tenant_settings capability.
func CanViewIntegrationSecrets(ctx context.Context) bool {
	if RoleFromContext(ctx).HasPermission(types.TenantRoleAdmin) {
		return true
	}
	return apiKeyCanManageIntegrationSecrets(ctx)
}

func apiKeyCanManageIntegrationSecrets(ctx context.Context) bool {
	scope, ok := types.TenantAPIKeyScopeFromContext(ctx)
	if !ok {
		return false
	}
	if scope.FullAccess {
		return true
	}
	return scope.HasCapability(types.APIKeyCapabilityManageTenantSettings)
}

// RoleCanViewTenantAPIKey is true for Owner+ only.
func RoleCanViewTenantAPIKey(role types.TenantRole) bool {
	return role.HasPermission(types.TenantRoleOwner)
}

// CanViewTenantAPIKey is true for Owner+ only.
func CanViewTenantAPIKey(ctx context.Context) bool {
	return RoleCanViewTenantAPIKey(RoleFromContext(ctx))
}

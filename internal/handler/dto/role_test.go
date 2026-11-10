package dto

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestCanViewIntegrationSecretsAdminRole(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantRoleContextKey, types.TenantRoleAdmin)
	if !CanViewIntegrationSecrets(ctx) {
		t.Fatal("admin should view integration secrets")
	}
}

func TestCanViewIntegrationSecretsViewerDenied(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantRoleContextKey, types.TenantRoleViewer)
	if CanViewIntegrationSecrets(ctx) {
		t.Fatal("viewer should not view integration secrets")
	}
}

func TestCanViewIntegrationSecretsScopedAPIKeyWithManageTenantSettings(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		Capabilities: types.StringArray{string(types.APIKeyCapabilityManageTenantSettings)},
	})
	ctx = context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleViewer)
	if !CanViewIntegrationSecrets(ctx) {
		t.Fatal("manage_tenant_settings API key should view integration secrets")
	}
}

func TestCanViewIntegrationSecretsScopedAPIKeyWithoutCapabilityDenied(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		Capabilities: types.StringArray{string(types.APIKeyCapabilityChat)},
	})
	ctx = context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleViewer)
	if CanViewIntegrationSecrets(ctx) {
		t.Fatal("chat-only API key should not view integration secrets")
	}
}

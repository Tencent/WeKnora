package handler

import (
	"context"
	"testing"
)

func TestValidateTenantAPIKeyRequestRequiresCapabilitiesForScopedKey(t *testing.T) {
	err := validateTenantAPIKeyRequest(context.Background(), nil, 1, tenantAPIKeyCreateRequest{
		Name:       "integration",
		FullAccess: false,
	})
	if err == nil {
		t.Fatal("expected validation error for scoped key without capabilities")
	}
}

func TestValidateTenantAPIKeyRequestAllowsFullAccessWithoutCapabilities(t *testing.T) {
	if err := validateTenantAPIKeyRequest(context.Background(), nil, 1, tenantAPIKeyCreateRequest{
		Name:       "owner",
		FullAccess: true,
	}); err != nil {
		t.Fatalf("full-access key validation error = %v", err)
	}
}

func TestValidateTenantAPIKeyRequestAcceptsScopedKeyWithCapability(t *testing.T) {
	if err := validateTenantAPIKeyRequest(context.Background(), nil, 1, tenantAPIKeyCreateRequest{
		Name:         "chat",
		FullAccess:   false,
		Capabilities: []string{"chat"},
	}); err != nil {
		t.Fatalf("scoped key validation error = %v", err)
	}
}

package handler

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
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

// TestValidateTenantAPIKeyKnowledgeBaseOwnership 验证知识库白名单的租户边界。
// 输入同租户、其他租户和不存在的知识库 ID；仅同租户 ID 应通过。
func TestValidateTenantAPIKeyKnowledgeBaseOwnership(t *testing.T) {
	lookup := func(_ context.Context, id string) (*types.KnowledgeBase, error) {
		switch id {
		case "kb-owned":
			return &types.KnowledgeBase{ID: id, TenantID: 42}, nil
		case "kb-other":
			return &types.KnowledgeBase{ID: id, TenantID: 43}, nil
		default:
			return nil, context.Canceled
		}
	}

	if err := validateTenantAPIKeyKnowledgeBaseIDsWithLookup(context.Background(), 42, []string{"kb-owned"}, lookup); err != nil {
		t.Fatalf("owned knowledge base validation error = %v", err)
	}
	if err := validateTenantAPIKeyKnowledgeBaseIDsWithLookup(context.Background(), 42, []string{"kb-other"}, lookup); err == nil {
		t.Fatal("expected cross-tenant knowledge base to be rejected")
	}
	if err := validateTenantAPIKeyKnowledgeBaseIDsWithLookup(context.Background(), 42, []string{"kb-missing"}, lookup); err == nil {
		t.Fatal("expected missing knowledge base to be rejected")
	}
}

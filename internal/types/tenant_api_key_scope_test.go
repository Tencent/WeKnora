package types

import (
	"context"
	"testing"
)

func TestAuthorizeTenantAPIKeyKnowledgeTargetsRejectsKnowledgeIDs(t *testing.T) {
	ctx := WithTenantAPIKeyScope(context.Background(), TenantAPIKeyScope{
		KnowledgeBaseIDs: StringArray{"kb-1"},
	})
	err := AuthorizeTenantAPIKeyKnowledgeTargets(ctx, []string{"kb-1"}, []string{"doc-1"})
	if err == nil {
		t.Fatal("expected forbidden when knowledge_ids supplied under KB-restricted key")
	}
}

func TestAuthorizeTenantAPIKeyKnowledgeTargetsAllowsUnspecifiedTargets(t *testing.T) {
	ctx := WithTenantAPIKeyScope(context.Background(), TenantAPIKeyScope{
		KnowledgeBaseIDs: StringArray{"kb-1"},
	})
	err := AuthorizeTenantAPIKeyKnowledgeTargets(ctx, nil, nil)
	if err != nil {
		t.Fatalf("expected unspecified targets to be allowed, got %v", err)
	}
}

func TestAuthorizeTenantAPIKeyOptionalTagIDsRejectsTags(t *testing.T) {
	ctx := WithTenantAPIKeyScope(context.Background(), TenantAPIKeyScope{
		KnowledgeBaseIDs: StringArray{"kb-1"},
	})
	err := AuthorizeTenantAPIKeyOptionalTagIDs(ctx, []string{"tag-1"})
	if err == nil {
		t.Fatal("expected forbidden when tag_ids supplied under KB-restricted key")
	}
}

func TestFilterKnowledgeBasesForTenantAPIKeyScopeIntersectsAgentDefaults(t *testing.T) {
	ctx := WithTenantAPIKeyScope(context.Background(), TenantAPIKeyScope{
		KnowledgeBaseIDs: StringArray{"kb-1", "kb-2"},
	})
	got, err := FilterKnowledgeBasesForTenantAPIKeyScope(ctx, nil, []string{"kb-2", "kb-3"})
	if err != nil {
		t.Fatalf("FilterKnowledgeBasesForTenantAPIKeyScope returned error: %v", err)
	}
	if len(got) != 1 || got[0] != "kb-2" {
		t.Fatalf("filtered = %#v, want only kb-2", got)
	}
}

func TestFilterKnowledgeBasesForTenantAPIKeyScopeRejectsExplicitOutOfScope(t *testing.T) {
	ctx := WithTenantAPIKeyScope(context.Background(), TenantAPIKeyScope{
		KnowledgeBaseIDs: StringArray{"kb-1"},
	})
	_, err := FilterKnowledgeBasesForTenantAPIKeyScope(ctx, []string{"kb-1", "kb-2"}, []string{"kb-1", "kb-2"})
	if err == nil {
		t.Fatal("expected forbidden for explicit out-of-scope kb_ids")
	}
}

func TestScopeHasCapability(t *testing.T) {
	s := TenantAPIKeyScope{Capabilities: StringArray{"chat"}}
	if !s.HasCapability(APIKeyCapabilityChat) {
		t.Fatal("expected chat capability to be present")
	}
	if (TenantAPIKeyScope{}).HasCapability(APIKeyCapabilityChat) {
		t.Fatal("empty scope must not report chat capability")
	}
	// Unknown capability is never satisfied.
	if s.HasCapability(APIKeyCapability("bogus")) {
		t.Fatal("unknown capability must not be satisfied")
	}
}

func TestNormalizeAPIKeyCapabilities(t *testing.T) {
	got := NormalizeAPIKeyCapabilities(StringArray{
		" Retrieve ",
		"chat",
		"read_agents",
		"manage_kbs",
		"message_history",
		"manage_mcp_services",
		"bogus",
		"",
	})
	want := []string{"retrieve", "chat", "read_agents", "manage_kbs", "message_history", "manage_mcp_services"}
	if len(got) != len(want) {
		t.Fatalf("normalized = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("normalized = %#v, want %#v", got, want)
		}
	}
}

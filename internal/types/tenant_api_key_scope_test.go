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

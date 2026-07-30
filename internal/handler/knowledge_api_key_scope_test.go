package handler

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestRequireTenantAPIKeyKnowledgeBaseAllowsUnrestrictedCaller(t *testing.T) {
	ctx := context.Background()
	if err := requireTenantAPIKeyKnowledgeBase(ctx, "kb-1"); err != nil {
		t.Fatalf("unrestricted caller error = %v, want nil", err)
	}
}

func TestRequireTenantAPIKeyKnowledgeBaseRejectsOutOfScopeKB(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		KnowledgeBaseIDs: types.StringArray{"kb-1"},
	})
	if err := requireTenantAPIKeyKnowledgeBase(ctx, "kb-2"); err == nil {
		t.Fatal("expected forbidden for out-of-scope kb_id")
	}
}

func TestIntersectKBAllowSetCombinesAgentAndAPIKeyScopes(t *testing.T) {
	got := intersectKBAllowSet(
		map[string]bool{"kb-1": true, "kb-2": true},
		map[string]bool{"kb-2": true, "kb-3": true},
	)
	if len(got) != 1 || !got["kb-2"] {
		t.Fatalf("intersection = %#v, want only kb-2", got)
	}
}

func TestFilterKnowledgesByKBAllowSet(t *testing.T) {
	in := []*types.Knowledge{
		{ID: "k1", KnowledgeBaseID: "kb-1"},
		{ID: "k2", KnowledgeBaseID: "kb-2"},
	}
	got := filterKnowledgesByKBAllowSet(in, map[string]bool{"kb-1": true})
	if len(got) != 1 || got[0].ID != "k1" {
		t.Fatalf("filtered = %#v, want only k1", got)
	}
}

func TestFilterKnowledgeSearchScopesForAPIKey(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		KnowledgeBaseIDs: types.StringArray{"kb-1"},
	})
	scopes := []types.KnowledgeSearchScope{
		{TenantID: 1, KBID: "kb-1"},
		{TenantID: 1, KBID: "kb-2"},
	}
	got := filterKnowledgeSearchScopesForAPIKey(ctx, scopes)
	if len(got) != 1 || got[0].KBID != "kb-1" {
		t.Fatalf("filtered scopes = %#v, want only kb-1", got)
	}
}

func TestTenantAPIKeySearchScopesUsesCallerTenant(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		KnowledgeBaseIDs: types.StringArray{"kb-1", "kb-2"},
	})
	ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(42))

	scopes, restricted := tenantAPIKeySearchScopes(ctx)
	if !restricted {
		t.Fatal("expected restricted search scopes")
	}
	if len(scopes) != 2 {
		t.Fatalf("scopes len = %d, want 2", len(scopes))
	}
	for _, scope := range scopes {
		if scope.TenantID != 42 {
			t.Fatalf("scope tenant_id = %d, want 42", scope.TenantID)
		}
	}
}

func TestRequireTenantAPIKeyKnowledgeBasesRejectsPartialOverlap(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		KnowledgeBaseIDs: types.StringArray{"kb-1"},
	})
	if err := requireTenantAPIKeyKnowledgeBases(ctx, "kb-1", "kb-2"); err == nil {
		t.Fatal("expected forbidden when one kb_id is out of scope")
	}
}

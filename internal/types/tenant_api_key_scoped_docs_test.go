package types

import (
	"context"
	"testing"
)

// Issue: https://github.com/Tencent/WeKnora/issues/5561
// A KB-scoped API key must be able to select explicit documents whose
// owning KB the key allows. The old blanket rejection of knowledge_ids
// made document-only selection impossible even for authorized docs.
func TestAuthorizeTenantAPIKeyKnowledgeTargetsAllowsInScopeDocuments(t *testing.T) {
	ctx := WithTenantAPIKeyScope(context.Background(), TenantAPIKeyScope{
		KnowledgeBaseIDs: StringArray{"kb-1"},
	})
	resolve := func(_ context.Context, _ []string) (map[string]string, error) {
		return map[string]string{"doc-1": "kb-1", "doc-2": "kb-1"}, nil
	}
	err := AuthorizeTenantAPIKeyKnowledgeTargets(ctx, nil, []string{"doc-1", "doc-2"}, resolve)
	if err != nil {
		t.Fatalf("in-scope documents must be allowed, got %v", err)
	}
}

// Issue: https://github.com/Tencent/WeKnora/issues/5561
// A document resolving to a KB outside the key allowlist is rejected
// before retrieval runs, as is an unknown document ID.
func TestAuthorizeTenantAPIKeyKnowledgeTargetsRejectsOutOfScopeDocuments(t *testing.T) {
	ctx := WithTenantAPIKeyScope(context.Background(), TenantAPIKeyScope{
		KnowledgeBaseIDs: StringArray{"kb-1"},
	})
	resolve := func(_ context.Context, _ []string) (map[string]string, error) {
		return map[string]string{"doc-1": "kb-2", "doc-2": ""}, nil
	}
	err := AuthorizeTenantAPIKeyKnowledgeTargets(ctx, nil, []string{"doc-1"}, resolve)
	if err == nil {
		t.Fatal("document outside allowlist must be forbidden")
	}
	err = AuthorizeTenantAPIKeyKnowledgeTargets(ctx, nil, []string{"doc-2"}, resolve)
	if err == nil {
		t.Fatal("unknown document must be forbidden")
	}
}

// Issue: https://github.com/Tencent/WeKnora/issues/5561
// knowledge_ids is an explicit document-only selection: combining it
// with whole-library kb_ids, or exceeding the count cap, is a bad
// request rather than a permission problem.
func TestAuthorizeTenantAPIKeyKnowledgeTargetsRejectsCombinationAndOverflow(t *testing.T) {
	ctx := WithTenantAPIKeyScope(context.Background(), TenantAPIKeyScope{
		KnowledgeBaseIDs: StringArray{"kb-1"},
	})
	resolve := func(_ context.Context, _ []string) (map[string]string, error) {
		return map[string]string{"doc-1": "kb-1"}, nil
	}
	err := AuthorizeTenantAPIKeyKnowledgeTargets(ctx, []string{"kb-1"}, []string{"doc-1"}, resolve)
	if err == nil {
		t.Fatal("knowledge_ids combined with kb_ids must be rejected")
	}
	many := make([]string, maxScopedAPIKeyKnowledgeIDs+1)
	for i := range many {
		many[i] = "doc"
	}
	err = AuthorizeTenantAPIKeyKnowledgeTargets(ctx, nil, many, resolve)
	if err == nil {
		t.Fatal("knowledge_ids beyond the cap must be rejected")
	}
}

// Issue: https://github.com/Tencent/WeKnora/issues/5561
// Without a resolver the scope still cannot verify documents, so the
// blanket rejection stays as the fallback for callers that cannot map
// knowledge IDs to their KBs.
func TestAuthorizeTenantAPIKeyKnowledgeTargetsNilResolverStillRejects(t *testing.T) {
	ctx := WithTenantAPIKeyScope(context.Background(), TenantAPIKeyScope{
		KnowledgeBaseIDs: StringArray{"kb-1"},
	})
	err := AuthorizeTenantAPIKeyKnowledgeTargets(ctx, nil, []string{"doc-1"}, nil)
	if err == nil {
		t.Fatal("nil resolver must keep the forbidden fallback")
	}
}

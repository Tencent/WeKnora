package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestResolveKnowledgeBasesFiltersImplicitAgentDefaultsForRestrictedAPIKey(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		KnowledgeBaseIDs: types.StringArray{"kb-allowed"},
	})
	svc := &sessionService{}

	kbIDs, knowledgeIDs, err := svc.resolveKnowledgeBases(ctx, &types.QARequest{
		Session: &types.Session{TenantID: 10000},
		CustomAgent: &types.CustomAgent{
			TenantID: 10000,
			Config: types.CustomAgentConfig{
				KBSelectionMode: "selected",
				KnowledgeBases:  []string{"kb-allowed", "kb-blocked"},
			},
		},
	})
	if err != nil {
		t.Fatalf("resolveKnowledgeBases returned error: %v", err)
	}
	if len(kbIDs) != 1 || kbIDs[0] != "kb-allowed" {
		t.Fatalf("kbIDs = %#v, want only kb-allowed", kbIDs)
	}
	if len(knowledgeIDs) != 0 {
		t.Fatalf("knowledgeIDs = %#v, want empty", knowledgeIDs)
	}
}

func TestResolveKnowledgeBasesRejectsExplicitOutOfScopeKBForRestrictedAPIKey(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		KnowledgeBaseIDs: types.StringArray{"kb-allowed"},
	})
	svc := &sessionService{}

	_, _, err := svc.resolveKnowledgeBases(ctx, &types.QARequest{
		Session:          &types.Session{TenantID: 10000},
		KnowledgeBaseIDs: []string{"kb-blocked"},
		CustomAgent: &types.CustomAgent{
			TenantID: 10000,
			Config: types.CustomAgentConfig{
				KBSelectionMode: "selected",
				KnowledgeBases:  []string{"kb-allowed", "kb-blocked"},
			},
		},
	})
	if err == nil {
		t.Fatal("expected forbidden for explicit out-of-scope knowledge_base_ids")
	}
}

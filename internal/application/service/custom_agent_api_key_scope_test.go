package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestGetSuggestedQuestionsRejectsOutOfScopeKnowledgeBaseIDs(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		KnowledgeBaseIDs: types.StringArray{"kb-1"},
	})
	ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(1))

	svc := &customAgentService{}
	_, err := svc.GetSuggestedQuestions(ctx, "agent-1", []string{"kb-2"}, nil, nil, 6)
	if err == nil {
		t.Fatal("expected forbidden for out-of-scope knowledge_base_ids")
	}
}

func TestGetSuggestedQuestionsRejectsKnowledgeIDsForRestrictedKey(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		KnowledgeBaseIDs: types.StringArray{"kb-1"},
	})
	ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(1))

	svc := &customAgentService{}
	_, err := svc.GetSuggestedQuestions(ctx, "agent-1", nil, []string{"doc-1"}, nil, 6)
	if err == nil {
		t.Fatal("expected forbidden for knowledge_ids under KB-restricted key")
	}
}

func TestGetSuggestedQuestionsRejectsTagScopesForRestrictedKey(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		KnowledgeBaseIDs: types.StringArray{"kb-1"},
	})
	ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(1))

	svc := &customAgentService{}
	_, err := svc.GetSuggestedQuestions(
		ctx,
		"agent-1",
		nil,
		nil,
		[]types.TagScope{{KnowledgeBaseID: "kb-1", TagIDs: []string{"tag-1"}}},
		6,
	)
	if err == nil {
		t.Fatal("expected forbidden for tag_scopes under KB-restricted key")
	}
}

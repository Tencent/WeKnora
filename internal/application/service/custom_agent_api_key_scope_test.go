package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type suggestionAgentRepo struct {
	interfaces.CustomAgentRepository
	agent *types.CustomAgent
}

func (r *suggestionAgentRepo) GetAgentByID(_ context.Context, _ string, _ uint64) (*types.CustomAgent, error) {
	return r.agent, nil
}

func TestGetSuggestedQuestionsRejectsOutOfScopeKnowledgeBaseIDs(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		KnowledgeBaseIDs: types.StringArray{"kb-1"},
	})
	ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(1))

	svc := &customAgentService{}
	_, err := svc.GetSuggestedQuestions(ctx, "agent-1", []string{"kb-2"}, nil, nil, nil, 6)
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
	_, err := svc.GetSuggestedQuestions(ctx, "agent-1", nil, []string{"doc-1"}, nil, nil, 6)
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
		nil,
		6,
	)
	if err == nil {
		t.Fatal("expected forbidden for tag_scopes under KB-restricted key")
	}
}

func TestGetSuggestedQuestionsFolderOnlyRejectsAgentDefaultOutsideAPIKeyScope(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		KnowledgeBaseIDs: types.StringArray{"allowed-kb"},
	})
	ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(1))

	svc := &customAgentService{
		repo: &suggestionAgentRepo{agent: &types.CustomAgent{
			ID: "agent-1", TenantID: 1,
			Config: types.CustomAgentConfig{KBSelectionMode: "selected", KnowledgeBases: []string{"default-kb"}},
		}},
		kbService: &suggestionKBService{kbs: map[string]*types.KnowledgeBase{
			"default-kb": {ID: "default-kb", TenantID: 1},
		}},
		knowledgeFolderService: &suggestionFolderService{folders: map[string]*types.KnowledgeFolder{
			"folder-1": {ID: "folder-1", TenantID: 1, KnowledgeBaseID: "default-kb"},
		}},
	}
	_, err := svc.GetSuggestedQuestions(ctx, "agent-1", nil, nil, nil, []string{"folder-1"}, 6)
	if err == nil {
		t.Fatal("expected folder-only scope outside API key allow-list to be rejected")
	}
}

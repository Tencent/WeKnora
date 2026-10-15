package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/agent/operations"
	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssistantScopeSnapshot(t *testing.T) {
	for _, scope := range []types.AssistantScope{"", "page", "knowledge_base", "workspace"} {
		req := &types.QARequest{
			Session:        &types.Session{ID: "s", TenantID: 1},
			AssistantScope: scope,
			PageContext:    &types.PageContext{AgentID: "a1"},
		}
		snapshot := resumableAgentRequest(context.Background(), req)
		raw, err := json.Marshal(snapshot)
		require.NoError(t, err)
		var restored types.ResumableAgentRequest
		require.NoError(t, json.Unmarshal(raw, &restored))
		assert.Equal(t, scope.WithDefault(), restored.AssistantScope)
		assert.Equal(t, req.PageContext, restored.PageContext)
	}
}

func TestAssistantOperationScopeUsesResolvedObjects(t *testing.T) {
	page := &agent.PageContextInfo{
		KnowledgeBaseID: "kb", Document: &agent.SelectedDocumentInfo{KnowledgeID: "doc"},
		WikiPage: &agent.PageContextWikiPage{Slug: "guide"}, Agent: &agent.PageContextAgent{ID: "agent"},
	}
	scope := assistantOperationScope(types.AssistantScopePage, page)
	assert.Equal(t, "kb", scope.KnowledgeBaseID)
	assert.Equal(t, "doc", scope.KnowledgeID)
	assert.Equal(t, "guide", scope.WikiSlug)
	assert.Equal(t, "agent", scope.AgentID)
	assert.Empty(t, assistantOperationScope(types.AssistantScopePage, nil).KnowledgeBaseID)
}

func TestAssistantDirectToolConfigCannotWidenScope(t *testing.T) {
	for _, level := range []types.AssistantScope{types.AssistantScopePage, types.AssistantScopeKnowledgeBase} {
		for _, attached := range []bool{false, true} {
			cfg := toolSurfaceConfig()
			cfg.OperationsAgentID = types.BuiltinWeKnoraAssistantID
			cfg.AssistantScope = level
			cfg.KnowledgeBases = append(cfg.KnowledgeBases, "other")
			cfg.SearchTargets = append(cfg.SearchTargets, &types.SearchTarget{KnowledgeBaseID: "other"})
			cfg.WritableKBIDs = []string{"kb", "other"}
			var page *agent.PageContextInfo
			if attached {
				page = &agent.PageContextInfo{KnowledgeBaseID: "kb"}
			}
			narrowed, scope := assistantToolConfig(cfg, page)
			assert.Len(t, cfg.SearchTargets, 2, "the original search targets must not be mutated")
			if attached {
				assert.Equal(t, "kb", scope.KnowledgeBaseID)
				require.Len(t, narrowed.SearchTargets, 1)
				assert.Equal(t, []string{"kb"}, narrowed.KnowledgeBases)
				assert.Equal(t, []string{"kb"}, narrowed.WritableKBIDs)
			} else {
				assert.Empty(t, narrowed.SearchTargets)
				assert.Empty(t, narrowed.KnowledgeBases)
				assert.Empty(t, narrowed.WritableKBIDs)
			}
		}
	}
}

type assistantScopeDispatcher struct{ calls []operations.Call }

func (d *assistantScopeDispatcher) Dispatch(
	_ context.Context, _ operations.Identity, call operations.Call,
) (*operations.Response, error) {
	d.calls = append(d.calls, call)
	return &operations.Response{Status: 200, Body: []byte(`{"data":{"id":"verified","name":"KB"}}`)}, nil
}

func TestRegisterOperationToolsUsesVerifiedPage(t *testing.T) {
	dispatcher := &assistantScopeDispatcher{}
	svc := &agentService{operationDispatcher: dispatcher}
	ctx := types.WithCaller(types.WithPrincipal(context.Background(),
		types.Principal{Type: types.PrincipalWebUser, ID: "u"}), types.Caller{UserID: "u", TenantID: 1})
	cfg := &types.AgentConfig{
		OperationsAgentID: types.BuiltinWeKnoraAssistantID,
		AssistantScope:    types.AssistantScopePage, PageContext: &types.PageContext{KnowledgeBaseID: "unverified"},
	}
	registry := tools.NewToolRegistry()
	svc.registerOperationTools(ctx, registry, cfg, &agent.PageContextInfo{KnowledgeBaseID: "verified"})
	tool, err := registry.GetTool(tools.ToolRunOperation)
	require.NoError(t, err)
	args := []byte(`{"operation":"knowledge_base.get","params":{"knowledge_base_id":"unverified"}}`)
	result, err := tool.Execute(ctx, args)
	require.NoError(t, err)
	assert.False(t, result.Success)
	assert.Empty(t, dispatcher.calls)
	args = []byte(`{"operation":"knowledge_base.get","params":{"knowledge_base_id":"verified"}}`)
	result, err = tool.Execute(ctx, args)
	require.NoError(t, err)
	assert.True(t, result.Success, result.Error)
	require.Len(t, dispatcher.calls, 1)
}

func TestRegisterDirectWikiToolsUsesVerifiedPage(t *testing.T) {
	kb := &types.KnowledgeBase{ID: "kb"}
	kb.IndexingStrategy.WikiEnabled = true
	svc := newToolSurfaceService(kb)
	cfg := toolSurfaceConfig(tools.ToolWikiWritePage)
	cfg.OperationsAgentID = types.BuiltinWeKnoraAssistantID
	cfg.AssistantScope = types.AssistantScopePage
	cfg.WritableKBIDs = []string{"kb"}
	registry := tools.NewToolRegistry()
	page := &agent.PageContextInfo{KnowledgeBaseID: "kb", Document: &agent.SelectedDocumentInfo{KnowledgeID: "doc"}}
	require.NoError(t, svc.registerTools(context.Background(), registry, cfg, nil, nil, "s", page))
	tool, err := registry.GetTool(tools.ToolWikiWritePage)
	require.NoError(t, err)
	result, err := tool.Execute(context.Background(), []byte(`{"slug":"guide"}`))
	require.NoError(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, result.Error, "scope page")
}

package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/config"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type restrictedDocumentLookup struct {
	interfaces.KnowledgeService
	docs []*types.Knowledge
}

func (l restrictedDocumentLookup) GetKnowledgeBatchWithSharedAccess(
	_ context.Context, _ uint64, _ []string,
) ([]*types.Knowledge, error) {
	return l.docs, nil
}

func TestRestrictedAgentDocumentTargetsRejectPartialAuthorization(t *testing.T) {
	ctx := types.WithCaller(context.Background(), types.Caller{TenantID: 7})
	ctx = types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{KeyID: 11, KnowledgeBaseIDs: []string{"hr"}})
	document := func(id, kb, status string) *types.Knowledge {
		return &types.Knowledge{
			ID: id, KnowledgeBaseID: kb, TenantID: 7,
			EnableStatus: status, ParseStatus: types.ParseStatusCompleted,
		}
	}
	lookup := restrictedDocumentLookup{docs: []*types.Knowledge{
		document("allowed", "hr", "enabled"),
		document("sibling", "hr", "enabled"),
		document("foreign", "finance", "enabled"),
		document("disabled", "hr", "disabled"),
		{
			ID: "wrong-owner", KnowledgeBaseID: "hr", TenantID: 8,
			EnableStatus: "enabled", ParseStatus: types.ParseStatusCompleted,
		},
	}}
	bases := &suggestionKBService{kbs: map[string]*types.KnowledgeBase{"hr": {ID: "hr", TenantID: 7}}}
	targets, err := buildRestrictedAgentDocumentTargets(ctx, lookup, bases, 7, []string{"allowed", "allowed"})
	require.NoError(t, err)
	require.Len(t, targets, 1)
	require.Equal(t, types.SearchTargetTypeKnowledge, targets[0].Type)
	require.Equal(t, "hr", targets[0].KnowledgeBaseID)
	require.Equal(t, []string{"allowed"}, targets[0].KnowledgeIDs)
	for _, id := range []string{"foreign", "disabled", "missing", "wrong-owner"} {
		t.Run(id, func(t *testing.T) {
			targets, err := buildRestrictedAgentDocumentTargets(ctx, lookup, bases, 7, []string{"allowed", id})
			var appErr *apperrors.AppError
			require.ErrorAs(t, err, &appErr)
			require.Equal(t, 403, appErr.HTTPCode)
			require.Empty(t, targets)
		})
	}
	targets, err = buildRestrictedAgentDocumentTargets(ctx, lookup, bases, 7, nil)
	var appErr *apperrors.AppError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, 400, appErr.HTTPCode)
	require.Empty(t, targets)
	tooMany := make([]string, maxRestrictedAgentDocuments+1)
	targets, err = buildRestrictedAgentDocumentTargets(ctx, lookup, bases, 7, tooMany)
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, 400, appErr.HTTPCode)
	require.Contains(t, appErr.Error(), "at most")
	require.Empty(t, targets)
	targets, err = buildRestrictedAgentDocumentTargets(ctx, lookup, &suggestionKBService{}, 7, []string{"allowed"})
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, 403, appErr.HTTPCode)
	require.Empty(t, targets)
}

func TestDocumentScopedAgentConfigExcludesDefaultsAndHistoricalContext(t *testing.T) {
	ctx := types.WithCaller(context.Background(), types.Caller{TenantID: 7})
	ctx = types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{KeyID: 11, KnowledgeBaseIDs: []string{"hr"}})
	svc := &sessionService{
		cfg:                  &config.Config{},
		knowledgeBaseService: &suggestionKBService{kbs: map[string]*types.KnowledgeBase{"hr": {ID: "hr", TenantID: 7}}},
		knowledgeService: restrictedDocumentLookup{docs: []*types.Knowledge{{
			ID: "allowed", TenantID: 7, KnowledgeBaseID: "hr",
			EnableStatus: "enabled", ParseStatus: types.ParseStatusCompleted,
		}}},
	}
	memoryEnabled := true
	req := &types.QARequest{
		DocumentScopeAPIKeyID: 11,
		Session:               &types.Session{TenantID: 7},
		KnowledgeIDs:          []string{"allowed"},
		WebSearchEnabled:      true,
		CustomAgent: &types.CustomAgent{TenantID: 7, Config: types.CustomAgentConfig{
			KnowledgeBases: []string{"hr", "finance"},
			AllowedTools: []string{
				tools.ToolSearchKnowledge, tools.ToolReadDocument, "query_knowledge_graph", "search_conversations",
			},
			MultiTurnEnabled: true,
			MemoryEnabled:    &memoryEnabled,
			WebSearchEnabled: true,
			MCPSelectionMode: "all",
		}},
	}
	cfg, err := svc.buildAgentConfig(ctx, req, &types.Tenant{ID: 7}, 7)
	require.NoError(t, err)
	require.True(t, cfg.DocumentScoped)
	require.Empty(t, cfg.KnowledgeBases)
	require.Empty(t, cfg.WritableKBIDs)
	require.False(t, cfg.MultiTurnEnabled)
	require.False(t, *cfg.MemoryEnabled)
	require.False(t, cfg.WebSearchEnabled)
	require.Equal(t, "none", cfg.MCPSelectionMode)
	require.Equal(t, []string{tools.ToolSearchKnowledge, tools.ToolReadDocument}, cfg.AllowedTools)
	require.Len(t, cfg.SearchTargets, 1)
	require.Equal(t, []string{"allowed"}, cfg.SearchTargets[0].KnowledgeIDs)
}

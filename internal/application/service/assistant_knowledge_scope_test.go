package service

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type assistantScopeKBService struct {
	*tagTargetKnowledgeBaseService
}

func (s *assistantScopeKBService) ListKnowledgeBases(context.Context) ([]*types.KnowledgeBase, error) {
	var bases []*types.KnowledgeBase
	for _, kb := range s.kbs {
		bases = append(bases, kb)
	}
	return bases, nil
}

func TestAssistantPageContextDoesNotSelectKnowledgeScope(t *testing.T) {
	require.NoError(t, types.LoadBuiltinAgentsConfig(filepath.Join("..", "..", "..", "config")))
	for _, tc := range []struct {
		name     string
		kbIDs    []string
		docIDs   []string
		tags     []types.TagScope
		apiScope bool
		wantKBs  []string
		wantDocs []string
	}{
		{name: "page alone keeps workspace scope", wantKBs: []string{"doc-kb", "faq-kb", "notes-kb"}},
		{name: "explicit KB overrides page", kbIDs: []string{"faq-kb"}, wantKBs: []string{"faq-kb"}},
		{
			name: "explicit document stays narrow", docIDs: []string{"doc-2"},
			wantKBs: []string{"doc-kb"}, wantDocs: []string{"doc-2"},
		},
		{
			name:    "explicit tag stays narrow",
			tags:    []types.TagScope{{KnowledgeBaseID: "doc-kb", TagIDs: []string{"tag-a"}}},
			wantKBs: []string{"doc-kb"}, wantDocs: []string{"doc-1", "doc-3"},
		},
		{name: "API key still limits defaults", apiScope: true, wantKBs: []string{"faq-kb"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newTagTargetSessionService()
			base := svc.knowledgeBaseService.(*tagTargetKnowledgeBaseService)
			base.kbs["notes-kb"] = &types.KnowledgeBase{
				ID: "notes-kb", TenantID: 100, Type: types.KnowledgeBaseTypeDocument,
			}
			for _, kb := range base.kbs {
				kb.IndexingStrategy.KeywordEnabled = true
			}
			svc.knowledgeBaseService = &assistantScopeKBService{base}
			agent := types.GetBuiltinAgent(types.BuiltinWeKnoraAssistantID, 100)
			agent.Config.WebSearchProviderID = "provider"
			ctx := tagTargetContext()
			if tc.apiScope {
				ctx = types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{
					KnowledgeBaseIDs: types.StringArray{"faq-kb"},
				})
			}
			req := &types.QARequest{
				Session: &types.Session{ID: "session", TenantID: 100}, CustomAgent: agent,
				KnowledgeBaseIDs: tc.kbIDs, KnowledgeIDs: tc.docIDs, TagScopes: tc.tags,
				PageContext:     &types.PageContext{KnowledgeBaseID: "notes-kb"},
				AssistantAccess: types.AssistantAccessReadOnly,
			}
			tenant := &types.Tenant{ID: 100}
			cfg, err := svc.buildAgentConfig(ctx, req, tenant, 100)
			require.NoError(t, err)
			require.ElementsMatch(t, tc.wantKBs, cfg.SearchTargets.GetAllKnowledgeBaseIDs())
			require.ElementsMatch(t, tc.wantDocs, cfg.KnowledgeIDs)
			require.Empty(t, cfg.WritableKBIDs, "workspace visibility must not enable writes in read-only mode")
			if len(tc.wantKBs) == 3 {
				require.Equal(t, "notes-kb", cfg.PageContext.KnowledgeBaseID)
			} else {
				require.Nil(t, cfg.PageContext, "the page cannot add a KB outside the explicit/API scope")
			}

			// An all-KB assistant with a narrowed turn must pass that resolved
			// scope to temporary children, including after durable restoration.
			ctx = context.WithValue(ctx, childSourceKey{}, childSource{Agent: agent, ModelID: "model"})
			raw, err := inheritedChildSnapshot(ctx, cfg, cfg.AllowedTools)
			require.NoError(t, err)
			var saved delegatedConfig
			require.NoError(t, json.Unmarshal([]byte(raw), &saved))
			// Newly visible bases must not expand a previously spawned child.
			base.kbs["new-kb"] = &types.KnowledgeBase{ID: "new-kb", TenantID: 100}
			restored, err := svc.restoreInheritedConfig(ctx, &saved, tenant)
			require.NoError(t, err)
			require.Equal(t, cfg.SearchTargets, restored.SearchTargets)
			require.ElementsMatch(t, tc.wantDocs, restored.KnowledgeIDs)
			require.Empty(t, restored.WritableKBIDs)
		})
	}
}

func TestPageContextDoesNotExpandTemplateAgentScope(t *testing.T) {
	svc := newTagTargetSessionService()
	for _, shared := range []bool{false, true} {
		agent := &types.CustomAgent{ID: "template", TenantID: 100, Config: types.CustomAgentConfig{
			AgentMode: types.AgentModeSmartReasoning, KBSelectionMode: "selected", KnowledgeBases: []string{"doc-kb"},
			WebSearchProviderID: "provider",
		}}
		req := &types.QARequest{
			Session: &types.Session{ID: "session", TenantID: 100}, CustomAgent: agent,
			PageContext: &types.PageContext{KnowledgeBaseID: "faq-kb"},
		}
		if shared {
			req.Session.TenantID = 200
			req.SharedAgentReadOnly = true
			req.KnowledgeBaseIDs = []string{"doc-kb", "faq-kb"}
		}
		cfg, err := svc.buildAgentConfig(tagTargetContext(), req, &types.Tenant{ID: 100}, 100)
		require.NoError(t, err)
		require.Equal(t, []string{"doc-kb"}, cfg.SearchTargets.GetAllKnowledgeBaseIDs())
		require.Nil(t, cfg.PageContext)
		if shared {
			require.Empty(t, cfg.WritableKBIDs)
		}
	}
}

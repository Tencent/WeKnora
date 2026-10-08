package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type toolSurfaceKBService struct {
	fakeAgentKnowledgeBaseService
}

func (s *toolSurfaceKBService) GetKnowledgeBaseByIDOnly(context.Context, string) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

func newToolSurfaceService(kb *types.KnowledgeBase) *agentService {
	return &agentService{
		knowledgeBaseService: &toolSurfaceKBService{fakeAgentKnowledgeBaseService{kb: kb}},
		knowledgeService:     &fakeAgentKnowledgeService{},
	}
}

func toolSurfaceConfig(allowed ...string) *types.AgentConfig {
	return &types.AgentConfig{
		KnowledgeBases: []string{"kb"},
		SearchTargets: types.SearchTargets{&types.SearchTarget{
			Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb", TenantID: 7,
		}},
		AllowedTools: allowed,
	}
}

// A stored allowlist that still names the retired retrieval tools registers
// their successors, so agents saved before the consolidation keep working
// without a data migration.
func TestRegisterToolsMapsRetiredRetrievalToolsToSuccessors(t *testing.T) {
	kb := &types.KnowledgeBase{ID: "kb"}
	kb.IndexingStrategy.VectorEnabled = true
	svc := newToolSurfaceService(kb)
	registry := tools.NewToolRegistry()
	cfg := toolSurfaceConfig(
		tools.LegacyToolKnowledgeSearch, tools.LegacyToolGrepChunks,
		tools.LegacyToolListKnowledgeChunks, tools.LegacyToolGetDocumentInfo,
		tools.LegacyToolWikiReadSourceDoc, tools.ToolThinking,
	)
	require.NoError(t, svc.registerTools(t.Context(), registry, cfg, nil, nil, "session"))

	names := registry.ListTools()
	require.Contains(t, names, tools.ToolSearchKnowledge)
	require.Contains(t, names, tools.ToolReadDocument)
	require.Contains(t, names, tools.ToolThinking)
	for _, retired := range []string{
		tools.LegacyToolKnowledgeSearch, tools.LegacyToolGrepChunks, tools.LegacyToolListKnowledgeChunks,
		tools.LegacyToolGetDocumentInfo, tools.LegacyToolWikiReadSourceDoc,
	} {
		require.NotContains(t, names, retired)
	}
	require.Equal(t, 1, countName(names, tools.ToolSearchKnowledge), "duplicates collapse to one registration")
	require.Equal(t, 1, countName(names, tools.ToolReadDocument))
}

// The graph tool follows the graph capability of the scope, not the checkbox.
func TestRegisterToolsOffersGraphToolOnlyWithGraphEnabledKB(t *testing.T) {
	plain := &types.KnowledgeBase{ID: "kb"}
	plain.IndexingStrategy.VectorEnabled = true
	registry := tools.NewToolRegistry()
	require.NoError(t, newToolSurfaceService(plain).registerTools(
		t.Context(), registry, toolSurfaceConfig(tools.ToolSearchKnowledge, tools.ToolQueryKnowledgeGraph),
		nil, nil, "s",
	))
	require.Contains(t, registry.ListTools(), tools.ToolSearchKnowledge)
	require.NotContains(t, registry.ListTools(), tools.ToolQueryKnowledgeGraph)

	graph := &types.KnowledgeBase{ID: "kb", ExtractConfig: &types.ExtractConfig{Enabled: true}}
	graph.IndexingStrategy.VectorEnabled = true
	graph.IndexingStrategy.GraphEnabled = true
	registry = tools.NewToolRegistry()
	require.NoError(t, newToolSurfaceService(graph).registerTools(
		t.Context(), registry, toolSurfaceConfig(tools.ToolSearchKnowledge, tools.ToolQueryKnowledgeGraph),
		nil, nil, "s",
	))
	require.Contains(t, registry.ListTools(), tools.ToolQueryKnowledgeGraph)
}

// Default agents get the consolidated read surface.
func TestRegisterToolsDefaultSurface(t *testing.T) {
	kb := &types.KnowledgeBase{ID: "kb"}
	kb.IndexingStrategy.KeywordEnabled = true
	registry := tools.NewToolRegistry()
	require.NoError(t, newToolSurfaceService(kb).registerTools(
		t.Context(), registry, toolSurfaceConfig(), nil, nil, "s",
	))
	names := registry.ListTools()
	for _, want := range []string{tools.ToolSearchKnowledge, tools.ToolReadDocument, tools.ToolListDocuments} {
		require.Contains(t, names, want)
	}
}

func countName(names []string, want string) int {
	n := 0
	for _, name := range names {
		if name == want {
			n++
		}
	}
	return n
}

// A wiki-only knowledge base has no chunk index but still stores chunks, so
// the document readers the wiki agents use to check sources stay available
// while chunk search is dropped.
func TestRegisterToolsKeepsDocumentReadersForWikiOnlyScope(t *testing.T) {
	kb := &types.KnowledgeBase{ID: "kb"}
	kb.IndexingStrategy.WikiEnabled = true
	registry := tools.NewToolRegistry()
	cfg := toolSurfaceConfig(
		tools.ToolWikiSearch, tools.ToolWikiReadPage, tools.ToolReadDocument,
		tools.ToolListDocuments, tools.ToolSearchKnowledge, tools.LegacyToolWikiReadSourceDoc,
	)
	require.NoError(t, newToolSurfaceService(kb).registerTools(t.Context(), registry, cfg, nil, nil, "s"))
	names := registry.ListTools()
	for _, want := range []string{
		tools.ToolWikiSearch, tools.ToolWikiReadPage, tools.ToolReadDocument, tools.ToolListDocuments,
	} {
		require.Contains(t, names, want)
	}
	require.NotContains(t, names, tools.ToolSearchKnowledge, "chunk search needs a vector or keyword index")
}

// Search targets only need read access, so wiki mutation tools are registered
// only when a wiki KB in scope is writable for this caller.
func TestRegisterToolsOffersWikiWritesOnlyForWritableKBs(t *testing.T) {
	kb := &types.KnowledgeBase{ID: "kb"}
	kb.IndexingStrategy.WikiEnabled = true
	writeTools := []string{
		tools.ToolWikiWritePage, tools.ToolWikiReplaceText, tools.ToolWikiRenamePage,
		tools.ToolWikiDeletePage, tools.ToolWikiFlagIssue, tools.ToolWikiUpdateIssue,
	}
	allowed := append([]string{tools.ToolWikiReadPage, tools.ToolWikiReadIssue}, writeTools...)

	readOnly := toolSurfaceConfig(allowed...)
	registry := tools.NewToolRegistry()
	require.NoError(t, newToolSurfaceService(kb).registerTools(t.Context(), registry, readOnly, nil, nil, "s"))
	names := registry.ListTools()
	require.Contains(t, names, tools.ToolWikiReadPage)
	require.Contains(t, names, tools.ToolWikiReadIssue)
	for _, name := range writeTools {
		require.NotContains(t, names, name)
	}

	writable := toolSurfaceConfig(allowed...)
	writable.WritableKBIDs = []string{"kb"}
	registry = tools.NewToolRegistry()
	require.NoError(t, newToolSurfaceService(kb).registerTools(t.Context(), registry, writable, nil, nil, "s"))
	names = registry.ListTools()
	for _, name := range writeTools {
		require.Contains(t, names, name)
	}
}

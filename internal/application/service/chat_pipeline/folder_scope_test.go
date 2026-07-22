package chatpipeline

import (
	"context"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type folderScopeKBService struct {
	interfaces.KnowledgeBaseService
	mu     sync.Mutex
	params []types.SearchParams
}

func (s *folderScopeKBService) HybridSearch(
	_ context.Context, _ string, params types.SearchParams,
) ([]*types.SearchResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.params = append(s.params, params)
	return nil, nil
}

func TestQueryExpansionCopiesTargetKnowledgeIDsForFolderScope(t *testing.T) {
	kb := &folderScopeKBService{}
	p := &PluginSearch{knowledgeBaseService: kb}
	cm := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			Query: "original", EmbeddingTopK: 2, RerankTopK: 2,
			SearchTargets: types.SearchTargets{{
				Type: types.SearchTargetTypeKnowledge, KnowledgeBaseID: "kb-1",
				KnowledgeIDs: []string{"doc-1", "doc-2"},
			}},
		},
		PipelineState: types.PipelineState{RewriteQuery: "what alpha beta"},
	}

	p.runQueryExpansion(context.Background(), cm)
	require.NotEmpty(t, kb.params)
	for _, params := range kb.params {
		assert.Equal(t, []string{"doc-1", "doc-2"}, params.KnowledgeIDs)
	}
}

func TestFolderAuthoritativeEmptyTargetsDoNotGenerateFullKBSearchRequest(t *testing.T) {
	kb := &folderScopeKBService{}
	p := &PluginSearch{knowledgeBaseService: kb}
	cm := &types.ChatManage{PipelineRequest: types.PipelineRequest{
		KnowledgeBaseIDs: []string{"default-kb"}, KnowledgeScopeSpecified: true,
	}}

	assert.Empty(t, p.searchByTargets(context.Background(), cm))
	assert.Empty(t, kb.params)
}

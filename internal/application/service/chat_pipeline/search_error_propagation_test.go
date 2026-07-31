package chatpipeline

import (
	"context"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type emptyScopeKnowledgeBaseServiceStub struct {
	interfaces.KnowledgeBaseService

	mu    sync.Mutex
	calls int
}

func (s *emptyScopeKnowledgeBaseServiceStub) GetKnowledgeBasesByIDsOnly(
	_ context.Context,
	ids []string,
) ([]*types.KnowledgeBase, error) {
	knowledgeBases := make([]*types.KnowledgeBase, 0, len(ids))
	for _, id := range ids {
		knowledgeBases = append(knowledgeBases, &types.KnowledgeBase{ID: id})
	}
	return knowledgeBases, nil
}

func (*emptyScopeKnowledgeBaseServiceStub) ResolveEmbeddingModelKeys(
	_ context.Context,
	knowledgeBases []*types.KnowledgeBase,
) map[string]string {
	keys := make(map[string]string, len(knowledgeBases))
	for _, knowledgeBase := range knowledgeBases {
		keys[knowledgeBase.ID] = "shared-model"
	}
	return keys
}

func (*emptyScopeKnowledgeBaseServiceStub) GetQueryEmbedding(
	context.Context,
	string,
	string,
) ([]float32, error) {
	return []float32{0.1, 0.2}, nil
}

func (s *emptyScopeKnowledgeBaseServiceStub) HybridSearch(
	context.Context,
	string,
	types.SearchParams,
) ([]*types.SearchResult, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	return nil, nil
}

func (s *emptyScopeKnowledgeBaseServiceStub) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func TestSearchDoesNotExpandAnEmptyKnowledgeScope(t *testing.T) {
	stub := &emptyScopeKnowledgeBaseServiceStub{}
	plugin := &PluginSearch{knowledgeBaseService: stub}
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			Query: "how does folder scoped search work",
			SearchTargets: types.SearchTargets{{
				Type:            types.SearchTargetTypeKnowledge,
				KnowledgeBaseID: "kb-empty",
				FolderIDs:       []string{"folder-1"},
				ScopeTagIDs:     []string{"tag-with-no-documents"},
			}},
			EmbeddingTopK:        5,
			RerankTopK:           5,
			EnableQueryExpansion: true,
		},
		PipelineState: types.PipelineState{
			RewriteQuery: "how does folder scoped search work",
		},
	}

	pluginErr := plugin.OnEvent(context.Background(), types.CHUNK_SEARCH, chatManage, func() *PluginError {
		t.Fatal("an empty constrained scope must not continue the pipeline")
		return nil
	})

	require.Same(t, ErrSearchNothing, pluginErr)
	require.Zero(t, stub.callCount(), "query expansion must not widen an empty knowledge scope")
}

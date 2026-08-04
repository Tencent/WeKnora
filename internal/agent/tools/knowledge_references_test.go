package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type knowledgeReferenceChunkRepository struct {
	interfaces.ChunkRepository
}

func (r *knowledgeReferenceChunkRepository) ListPagedChunksByKnowledgeID(
	context.Context,
	uint64,
	string,
	*types.Pagination,
	[]types.ChunkType,
	[]string,
	string,
	string,
	string,
	string,
	*bool,
) ([]*types.Chunk, int64, error) {
	return nil, 1, nil
}

type knowledgeReferenceChunkService struct {
	interfaces.ChunkService
	repository interfaces.ChunkRepository
}

func (s *knowledgeReferenceChunkService) GetRepository() interfaces.ChunkRepository {
	return s.repository
}

func TestKnowledgeSearchResultCarriesRuntimeKnowledgeReferences(t *testing.T) {
	reference := &types.SearchResult{
		ID:              "chunk-search-1",
		Content:         "retrieved content",
		KnowledgeID:     "document-1",
		KnowledgeTitle:  "Document",
		KnowledgeBaseID: "kb-1",
		MatchType:       types.MatchTypeEmbedding,
	}
	tool := &KnowledgeSearchTool{
		chunkService: &knowledgeReferenceChunkService{
			repository: &knowledgeReferenceChunkRepository{},
		},
		searchTargets: types.SearchTargets{{
			KnowledgeBaseID: "kb-1",
			TenantID:        7,
		}},
		seenChunks: make(map[string]bool),
	}

	result, err := tool.formatOutput(
		context.Background(),
		[]*searchResultWithMeta{{
			SearchResult:    reference,
			KnowledgeBaseID: "kb-1",
		}},
		[]string{"kb-1"},
		[]string{"query"},
	)
	require.NoError(t, err)
	require.Equal(t, []*types.SearchResult{reference}, result.KnowledgeReferences)

	encoded, err := json.Marshal(&types.ToolResult{
		Success:             true,
		KnowledgeReferences: []*types.SearchResult{{ID: "runtime-only"}},
	})
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "runtime-only")
}

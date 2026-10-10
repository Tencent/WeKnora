package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type recordingEmbeddingModelService struct {
	interfaces.ModelService
	requested []string
}

func (s *recordingEmbeddingModelService) GetEmbeddingModel(_ context.Context, id string) (embedding.Embedder, error) {
	s.requested = append(s.requested, id)
	return dimensionTestEmbedder{dimensions: 768}, nil
}

// A wiki primary has no embedding model. Searched together with a document
// KB, the query used to be embedded with the primary's empty model ID and
// the whole search failed; it is now embedded with the vector KB's model.
func TestBuildRetrievalParamsEmbedsWithAVectorKB(t *testing.T) {
	models := &recordingEmbeddingModelService{}
	s := &knowledgeBaseService{modelService: models}
	engine := buildBoundComposite(t, &fakeRetrieveEngineService{
		engineType: types.PostgresRetrieverEngineType,
		support:    []types.RetrieverType{types.VectorRetrieverType},
	})
	wiki := &types.KnowledgeBase{ID: "kb-wiki", TenantID: 1}
	docs := &types.KnowledgeBase{
		ID: "kb-docs", TenantID: 1, EmbeddingModelID: "embed-1",
		IndexingStrategy: types.IndexingStrategy{VectorEnabled: true},
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))

	params, err := s.buildRetrievalParams(ctx, engine, wiki, []*types.KnowledgeBase{wiki, docs},
		types.SearchParams{QueryText: "q"}, 50)
	require.NoError(t, err)
	require.Equal(t, []string{"embed-1"}, models.requested)
	require.NotEmpty(t, params)
	require.Len(t, params[0].Embedding, 768)
}

func TestBuildRetrievalParamsPreservesKnowledgeExclusions(t *testing.T) {
	s := &knowledgeBaseService{}
	engine := buildBoundComposite(t, &fakeRetrieveEngineService{
		engineType: types.PostgresRetrieverEngineType,
		support:    []types.RetrieverType{types.VectorRetrieverType, types.KeywordsRetrieverType},
	})
	docs := &types.KnowledgeBase{
		ID: "docs", TenantID: 1, EmbeddingModelID: "embed",
		IndexingStrategy: types.IndexingStrategy{VectorEnabled: true, KeywordEnabled: true},
	}
	faq := &types.KnowledgeBase{
		ID: "faq", TenantID: 1, Type: types.KnowledgeBaseTypeFAQ, EmbeddingModelID: "embed",
		IndexingStrategy: types.IndexingStrategy{VectorEnabled: true},
	}
	ctx := context.WithValue(t.Context(), types.TenantIDContextKey, uint64(1))
	for _, excluded := range [][]string{nil, {"current-knowledge"}} {
		params, err := s.buildRetrievalParams(ctx, engine, docs, []*types.KnowledgeBase{docs, faq}, types.SearchParams{
			QueryText: "config", QueryEmbedding: []float32{1, 0}, ExcludeKnowledgeIDs: excluded,
		}, 5)
		require.NoError(t, err)
		require.Len(t, params, 3)
		for _, p := range params {
			require.Equal(t, excluded, p.ExcludeKnowledgeIDs)
			require.Equal(t, 5, p.TopK)
		}
	}
}

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

// Document knowledge bases with question generation off must drop historical
// question rows before TopK. FAQ knowledge bases use the same source-id shape
// for their own similar questions, so they stay off that list.
func TestBuildRetrievalParamsExcludesGeneratedQuestionsOnlyForDocumentKBs(t *testing.T) {
	s := &knowledgeBaseService{}
	engine := buildBoundComposite(t, &fakeRetrieveEngineService{
		engineType: types.PostgresRetrieverEngineType,
		support:    []types.RetrieverType{types.VectorRetrieverType, types.KeywordsRetrieverType},
	})
	docOff := &types.KnowledgeBase{
		ID: "doc-off", TenantID: 1, Type: types.KnowledgeBaseTypeDocument,
		EmbeddingModelID: "embed-1",
		IndexingStrategy: types.IndexingStrategy{VectorEnabled: true, KeywordEnabled: true},
	}
	docOn := &types.KnowledgeBase{
		ID: "doc-on", TenantID: 1, Type: types.KnowledgeBaseTypeDocument,
		EmbeddingModelID:         "embed-1",
		IndexingStrategy:         types.IndexingStrategy{VectorEnabled: true, KeywordEnabled: true},
		QuestionGenerationConfig: &types.QuestionGenerationConfig{Enabled: true},
	}
	faq := &types.KnowledgeBase{
		ID: "faq", TenantID: 1, Type: types.KnowledgeBaseTypeFAQ,
		EmbeddingModelID: "embed-1",
		IndexingStrategy: types.IndexingStrategy{VectorEnabled: true, KeywordEnabled: true},
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))

	params, err := s.buildRetrievalParams(ctx, engine, docOff,
		[]*types.KnowledgeBase{docOff, docOn, faq},
		types.SearchParams{QueryText: "q", QueryEmbedding: []float32{1}}, 5)
	require.NoError(t, err)

	var docVector, faqVector, keyword *types.RetrieveParams
	for i := range params {
		p := &params[i]
		switch {
		case p.RetrieverType == types.VectorRetrieverType && p.KnowledgeType == types.KnowledgeTypeFAQ:
			faqVector = p
		case p.RetrieverType == types.VectorRetrieverType:
			docVector = p
		case p.RetrieverType == types.KeywordsRetrieverType:
			keyword = p
		}
	}
	require.NotNil(t, docVector)
	require.NotNil(t, faqVector)
	require.NotNil(t, keyword)
	require.Equal(t, []string{"doc-off"}, docVector.ExcludeGeneratedQuestionKBIDs)
	require.Empty(t, faqVector.ExcludeGeneratedQuestionKBIDs)
	require.Equal(t, []string{"doc-off"}, keyword.ExcludeGeneratedQuestionKBIDs)
	require.ElementsMatch(t, []string{"doc-off", "doc-on"}, docVector.KnowledgeBaseIDs)
	require.Equal(t, []string{"faq"}, faqVector.KnowledgeBaseIDs)
	require.NotContains(t, keyword.KnowledgeBaseIDs, "faq")
}

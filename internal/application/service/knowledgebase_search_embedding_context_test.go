package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type queryContextEmbedder struct {
	embedding.Embedder
	query bool
}

func (e *queryContextEmbedder) Embed(ctx context.Context, _ string) ([]float32, error) {
	e.query, _ = ctx.Value(types.EmbedQueryContextKey).(bool)
	return []float32{1}, nil
}

type queryContextModelService struct {
	interfaces.ModelService
	embedder embedding.Embedder
}

func (s *queryContextModelService) GetEmbeddingModel(context.Context, string) (embedding.Embedder, error) {
	return s.embedder, nil
}

func (s *queryContextModelService) GetEmbeddingModelForTenant(
	context.Context, string, uint64,
) (embedding.Embedder, error) {
	return s.embedder, nil
}

type queryContextKBRepository struct {
	interfaces.KnowledgeBaseRepository
	kb *types.KnowledgeBase
}

func (r *queryContextKBRepository) GetKnowledgeBaseByID(
	context.Context, string,
) (*types.KnowledgeBase, error) {
	return r.kb, nil
}

func TestKnowledgeBaseQueryEmbeddingMarksQueryContext(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	embedder := &queryContextEmbedder{}
	kb := &types.KnowledgeBase{ID: "kb-1", TenantID: 7, EmbeddingModelID: "model-1"}
	service := &knowledgeBaseService{
		repo:         &queryContextKBRepository{kb: kb},
		modelService: &queryContextModelService{embedder: embedder},
	}

	_, err := service.GetQueryEmbedding(ctx, kb.ID, "first query")
	require.NoError(t, err)
	require.True(t, embedder.query)

	embedder.query = false
	_, err = service.resolveQueryEmbedding(ctx, kb, types.SearchParams{QueryText: "fallback query"}, 7)
	require.NoError(t, err)
	require.True(t, embedder.query)
}

package service

import (
	"context"
	"fmt"
	"sync"
	"testing"

	chatpipeline "github.com/Tencent/WeKnora/internal/application/service/chat_pipeline"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveEmbeddingModelKeys_Dimensions(t *testing.T) {
	t.Parallel()

	models := make(map[string]*types.Model)
	for id, dimension := range map[string]int{"full": 1536, "short": 512, "shared": 1536} {
		model := buildModel(id, "text-embedding-3-small", "https://embedding.example/v1")
		model.Parameters.EmbeddingParameters = types.EmbeddingParameters{
			Dimension:                 dimension,
			SupportsDimensionOverride: true,
		}
		models[id] = model
	}
	svc := newKBSvcForValidate(&fakeModelSvcForKeys{byID: models})
	keys := svc.ResolveEmbeddingModelKeys(context.Background(), []*types.KnowledgeBase{
		{ID: "kb-full", EmbeddingModelID: "full", TenantID: 1},
		{ID: "kb-short", EmbeddingModelID: "short", TenantID: 1},
		{ID: "kb-shared", EmbeddingModelID: "shared", TenantID: 2},
	})

	require.Len(t, keys, 3)
	assert.NotEqual(t, keys["kb-full"], keys["kb-short"],
		"different vector widths must not share a precomputed query embedding")
	assert.Equal(t, keys["kb-full"], keys["kb-shared"],
		"compatible cross-tenant knowledge bases should still reuse the query embedding")
}

func TestValidateSameEmbeddingModel_Dimensions(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		dimensions [2]int
		wantError  bool
	}{
		{name: "different widths", dimensions: [2]int{1536, 512}, wantError: true},
		{name: "same width", dimensions: [2]int{1536, 1536}},
		{name: "unknown and explicit width", dimensions: [2]int{0, 512}, wantError: true},
		{name: "legacy unspecified widths", dimensions: [2]int{0, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			models := make(map[string]*types.Model)
			for i, id := range []string{"model-a", "model-b"} {
				model := buildModel(id, "text-embedding-3-small", "https://embedding.example/v1")
				model.Parameters.EmbeddingParameters = types.EmbeddingParameters{
					Dimension:                 tc.dimensions[i],
					SupportsDimensionOverride: true,
				}
				models[id] = model
			}
			svc := newKBSvcForValidate(&fakeModelSvcForKeys{byID: models})
			err := svc.validateSameEmbeddingModel(context.Background(), []*types.KnowledgeBase{
				{ID: "kb-a", EmbeddingModelID: "model-a", TenantID: 1},
				{ID: "kb-b", EmbeddingModelID: "model-b", TenantID: 2},
			})
			if !tc.wantError {
				require.NoError(t, err)
				return
			}
			require.Error(t, err, "incompatible vector spaces must be rejected before retrieval")
			app, ok := apperrors.IsAppError(err)
			require.True(t, ok, "expected AppError, got %T", err)
			assert.Equal(t, apperrors.ErrBadRequest, app.Code)
		})
	}
}

// Keep the real model-key resolution while recording the vector passed to
// each KB. This exercises the chat pipeline's grouping and embedding reuse,
// rather than assuming a different key is enough to prevent missing recall.
type dimensionSearchService struct {
	interfaces.KnowledgeBaseService
	kbs        []*types.KnowledgeBase
	dimensions map[string]int
	mu         sync.Mutex
	embedCalls int
}

func (s *dimensionSearchService) GetKnowledgeBasesByIDsOnly(
	context.Context, []string,
) ([]*types.KnowledgeBase, error) {
	return s.kbs, nil
}

func (s *dimensionSearchService) GetQueryEmbedding(_ context.Context, kbID, _ string) ([]float32, error) {
	s.mu.Lock()
	s.embedCalls++
	s.mu.Unlock()
	return make([]float32, s.dimensions[kbID]), nil
}

func (s *dimensionSearchService) HybridSearch(
	_ context.Context, _ string, params types.SearchParams,
) ([]*types.SearchResult, error) {
	var results []*types.SearchResult
	for _, id := range params.KnowledgeBaseIDs {
		if len(params.QueryEmbedding) != s.dimensions[id] {
			return nil, fmt.Errorf("query has %d dimensions, knowledge base %s expects %d",
				len(params.QueryEmbedding), id, s.dimensions[id])
		}
		results = append(results, &types.SearchResult{ID: "chunk-" + id, KnowledgeBaseID: id})
	}
	return results, nil
}

func TestSearchPipeline_GroupsByEmbeddingDimension(t *testing.T) {
	t.Parallel()

	models := make(map[string]*types.Model)
	svc := &dimensionSearchService{dimensions: map[string]int{"kb-full": 1536, "kb-short": 512, "kb-shared": 1536}}
	var targets types.SearchTargets
	for _, id := range []string{"kb-full", "kb-short", "kb-shared"} {
		model := buildModel(id, "text-embedding-3-small", "https://embedding.example/v1")
		model.Parameters.EmbeddingParameters.Dimension = svc.dimensions[id]
		model.Parameters.EmbeddingParameters.SupportsDimensionOverride = true
		models[id] = model
		tenantID := uint64(1)
		if id == "kb-shared" {
			tenantID = 2
		}
		svc.kbs = append(svc.kbs, &types.KnowledgeBase{ID: id, EmbeddingModelID: id, TenantID: tenantID})
		targets = append(targets, &types.SearchTarget{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: id})
	}
	svc.KnowledgeBaseService = newKBSvcForValidate(&fakeModelSvcForKeys{byID: models})
	plugin := chatpipeline.NewPluginSearch(chatpipeline.NewEventManager(), svc, nil, nil, nil, nil, nil, nil, nil, nil)
	chat := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{SearchTargets: targets, EmbeddingTopK: 10},
		PipelineState:   types.PipelineState{RewriteQuery: "a shared question"},
	}
	err := plugin.OnEvent(context.Background(), types.CHUNK_SEARCH, chat, func() *chatpipeline.PluginError {
		return nil
	})
	require.Nil(t, err)
	require.Len(t, chat.SearchResult, 3, "every KB must be searched in its configured vector space")
	assert.ElementsMatch(t, []string{"chunk-kb-full", "chunk-kb-short", "chunk-kb-shared"},
		[]string{chat.SearchResult[0].ID, chat.SearchResult[1].ID, chat.SearchResult[2].ID})
	assert.Equal(t, 2, svc.embedCalls, "same-width KBs should share one embedding even across tenants")
}

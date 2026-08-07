package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildRetrievalParams_PropagatesMetadataFilter(t *testing.T) {
	t.Parallel()

	filter := &types.MetadataFilter{
		Field: "department",
		Op:    types.MetadataFilterOpEqual,
		Value: "research",
	}
	engine := buildBoundComposite(t, &fakeRetrieveEngineService{
		engineType: types.PostgresRetrieverEngineType,
		support: []types.RetrieverType{
			types.VectorRetrieverType,
			types.KeywordsRetrieverType,
		},
	})
	kb := &types.KnowledgeBase{
		ID:               "kb-1",
		TenantID:         1,
		EmbeddingModelID: "embedding-model",
		IndexingStrategy: types.DefaultIndexingStrategy(),
	}

	params, err := (&knowledgeBaseService{}).buildRetrievalParams(
		ctxWithTenant(1), engine, kb, []*types.KnowledgeBase{kb}, types.SearchParams{
			QueryText:        "expense policy",
			QueryEmbedding:   []float32{0.1},
			MetadataFilter:   filter,
			VectorThreshold:  0.2,
			KeywordThreshold: 0.3,
		}, 10)
	require.NoError(t, err)
	require.Len(t, params, 2)

	for _, param := range params {
		assert.Same(t, filter, param.MetadataFilter)
		assert.Equal(t, "department", param.MetadataFilter.Field)
	}
}

type metadataFilterKBRepo struct {
	*fakeKBRepo
	kbs []*types.KnowledgeBase
}

func (r *metadataFilterKBRepo) GetKnowledgeBaseByIDs(
	context.Context, []string,
) ([]*types.KnowledgeBase, error) {
	return r.kbs, nil
}

func TestHybridSearch_MetadataFilterUnsupportedFailsClosedBeforeAnyRetrieve(t *testing.T) {
	t.Parallel()

	const (
		postgresStoreID = "00000000-0000-0000-0000-000000000011"
		elasticStoreID  = "00000000-0000-0000-0000-000000000012"
	)
	postgres := &fakeRetrieveEngineService{
		engineType: types.PostgresRetrieverEngineType,
		support:    []types.RetrieverType{types.KeywordsRetrieverType},
	}
	elastic := &fakeRetrieveEngineService{
		engineType: types.ElasticsearchRetrieverEngineType,
		support:    []types.RetrieverType{types.KeywordsRetrieverType},
	}
	repo := &metadataFilterKBRepo{
		fakeKBRepo: newFakeKBRepo(),
		kbs: []*types.KnowledgeBase{
			{ID: "kb-postgres", TenantID: 1, VectorStoreID: ptr(postgresStoreID), IndexingStrategy: types.DefaultIndexingStrategy()},
			{ID: "kb-elastic", TenantID: 1, VectorStoreID: ptr(elasticStoreID), IndexingStrategy: types.DefaultIndexingStrategy()},
		},
	}
	svc := &knowledgeBaseService{
		repo: repo,
		retrieveEngine: &fakeFanoutRegistry{byStore: map[string]interfaces.RetrieveEngineService{
			postgresStoreID: postgres,
			elasticStoreID:  elastic,
		}},
		ownership: &fakeOwnership{owned: map[string]uint64{
			postgresStoreID: 1,
			elasticStoreID:  1,
		}},
		modelService: &fakeModelSvcForKeys{},
	}

	_, err := svc.HybridSearch(ctxWithTenant(1), "kb-postgres", types.SearchParams{
		QueryText:          "expense policy",
		MatchCount:         1,
		KnowledgeBaseIDs:   []string{"kb-postgres", "kb-elastic"},
		DisableVectorMatch: true,
		MetadataFilter: &types.MetadataFilter{
			Field: "department",
			Op:    types.MetadataFilterOpEqual,
			Value: "research",
		},
	})
	require.Error(t, err)
	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok, "expected typed AppError, got %T", err)
	assert.Equal(t, apperrors.ErrMetadataFilterUnsupported, appErr.Code)
	assert.Equal(t, 400, appErr.HTTPCode)
	assert.NotContains(t, appErr.Message, postgresStoreID)
	assert.NotContains(t, appErr.Message, elasticStoreID)
	assert.Equal(t, int64(0), postgres.retrieveCalls.Load())
	assert.Equal(t, int64(0), elastic.retrieveCalls.Load())
}

func ptr(value string) *string { return &value }

var _ retriever.TenantStoreOwnership = (*fakeOwnership)(nil)

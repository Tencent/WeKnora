package retriever

import (
	"context"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository/retriever/tencentvectordb"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeletePreviousModelVectorsRespectsPhysicalNamespace(t *testing.T) {
	shared := &mockEngineService{
		engineType: types.PostgresRetrieverEngineType,
		namespace:  func(int, string) string { return "embeddings" },
	}
	dimensionScoped := &mockEngineService{
		engineType: types.QdrantRetrieverEngineType,
		namespace: func(dimension int, _ string) string {
			return fmt.Sprintf("vectors_%d", dimension)
		},
	}
	engine := &CompositeRetrieveEngine{engineInfos: []*engineInfo{
		{retrieveEngine: shared},
		{retrieveEngine: dimensionScoped},
	}}

	require.NoError(t, engine.DeletePreviousModelVectors(
		context.Background(), []string{"knowledge-1"}, 768, 1536, "document",
	))
	assert.Empty(t, shared.knowledgeDeletes, "shared namespace cleanup would delete the replacement")
	assert.Equal(t, []int{768}, dimensionScoped.knowledgeDeletes)
}

func TestDeletePreviousModelVectorsSkipsEqualDimensionModelChange(t *testing.T) {
	dimensionScoped := &mockEngineService{
		engineType: types.QdrantRetrieverEngineType,
		namespace: func(dimension int, _ string) string {
			return fmt.Sprintf("vectors_%d", dimension)
		},
	}
	engine := &CompositeRetrieveEngine{engineInfos: []*engineInfo{{retrieveEngine: dimensionScoped}}}

	require.NoError(t, engine.DeletePreviousModelVectors(
		context.Background(), []string{"knowledge-1"}, 1536, 1536, "document",
	))
	assert.Empty(t, dimensionScoped.knowledgeDeletes)
}

func TestTencentVectorDBPhysicalNamespaceUsesDimensionSuffixByDefault(t *testing.T) {
	indexRepo := tencentvectordb.NewTencentVectorDBRetrieveEngineRepository(nil, "", nil)
	service := NewKVHybridRetrieveEngine(indexRepo, types.TencentVectorDBRetrieverEngineType).(*KeywordsVectorHybridRetrieveEngineService)

	assert.NotEqual(t,
		service.PhysicalIndexNamespace(768, "document"),
		service.PhysicalIndexNamespace(1536, "document"),
	)
}

func TestTencentVectorDBPhysicalNamespaceTreatsConfiguredCollectionAsShared(t *testing.T) {
	indexRepo := tencentvectordb.NewTencentVectorDBRetrieveEngineRepository(nil, "", &types.IndexConfig{
		CollectionName: "custom_collection",
	})
	service := NewKVHybridRetrieveEngine(indexRepo, types.TencentVectorDBRetrieverEngineType).(*KeywordsVectorHybridRetrieveEngineService)

	assert.Equal(t,
		service.PhysicalIndexNamespace(768, "document"),
		service.PhysicalIndexNamespace(1536, "document"),
	)
}

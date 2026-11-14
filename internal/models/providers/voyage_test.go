package providers_test

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/providers"
	modelruntime "github.com/Tencent/WeKnora/internal/models/runtime"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVoyageProvider(t *testing.T) {
	v, ok := modelruntime.Get(providers.VoyageID)
	require.True(t, ok)
	assert.Equal(t, []types.ModelType{types.ModelTypeEmbedding, types.ModelTypeRerank}, v.ModelTypes)
	assert.Equal(t, "https://api.voyageai.com/v1", v.GetDefaultURL(types.ModelTypeEmbedding))
	assert.Equal(t, "https://api.voyageai.com/v1", v.GetDefaultURL(types.ModelTypeRerank))
	assert.Equal(t, providers.VoyageID, modelruntime.DetectByURL("https://api.voyageai.com/v1/embeddings"))

	// A key is required and so is a model: the provider supplies neither.
	assert.Error(t, v.ValidateConfig(&providers.Config{ModelName: "voyage-4"}))
	assert.Error(t, v.ValidateConfig(&providers.Config{APIKey: "k"}))
	assert.NoError(t, v.ValidateConfig(&providers.Config{ModelName: "voyage-4", APIKey: "k"}))
}

// The model a row names decides which Voyage endpoint it talks to; a wrong
// assignment sends a request shaped for another endpoint, which Voyage
// answers with a 400.
func TestVoyageEmbeddingModelsResolveToTheirEndpoint(t *testing.T) {
	want := map[string]api.EmbeddingAPI{
		"voyage-4-large": api.EmbeddingVoyage, "voyage-4": api.EmbeddingVoyage,
		"voyage-4-lite": api.EmbeddingVoyage, "voyage-code-4": api.EmbeddingVoyage,
		"voyage-3-large": api.EmbeddingVoyage, "voyage-3.5": api.EmbeddingVoyage,
		"voyage-3.5-lite": api.EmbeddingVoyage, "voyage-code-3": api.EmbeddingVoyage,
		"voyage-finance-2": api.EmbeddingVoyage, "voyage-law-2": api.EmbeddingVoyage,
		"voyage-context-4": api.EmbeddingVoyageContextualized, "voyage-context-3": api.EmbeddingVoyageContextualized,
		"voyage-multimodal-3.5": api.EmbeddingVoyageMultimodal, "voyage-multimodal-3": api.EmbeddingVoyageMultimodal,
	}
	for model, protocol := range want {
		r, err := modelruntime.Resolve(modelruntime.Ref{
			Provider: providers.VoyageID, Model: model, ModelType: types.ModelTypeEmbedding,
		})
		require.NoError(t, err, model)
		assert.True(t, r.Cataloged, model)
		assert.Equal(t, protocol, r.EmbeddingAPI, model)
		assert.Equal(t, 1000, r.Embeddings.MaxBatchSize, model)
		assert.Equal(t, 1024, r.Spec.Dimension, model)
		require.NotNil(t, r.Spec.Cost, model)
		assert.Equal(t, r.Spec.AcceptsImages(), protocol == api.EmbeddingVoyageMultimodal, model)
	}
	// A model the catalog does not know resolves to the plain endpoint and
	// is left to Voyage to accept or reject.
	r, err := modelruntime.Resolve(modelruntime.Ref{
		Provider: providers.VoyageID, Model: "voyage-5-future", ModelType: types.ModelTypeEmbedding,
	})
	require.NoError(t, err)
	assert.False(t, r.Cataloged)
	assert.Equal(t, api.EmbeddingVoyage, r.EmbeddingAPI)
}

// Only the models Voyage documents with a single width must never be sent
// output_dimension.
func TestVoyageFixedWidthModelsSendNoDimension(t *testing.T) {
	fixed := map[string]bool{"voyage-finance-2": true, "voyage-law-2": true, "voyage-multimodal-3": true}
	for _, model := range []string{
		"voyage-4-large", "voyage-4", "voyage-code-4", "voyage-3.5-lite", "voyage-context-4",
		"voyage-context-3", "voyage-multimodal-3.5", "voyage-finance-2", "voyage-law-2", "voyage-multimodal-3",
	} {
		r, err := modelruntime.Resolve(modelruntime.Ref{
			Provider: providers.VoyageID, Model: model, ModelType: types.ModelTypeEmbedding,
		})
		require.NoError(t, err, model)
		if fixed[model] {
			assert.Empty(t, r.Embeddings.DimensionsField, model)
		} else {
			assert.Equal(t, "output_dimension", r.Embeddings.DimensionsField, model)
		}
	}
}

func TestVoyageRerankModels(t *testing.T) {
	for _, model := range []string{"rerank-3", "rerank-3-lite", "rerank-2.5", "rerank-2.5-lite"} {
		r, err := modelruntime.Resolve(modelruntime.Ref{
			Provider: providers.VoyageID, Model: model, ModelType: types.ModelTypeRerank,
		})
		require.NoError(t, err, model)
		assert.True(t, r.Cataloged, model)
		assert.Equal(t, api.RerankVoyage, r.RerankAPI, model)
		assert.Equal(t, 1000, r.Rerank.MaxDocuments, model)
		assert.True(t, r.Rerank.SendReturnDocs, model)
		assert.False(t, r.Rerank.SendTopN, model)
		require.NotNil(t, r.Spec.Cost, model)
	}
}

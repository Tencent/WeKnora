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

func TestDeepInfraRerankCatalog(t *testing.T) {
	v, ok := modelruntime.Get(providers.DeepInfraID)
	require.True(t, ok)
	assert.Equal(t, []types.ModelType{types.ModelTypeRerank}, v.ModelTypes)
	assert.Equal(t, providers.DeepInfraRerankBaseURL, v.GetDefaultURL(types.ModelTypeRerank))
	assert.Equal(t, providers.DeepInfraID, modelruntime.DetectByURL(providers.DeepInfraRerankBaseURL))
	assert.Equal(t, providers.GenericID, modelruntime.DetectByURL("https://api.deepinfra.com/v1/openai"))
	assert.Error(t, v.ValidateConfig(&providers.Config{ModelName: "Qwen/Qwen3-Reranker-4B"}))
	assert.NoError(t, v.ValidateConfig(&providers.Config{ModelName: "Qwen/Qwen3-Reranker-4B", APIKey: "k"}))
	r, err := modelruntime.Resolve(modelruntime.Ref{
		Provider: providers.DeepInfraID, Model: "Qwen/Qwen3-Reranker-4B", ModelType: types.ModelTypeRerank,
	})
	require.NoError(t, err)
	assert.True(t, r.Cataloged)
	assert.Equal(t, api.RerankPairwise, r.RerankAPI)
	assert.True(t, r.RerankAPI.Known())
	assert.True(t, r.RerankAPI.HTTPServed())
	assert.Equal(t, 1024, r.Rerank.MaxDocuments)
	assert.Equal(t, api.ScoreProbability, r.Rerank.ScoreScale)
	assert.Zero(t, r.Rerank.MaxQueryChars)
	assert.Zero(t, r.Rerank.MaxDocumentChars)
	assert.Zero(t, r.Rerank.MaxRequestChars)
	assert.False(t, r.Rerank.SendTopN)
	assert.False(t, r.Rerank.SendReturnDocs)
}

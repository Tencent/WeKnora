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

func TestCohereRerankCatalog(t *testing.T) {
	v, ok := modelruntime.Get("cohere")
	require.True(t, ok)
	assert.Equal(t, []types.ModelType{types.ModelTypeRerank}, v.ModelTypes)
	assert.Equal(t, "https://api.cohere.com/v2", v.GetDefaultURL(types.ModelTypeRerank))
	assert.Equal(t, "cohere", modelruntime.DetectByURL("https://api.cohere.com/v2/rerank"))
	assert.Error(t, v.ValidateConfig(&providers.Config{ModelName: "rerank-v4.0-pro"}))
	assert.NoError(t, v.ValidateConfig(&providers.Config{ModelName: "rerank-v4.0-pro", APIKey: "k"}))
	for _, model := range []string{"rerank-v4.0-pro", "rerank-v4.0-fast", "rerank-v3.5"} {
		r, err := modelruntime.Resolve(modelruntime.Ref{
			Provider: "cohere", Model: model, ModelType: types.ModelTypeRerank,
		})
		require.NoError(t, err)
		assert.True(t, r.Cataloged, model)
		assert.Equal(t, api.RerankCohere, r.RerankAPI)
		assert.Equal(t, 1000, r.Rerank.MaxDocuments)
		assert.Equal(t, api.ScoreProbability, r.Rerank.ScoreScale)
		assert.False(t, r.Rerank.SendTopN)
		assert.False(t, r.Rerank.SendReturnDocs)
	}
}

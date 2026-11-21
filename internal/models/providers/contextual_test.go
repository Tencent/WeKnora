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

// Model IDs and endpoint follow https://docs.contextual.ai/api-reference/rerank/rerank.
func TestContextualRerankCatalog(t *testing.T) {
	v, ok := modelruntime.Get("contextual")
	require.True(t, ok)
	assert.Equal(t, []types.ModelType{types.ModelTypeRerank}, v.ModelTypes)
	assert.Equal(t, providers.AuthBearer, v.Auth)
	assert.Equal(t, "https://api.contextual.ai/v1", v.GetDefaultURL(types.ModelTypeRerank))
	for _, base := range []string{"https://api.contextual.ai/v1", "https://api.contextual.ai/v1/rerank"} {
		assert.Equal(t, "contextual", modelruntime.DetectByURL(base))
	}
	assert.Error(t, v.ValidateConfig(&providers.Config{ModelName: "ctxl-rerank-v2-instruct-multilingual"}))
	assert.NoError(t, v.ValidateConfig(&providers.Config{
		ModelName: "ctxl-rerank-v2-instruct-multilingual", APIKey: "k",
	}))
	models := []string{
		"ctxl-rerank-v2-instruct-multilingual",
		"ctxl-rerank-v2-instruct-multilingual-mini",
		"ctxl-rerank-v1-instruct",
	}
	require.Len(t, v.Models(), len(models))
	for _, model := range models {
		r, err := modelruntime.Resolve(modelruntime.Ref{
			Provider: "contextual", Model: model, ModelType: types.ModelTypeRerank,
		})
		require.NoError(t, err)
		assert.True(t, r.Cataloged, model)
		assert.Equal(t, api.RerankCohere, r.RerankAPI)
		assert.Equal(t, api.ScoreProbability, r.Rerank.ScoreScale)
		assert.True(t, r.Rerank.SendTopN)
		assert.False(t, r.Rerank.SendReturnDocs)
		assert.False(t, r.Rerank.AcceptsTruncatePromptTokens)
		assert.Empty(t, r.Rerank.ExtraBody)
		// The reference states token budgets, not document or character limits.
		assert.Zero(t, r.Rerank.MaxDocuments)
		assert.Zero(t, r.Rerank.MaxQueryChars)
		assert.Zero(t, r.Rerank.MaxDocumentChars)
		assert.Zero(t, r.Rerank.MaxRequestChars)
	}
}

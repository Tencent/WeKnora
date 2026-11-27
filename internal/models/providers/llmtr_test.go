package providers_test

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/models/providers"
	modelruntime "github.com/Tencent/WeKnora/internal/models/runtime"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLlmtrCatalog(t *testing.T) {
	v, ok := modelruntime.Get("llmtr")
	require.True(t, ok)
	for _, mt := range []types.ModelType{
		types.ModelTypeKnowledgeQA, types.ModelTypeEmbedding, types.ModelTypeVLLM,
	} {
		assert.Equal(t, "https://llmtr.com/v1", v.GetDefaultURL(mt), mt)
	}
	assert.Equal(t, "llmtr", modelruntime.DetectByURL("https://llmtr.com/v1"))
	assert.Error(t, v.ValidateConfig(&providers.Config{ModelName: "qwen/qwen3.8-flash"}))
	assert.NoError(t, v.ValidateConfig(&providers.Config{
		ModelName: "qwen/qwen3.8-flash", APIKey: "k",
	}))

	for _, model := range []string{
		"qwen/qwen3.8-flash",
		"qwen/qwen3.8-max",
		"qwen/qwen3.8-27b-free",
		"google/gemini-3.8-flash",
		"minimax/minimax-m3.1-flash",
		"zai/glm-5.3",
	} {
		r, err := modelruntime.Resolve(modelruntime.Ref{
			Provider: "llmtr", Model: model, ModelType: types.ModelTypeKnowledgeQA,
		})
		require.NoError(t, err, model)
		assert.True(t, r.Cataloged, model)
		assert.Equal(t, "max_tokens", r.OpenAICompletions.MaxTokensField, model)
	}
}

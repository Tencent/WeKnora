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

func TestStandardComputeCatalog(t *testing.T) {
	v, ok := modelruntime.Get("standardcompute")
	require.True(t, ok)
	for _, mt := range []types.ModelType{types.ModelTypeKnowledgeQA, types.ModelTypeVLLM} {
		assert.Equal(t, "https://api.stdcmpt.com/v1", v.GetDefaultURL(mt), mt)
	}
	assert.Equal(t, "standardcompute", modelruntime.DetectByURL("https://api.stdcmpt.com/v1"))
	assert.Error(t, v.ValidateConfig(&providers.Config{ModelName: "StandardCompute"}))
	assert.NoError(t, v.ValidateConfig(&providers.Config{ModelName: "StandardCompute", APIKey: "k"}))

	for _, model := range []string{"StandardCompute", "deepseek/deepseek-v4.1-flash", "z-ai/glm-5.3"} {
		r, err := modelruntime.Resolve(modelruntime.Ref{
			Provider: "standardcompute", Model: model, ModelType: types.ModelTypeKnowledgeQA,
		})
		require.NoError(t, err, model)
		assert.True(t, r.Cataloged, model)
		assert.Equal(t, "max_tokens", r.OpenAICompletions.MaxTokensField, model)
		assert.True(t, r.OpenAICompletions.SupportsReasoningEffort, model)
		assert.True(t, r.ThinkingLevels.Supports(api.ReasoningHigh), model)
		assert.False(t, r.ThinkingLevels.Supports(api.ReasoningMax), model)
	}
}

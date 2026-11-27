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

func TestCloudGPUCatalog(t *testing.T) {
	v, ok := modelruntime.Get("cloudgpu")
	require.True(t, ok)
	assert.Equal(t, "https://cloudgpu.app/v1", v.GetDefaultURL(types.ModelTypeKnowledgeQA))
	assert.True(t, v.SupportsType(types.ModelTypeKnowledgeQA))
	assert.False(t, v.SupportsType(types.ModelTypeEmbedding))
	assert.False(t, v.SupportsType(types.ModelTypeVLLM))
	assert.Equal(t, "cloudgpu", modelruntime.DetectByURL("https://cloudgpu.app/v1"))
	assert.Error(t, v.ValidateConfig(&providers.Config{ModelName: "deepseek-v4-flash"}))
	assert.NoError(t, v.ValidateConfig(&providers.Config{ModelName: "deepseek-v4-flash", APIKey: "k"}))

	windows := map[string]int{
		"deepseek-v4-pro":   131072,
		"deepseek-v4-flash": 131072,
		"kimi-k3":           262144,
		"glm-5.2":           131072,
		"minimax-m3":        1000000,
		"qwen3-coder-480b":  262144,
		"qwen3.8-27b":       262144,
	}
	for model, window := range windows {
		r, err := modelruntime.Resolve(modelruntime.Ref{
			Provider: "cloudgpu", Model: model, ModelType: types.ModelTypeKnowledgeQA,
		})
		require.NoError(t, err, model)
		assert.True(t, r.Cataloged, model)
		assert.False(t, r.Spec.Reasoning, model)
		assert.Equal(t, []string{"text"}, r.Spec.Input, model)
		assert.Equal(t, window, r.Spec.ContextWindow, model)
		assert.Nil(t, r.Spec.Cost, model)
		assert.Equal(t, "max_completion_tokens", r.OpenAICompletions.MaxTokensField, model)
		assert.Equal(t, api.ThinkingFormatNone, r.OpenAICompletions.ThinkingFormat, model)
		assert.False(t, r.OpenAICompletions.SupportsReasoningEffort, model)
	}
}

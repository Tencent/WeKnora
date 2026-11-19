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

func TestRuncrateCatalog(t *testing.T) {
	v, ok := modelruntime.Get("runcrate")
	require.True(t, ok)
	for _, mt := range []types.ModelType{types.ModelTypeKnowledgeQA, types.ModelTypeEmbedding, types.ModelTypeVLLM} {
		assert.Equal(t, "https://api.runcrate.ai/v1", v.GetDefaultURL(mt), mt)
	}
	assert.Equal(t, "runcrate", modelruntime.DetectByURL("https://api.runcrate.ai/v1"))
	assert.Error(t, v.ValidateConfig(&providers.Config{ModelName: "deepseek-ai/DeepSeek-V4.1-Flash"}))
	assert.NoError(t, v.ValidateConfig(&providers.Config{ModelName: "deepseek-ai/DeepSeek-V4.1-Flash", APIKey: "k"}))

	r, err := modelruntime.Resolve(modelruntime.Ref{
		Provider: "runcrate", Model: "deepseek-ai/DeepSeek-V4.1-Flash", ModelType: types.ModelTypeKnowledgeQA,
	})
	require.NoError(t, err)
	assert.True(t, r.Cataloged)
	assert.Equal(t, "max_tokens", r.OpenAICompletions.MaxTokensField)
	assert.True(t, r.OpenAICompletions.SupportsReasoningEffort)
	assert.Equal(t, "none", r.ThinkingLevels.Value(api.ReasoningOff))
	assert.True(t, r.ThinkingLevels.Supports(api.ReasoningMax))

	// Reasoning on these models cannot be switched off, so "off" is not offered.
	for _, model := range []string{
		"zai-org/GLM-5.3", "zai-org/GLM-5.3-Flash", "Qwen/Qwen3.8-Flash", "openai/gpt-oss-120b",
	} {
		r, err := modelruntime.Resolve(modelruntime.Ref{
			Provider: "runcrate", Model: model, ModelType: types.ModelTypeKnowledgeQA,
		})
		require.NoError(t, err, model)
		assert.True(t, r.Cataloged, model)
		assert.False(t, r.ThinkingLevels.Supports(api.ReasoningOff), model)
	}

	for _, model := range []string{"Qwen/Qwen3-Embedding-8B", "BAAI/bge-m3"} {
		r, err := modelruntime.Resolve(modelruntime.Ref{
			Provider: "runcrate", Model: model, ModelType: types.ModelTypeEmbedding,
		})
		require.NoError(t, err, model)
		assert.True(t, r.Cataloged, model)
		assert.True(t, r.Embeddings.SendEncodingFormat, model)
		assert.Equal(t, "dimensions", r.Embeddings.DimensionsField, model)
	}
}

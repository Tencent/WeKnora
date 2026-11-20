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

func TestUberllmCatalog(t *testing.T) {
	v, ok := modelruntime.Get("uberllm")
	require.True(t, ok)
	for _, mt := range []types.ModelType{types.ModelTypeKnowledgeQA, types.ModelTypeVLLM, types.ModelTypeASR} {
		assert.Equal(t, "https://api.uberllm.dev/v1", v.GetDefaultURL(mt), mt)
	}
	assert.Equal(t, "uberllm", modelruntime.DetectByURL("https://api.uberllm.dev/v1"))
	assert.Error(t, v.ValidateConfig(&providers.Config{ModelName: "deepseek/deepseek-v4.1-flash"}))
	assert.NoError(t, v.ValidateConfig(&providers.Config{ModelName: "deepseek/deepseek-v4.1-flash", APIKey: "k"}))

	for _, model := range []string{"deepseek/deepseek-v4.1-flash", "z-ai/glm-5.3-flash"} {
		r, err := modelruntime.Resolve(modelruntime.Ref{
			Provider: "uberllm", Model: model, ModelType: types.ModelTypeKnowledgeQA,
		})
		require.NoError(t, err, model)
		assert.True(t, r.Cataloged, model)
		assert.Equal(t, "max_tokens", r.OpenAICompletions.MaxTokensField, model)
		assert.True(t, r.OpenAICompletions.SupportsReasoningEffort, model)
		assert.Equal(t, "none", r.ThinkingLevels.Value(api.ReasoningOff), model)
		assert.True(t, r.ThinkingLevels.Supports(api.ReasoningMax), model)
	}

	// Qwen3.8 Flash keeps reasoning with "none", so "off" is not offered.
	r, err := modelruntime.Resolve(modelruntime.Ref{
		Provider: "uberllm", Model: "qwen/qwen3.8-flash", ModelType: types.ModelTypeKnowledgeQA,
	})
	require.NoError(t, err)
	assert.True(t, r.Cataloged)
	assert.False(t, r.ThinkingLevels.Supports(api.ReasoningOff))

	// GLM-5.3 accepts only low, high and max; the other rungs clamp onto them.
	r, err = modelruntime.Resolve(modelruntime.Ref{
		Provider: "uberllm", Model: "z-ai/glm-5.3", ModelType: types.ModelTypeKnowledgeQA,
	})
	require.NoError(t, err)
	assert.True(t, r.Cataloged)
	assert.False(t, r.ThinkingLevels.Supports(api.ReasoningOff))
	assert.Equal(t, api.ReasoningLow, r.ThinkingLevels.Clamp(api.ReasoningMinimal))
	assert.Equal(t, api.ReasoningHigh, r.ThinkingLevels.Clamp(api.ReasoningMedium))
	assert.Equal(t, api.ReasoningMax, r.ThinkingLevels.Clamp(api.ReasoningXHigh))

	for _, model := range []string{"venice/whisper-large-v3", "elevenlabs/elevenlabs-scribe-v2"} {
		r, err := modelruntime.Resolve(modelruntime.Ref{
			Provider: "uberllm", Model: model, ModelType: types.ModelTypeASR,
		})
		require.NoError(t, err, model)
		assert.True(t, r.Cataloged, model)
		assert.Equal(t, 40<<20, r.Transcriptions.MaxFileBytes, model)
		assert.Equal(t, api.LanguageForm, r.Transcriptions.LanguageParam, model)
	}
}

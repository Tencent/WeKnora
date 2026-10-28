package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func defaultSettingsModels() []*Model {
	return []*Model{
		{ID: "chat-a", Type: ModelTypeKnowledgeQA, Status: ModelStatusActive},
		{ID: "chat-yaml", Type: ModelTypeKnowledgeQA, Status: ModelStatusActive, IsDefault: true},
		{ID: "chat-down", Type: ModelTypeKnowledgeQA, Status: ModelStatusDownloading},
		{ID: "rerank-a", Type: ModelTypeRerank, Status: ModelStatusActive},
	}
}

func TestApplyModelDefaultsOverridesDeploymentDefault(t *testing.T) {
	models := defaultSettingsModels()
	(&DefaultSettings{ChatModelID: "chat-a", RerankModelID: "rerank-a"}).ApplyModelDefaults(models)

	assert.True(t, models[0].IsDefault)
	assert.False(t, models[1].IsDefault, "the workspace choice replaces the YAML default")
	assert.True(t, models[3].IsDefault)
}

func TestApplyModelDefaultsIgnoresUnusableChoice(t *testing.T) {
	for name, id := range map[string]string{
		"missing":      "gone",
		"inactive":     "chat-down",
		"wrong type":   "rerank-a",
		"empty choice": "",
	} {
		t.Run(name, func(t *testing.T) {
			models := defaultSettingsModels()
			(&DefaultSettings{ChatModelID: id}).ApplyModelDefaults(models)
			assert.True(t, models[1].IsDefault, "the deployment default must still apply")
			assert.False(t, models[0].IsDefault)
		})
	}
}

func TestApplyModelDefaultsNilSettings(t *testing.T) {
	models := defaultSettingsModels()
	var settings *DefaultSettings
	settings.ApplyModelDefaults(models)
	assert.True(t, models[1].IsDefault)
}

func TestPickChatModel(t *testing.T) {
	models := defaultSettingsModels()
	assert.Equal(t, "chat-a", PickChatModel(models, "chat-a").ID)
	assert.Equal(t, "chat-yaml", PickChatModel(models, "").ID)
	assert.Equal(t, "chat-yaml", PickChatModel(models, "chat-down").ID, "an inactive preference falls back")
	assert.Equal(t, "chat-yaml", PickChatModel(models, "rerank-a").ID, "a non-chat preference falls back")

	models[1].IsDefault = false
	assert.Equal(t, "chat-a", PickChatModel(models, "").ID, "without a default the first active model is used")
	assert.Nil(t, PickChatModel(models[3:], ""))
}

func TestPickDefaultModelSkipsInactive(t *testing.T) {
	models := []*Model{
		{ID: "r1", Type: ModelTypeRerank, Status: ModelStatusDownloading, IsDefault: true},
		{ID: "r2", Type: ModelTypeRerank, Status: ModelStatusActive},
	}
	assert.Equal(t, "r2", PickDefaultModel(models, ModelTypeRerank).ID)
	assert.Nil(t, PickDefaultModel(models, ModelTypeEmbedding))
}

func TestPickUtilityModel(t *testing.T) {
	for _, id := range []string{"chat-a", "gone", "chat-down", "rerank-a", ""} {
		t.Run(id, func(t *testing.T) {
			models := defaultSettingsModels()
			(&DefaultSettings{UtilityModelID: id}).ApplyModelDefaults(models)
			want := "chat-yaml"
			if id == "chat-a" {
				want = id
			}
			assert.Equal(t, want, PickUtilityModel(models).ID)
			assert.Equal(t, "chat-yaml", PickChatModel(models, "").ID)
		})
	}
	assert.Nil(t, PickUtilityModel(nil))
}

func TestVisionDefaultDoesNotReplaceChatDefault(t *testing.T) {
	models := []*Model{
		{
			ID: "chat", Type: ModelTypeKnowledgeQA, Status: ModelStatusActive,
			Parameters: ModelParameters{SupportsVision: true},
		},
		{
			ID: "vision-chat", Type: ModelTypeKnowledgeQA, Status: ModelStatusActive,
			Parameters: ModelParameters{SupportsVision: true},
		},
		{ID: "vision-yaml", Type: ModelTypeVLLM, Status: ModelStatusActive, IsDefault: true},
		{ID: "asr", Type: ModelTypeASR, Status: ModelStatusActive},
	}
	(&DefaultSettings{ChatModelID: "chat", VLMModelID: "vision-chat", ASRModelID: "asr"}).ApplyModelDefaults(models)
	assert.Equal(t, "chat", PickDefaultModel(models, ModelTypeKnowledgeQA).ID)
	assert.Equal(t, "vision-chat", PickDefaultModel(models, ModelTypeVLLM).ID)
	assert.Equal(t, "asr", PickDefaultModel(models, ModelTypeASR).ID)
	assert.True(t, models[0].IsDefault)
	assert.False(t, models[1].IsDefault)
	assert.False(t, models[2].IsDefault)
	assert.Contains(t, models[1].DefaultFor, string(ModelTypeVLLM))
}

func TestVisionFallbackPrefersNativeDefaultOverChatDefault(t *testing.T) {
	models := []*Model{
		{
			ID: "chat", Type: ModelTypeKnowledgeQA, Status: ModelStatusActive, IsDefault: true,
			Parameters: ModelParameters{SupportsVision: true},
		},
		{ID: "vision", Type: ModelTypeVLLM, Status: ModelStatusActive, IsDefault: true},
	}
	(&DefaultSettings{VLMModelID: "deleted"}).ApplyModelDefaults(models)
	assert.Equal(t, "vision", PickDefaultModel(models, ModelTypeVLLM).ID)
}

package types

import (
	"context"
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

func TestUtilityModelIDFromContext(t *testing.T) {
	assert.Empty(t, UtilityModelIDFromContext(context.Background()))
	ctx := context.WithValue(context.Background(), TenantInfoContextKey,
		&Tenant{ID: 1, DefaultSettings: &DefaultSettings{UtilityModelID: " fast "}})
	assert.Equal(t, "fast", UtilityModelIDFromContext(ctx))
}

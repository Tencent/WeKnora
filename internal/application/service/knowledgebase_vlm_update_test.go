package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// vlmUpdateModelService resolves models from a fixed table, enough for the
// model check the KB update applies to vlm_config.
type vlmUpdateModelService struct {
	interfaces.ModelService
	models map[string]*types.Model
}

func (m *vlmUpdateModelService) GetModelByID(_ context.Context, id string) (*types.Model, error) {
	if model, ok := m.models[id]; ok {
		return model, nil
	}
	return nil, ErrModelNotFound
}

func newVLMUpdateService(repo *fakeKBRepo) *knowledgeBaseService {
	return &knowledgeBaseService{repo: repo, modelService: &vlmUpdateModelService{models: map[string]*types.Model{
		"vlm-1":  {ID: "vlm-1", Type: types.ModelTypeVLLM},
		"chat-1": {ID: "chat-1", Type: types.ModelTypeKnowledgeQA},
	}}}
}

// TestUpdateKnowledgeBaseAppliesVLMConfig guards issue #1723: the KB update
// API previously had no way to change a knowledge base's multimodal (VLM)
// config, because neither the HTTP request nor the service method carried it.
func TestUpdateKnowledgeBaseAppliesVLMConfig(t *testing.T) {
	repo := newFakeKBRepo()
	repo.rows["kb-1"] = &types.KnowledgeBase{ID: "kb-1", Name: "old", TenantID: 1}
	svc := newVLMUpdateService(repo)
	ctx := context.Background()

	// nil vlmConfig must preserve whatever is already stored.
	kb, err := svc.UpdateKnowledgeBase(ctx, "kb-1", "n", "d", nil, nil)
	require.NoError(t, err)
	assert.False(t, kb.VLMConfig.Enabled, "absent vlm_config must not enable multimodal")

	// Providing vlm_config must persist it — this was silently dropped before #1723.
	kb, err = svc.UpdateKnowledgeBase(ctx, "kb-1", "n", "d", nil,
		&types.VLMConfig{Enabled: true, ModelID: "vlm-1"})
	require.NoError(t, err)
	assert.True(t, kb.VLMConfig.Enabled)
	assert.Equal(t, "vlm-1", kb.VLMConfig.ModelID)

	// Disabling clears the model, like the initialization config endpoint does.
	kb, err = svc.UpdateKnowledgeBase(ctx, "kb-1", "n", "d", nil,
		&types.VLMConfig{Enabled: false, ModelID: "vlm-1"})
	require.NoError(t, err)
	assert.False(t, kb.VLMConfig.Enabled)
	assert.Empty(t, kb.VLMConfig.ModelID)
}

// The legacy fields point the VLM client straight at BaseURL, outside model
// management and its SSRF checks, so the update API must not let a caller set
// them; whatever is stored stays as it was.
func TestUpdateKnowledgeBaseIgnoresLegacyVLMFields(t *testing.T) {
	repo := newFakeKBRepo()
	repo.rows["kb-1"] = &types.KnowledgeBase{
		ID: "kb-1", Name: "old", TenantID: 1,
		VLMConfig: types.VLMConfig{ModelName: "stored", BaseURL: "https://stored.example"},
	}
	svc := newVLMUpdateService(repo)

	kb, err := svc.UpdateKnowledgeBase(context.Background(), "kb-1", "n", "d", nil, &types.VLMConfig{
		Enabled: true, ModelID: "vlm-1",
		ModelName: "evil", BaseURL: "http://169.254.169.254", APIKey: "k", InterfaceType: "openai",
	})
	require.NoError(t, err)
	assert.Equal(t, "vlm-1", kb.VLMConfig.ModelID)
	assert.Equal(t, "stored", kb.VLMConfig.ModelName)
	assert.Equal(t, "https://stored.example", kb.VLMConfig.BaseURL)
	assert.Empty(t, kb.VLMConfig.APIKey)
	assert.Empty(t, kb.VLMConfig.InterfaceType)
}

// A vlm_config that names a missing model or a non-VLM model is rejected as a
// bad request and leaves the stored VLM config alone.
func TestUpdateKnowledgeBaseRejectsInvalidVLMModel(t *testing.T) {
	for _, modelID := range []string{"missing", "chat-1"} {
		t.Run(modelID, func(t *testing.T) {
			repo := newFakeKBRepo()
			repo.rows["kb-1"] = &types.KnowledgeBase{ID: "kb-1", Name: "old", TenantID: 1}
			svc := newVLMUpdateService(repo)

			_, err := svc.UpdateKnowledgeBase(context.Background(), "kb-1", "n", "d", nil,
				&types.VLMConfig{Enabled: true, ModelID: modelID})

			appErr, ok := apperrors.IsAppError(err)
			require.True(t, ok, "error = %v, want an AppError", err)
			assert.Equal(t, http.StatusBadRequest, appErr.HTTPCode)
			assert.False(t, repo.rows["kb-1"].VLMConfig.Enabled)
		})
	}
}

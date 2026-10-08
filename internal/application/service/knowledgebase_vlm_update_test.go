package service

import (
	"context"
	"net/http"
	"strings"
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

func newLegacyVLMKB() *types.KnowledgeBase {
	return &types.KnowledgeBase{
		ID: "kb-1", Name: "old", TenantID: 1,
		VLMConfig: types.VLMConfig{
			ModelName: "stored", BaseURL: "https://stored.example", APIKey: "sk", InterfaceType: "openai",
		},
	}
}

// The legacy fields point the VLM client straight at BaseURL, outside model
// management and its SSRF checks, so the update API never writes them from the
// request. Switching to a managed model clears the stored ones, so a later
// disable cannot fall back to the old endpoint.
func TestUpdateKnowledgeBaseManagedVLMClearsLegacyFields(t *testing.T) {
	repo := newFakeKBRepo()
	repo.rows["kb-1"] = newLegacyVLMKB()
	svc := newVLMUpdateService(repo)
	ctx := context.Background()

	kb, err := svc.UpdateKnowledgeBase(ctx, "kb-1", "n", "d", nil, &types.VLMConfig{
		Enabled: true, ModelID: "vlm-1",
		ModelName: "evil", BaseURL: "http://169.254.169.254", APIKey: "k", InterfaceType: "ollama",
	})
	require.NoError(t, err)
	assert.Equal(t, "vlm-1", kb.VLMConfig.ModelID)
	assert.Empty(t, kb.VLMConfig.ModelName)
	assert.Empty(t, kb.VLMConfig.BaseURL)
	assert.Empty(t, kb.VLMConfig.APIKey)
	assert.Empty(t, kb.VLMConfig.InterfaceType)

	kb, err = svc.UpdateKnowledgeBase(ctx, "kb-1", "n", "d", nil, &types.VLMConfig{Enabled: false})
	require.NoError(t, err)
	assert.False(t, kb.VLMConfig.IsEnabled(), "disable after managed must not fall back to legacy")
}

// An explicit disable on a legacy KB must actually turn VLM off: the stored
// ModelName/BaseURL alone keep IsEnabled() true, so they are cleared too.
func TestUpdateKnowledgeBaseDisablesLegacyVLM(t *testing.T) {
	cases := map[string]types.VLMConfig{
		"plain disable":        {Enabled: false},
		"disable other legacy": {Enabled: false, ModelName: "stored", BaseURL: "http://169.254.169.254"},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			repo := newFakeKBRepo()
			repo.rows["kb-1"] = newLegacyVLMKB()
			svc := newVLMUpdateService(repo)

			kb, err := svc.UpdateKnowledgeBase(context.Background(), "kb-1", "n", "d", nil, &req)
			require.NoError(t, err)
			assert.False(t, kb.VLMConfig.IsEnabled())
			assert.Empty(t, kb.VLMConfig.ModelName)
			assert.Empty(t, kb.VLMConfig.BaseURL)
			assert.Empty(t, kb.VLMConfig.APIKey)
		})
	}
}

// GET returns model_name/base_url verbatim with enabled=false for a legacy KB;
// PUTting that body back is an unrelated save and must keep legacy VLM on.
func TestUpdateKnowledgeBaseLegacyVLMEchoKeepsLegacy(t *testing.T) {
	repo := newFakeKBRepo()
	repo.rows["kb-1"] = newLegacyVLMKB()
	svc := newVLMUpdateService(repo)
	ctx := context.Background()

	kb, err := svc.UpdateKnowledgeBase(ctx, "kb-1", "n", "d", nil, &types.VLMConfig{
		Enabled: false, ModelName: "stored", BaseURL: "https://stored.example",
	})
	require.NoError(t, err)
	assert.True(t, kb.VLMConfig.IsEnabled())
	assert.Equal(t, newLegacyVLMKB().VLMConfig, kb.VLMConfig)

	// enabled=true without a model_id keeps a legacy KB as it is.
	kb, err = svc.UpdateKnowledgeBase(ctx, "kb-1", "n", "d", nil, &types.VLMConfig{Enabled: true})
	require.NoError(t, err)
	assert.True(t, kb.VLMConfig.IsEnabled())
	assert.Equal(t, "https://stored.example", kb.VLMConfig.BaseURL)
}

// Enabling without a model on a non-legacy KB would store Enabled=true while
// IsEnabled() stays false; reject it instead.
func TestUpdateKnowledgeBaseRejectsEnableWithoutModel(t *testing.T) {
	repo := newFakeKBRepo()
	repo.rows["kb-1"] = &types.KnowledgeBase{ID: "kb-1", Name: "old", TenantID: 1}
	svc := newVLMUpdateService(repo)

	_, err := svc.UpdateKnowledgeBase(context.Background(), "kb-1", "n", "d", nil, &types.VLMConfig{Enabled: true})

	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok, "error = %v, want an AppError", err)
	assert.Equal(t, http.StatusBadRequest, appErr.HTTPCode)
	assert.False(t, repo.rows["kb-1"].VLMConfig.Enabled)
}

// custom_instructions has the same 4000-character cap as create and
// /initialization/config.
func TestUpdateKnowledgeBaseRejectsOverlongVLMInstructions(t *testing.T) {
	repo := newFakeKBRepo()
	repo.rows["kb-1"] = &types.KnowledgeBase{
		ID: "kb-1", Name: "old", TenantID: 1,
		VLMConfig: types.VLMConfig{Enabled: true, ModelID: "vlm-1", CustomInstructions: "keep"},
	}
	svc := newVLMUpdateService(repo)
	ctx := context.Background()

	_, err := svc.UpdateKnowledgeBase(ctx, "kb-1", "n", "d", nil, &types.VLMConfig{
		Enabled: true, ModelID: "vlm-1",
		CustomInstructions: strings.Repeat("字", types.MaxCustomPromptInstructionsLength+1),
	})
	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok, "error = %v, want an AppError", err)
	assert.Equal(t, http.StatusBadRequest, appErr.HTTPCode)
	assert.Equal(t, "keep", repo.rows["kb-1"].VLMConfig.CustomInstructions)

	kb, err := svc.UpdateKnowledgeBase(ctx, "kb-1", "n", "d", nil, &types.VLMConfig{
		Enabled: true, ModelID: "vlm-1",
		CustomInstructions: strings.Repeat("字", types.MaxCustomPromptInstructionsLength),
	})
	require.NoError(t, err)
	assert.Len(t, []rune(kb.VLMConfig.CustomInstructions), types.MaxCustomPromptInstructionsLength)
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

// CreateKnowledgeBase stores vlm_config as sent, so a managed model_id can sit
// next to legacy model_name/base_url. resolveVLM uses the managed model then;
// an update must not drop model_id and leave the legacy endpoint in charge.
func newMixedVLMKB() *types.KnowledgeBase {
	kb := newLegacyVLMKB()
	kb.VLMConfig.Enabled = true
	kb.VLMConfig.ModelID = "vlm-1"
	return kb
}

func TestUpdateKnowledgeBaseMixedVLMDisableClearsLegacy(t *testing.T) {
	cases := map[string]types.VLMConfig{
		"plain disable": {Enabled: false},
		"disable echoing stored legacy": {
			Enabled: false, ModelID: "vlm-1", ModelName: "stored", BaseURL: "https://stored.example",
		},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			repo := newFakeKBRepo()
			repo.rows["kb-1"] = newMixedVLMKB()
			svc := newVLMUpdateService(repo)

			kb, err := svc.UpdateKnowledgeBase(context.Background(), "kb-1", "n", "d", nil, &req)
			require.NoError(t, err)
			assert.False(t, kb.VLMConfig.IsEnabled(), "disable must not fall back to the stored base_url")
			assert.Empty(t, kb.VLMConfig.ModelID)
			assert.Empty(t, kb.VLMConfig.ModelName)
			assert.Empty(t, kb.VLMConfig.BaseURL)
			assert.Empty(t, kb.VLMConfig.APIKey)
		})
	}
}

// enabled=true without model_id is only a no-op for a pure legacy KB. On a
// mixed config it would drop the managed model and run on the legacy
// endpoint, so it is rejected like on any other managed KB.
func TestUpdateKnowledgeBaseMixedVLMEnableWithoutModelRejected(t *testing.T) {
	repo := newFakeKBRepo()
	repo.rows["kb-1"] = newMixedVLMKB()
	svc := newVLMUpdateService(repo)

	_, err := svc.UpdateKnowledgeBase(context.Background(), "kb-1", "n", "d", nil, &types.VLMConfig{
		Enabled: true, ModelName: "stored", BaseURL: "https://stored.example",
	})

	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok, "error = %v, want an AppError", err)
	assert.Equal(t, http.StatusBadRequest, appErr.HTTPCode)
	assert.Equal(t, newMixedVLMKB().VLMConfig, repo.rows["kb-1"].VLMConfig)
}

// Echoing the managed model_id keeps it and drops the legacy fields.
func TestUpdateKnowledgeBaseMixedVLMKeepsManagedModel(t *testing.T) {
	repo := newFakeKBRepo()
	repo.rows["kb-1"] = newMixedVLMKB()
	svc := newVLMUpdateService(repo)

	kb, err := svc.UpdateKnowledgeBase(context.Background(), "kb-1", "n", "d", nil, &types.VLMConfig{
		Enabled: true, ModelID: "vlm-1", ModelName: "stored", BaseURL: "https://stored.example",
	})
	require.NoError(t, err)
	assert.Equal(t, "vlm-1", kb.VLMConfig.ModelID)
	assert.True(t, kb.VLMConfig.IsEnabled())
	assert.Empty(t, kb.VLMConfig.ModelName)
	assert.Empty(t, kb.VLMConfig.BaseURL)
}

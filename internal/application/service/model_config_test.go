package service

// model_config_test.go — P1c wave 1: the single shared constructor
// BuildModelConfig (design §6.1/§6.8): credential slots with the WeKnoraCloud
// tenant fallback, plus the legacy local-record provider/base-url mapping.

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wkcTenantStub overrides the shared delete-test tenant stub so the tenant
// fallback has credentials to resolve.
type wkcTenantStub struct {
	stubTenantServiceForModelDelete
	creds *types.WeKnoraCloudCredentials
}

func (s *wkcTenantStub) GetWeKnoraCloudCredentials(context.Context) *types.WeKnoraCloudCredentials {
	return s.creds
}

func newBuildConfigService(creds *types.WeKnoraCloudCredentials) *modelService {
	return &modelService{tenantService: &wkcTenantStub{creds: creds}}
}

func TestBuildModelConfigCredentialSlots(t *testing.T) {
	svc := newBuildConfigService(nil)
	model := &types.Model{
		ID:   "m-1",
		Name: "gpt-4o",
		Parameters: types.ModelParameters{
			BaseURL:        "https://api.openai.com/v1",
			APIKey:         "sk-live",
			Provider:       "openai",
			MaxConcurrency: 3,
		},
	}
	cfg, err := svc.BuildModelConfig(context.Background(), model)
	require.NoError(t, err)
	assert.Equal(t, "openai", cfg.Provider)
	assert.Equal(t, "gpt-4o", cfg.ModelName)
	assert.Equal(t, "m-1", cfg.ModelID)
	assert.Equal(t, "sk-live", cfg.Credentials.APIKey)
	assert.Zero(t, cfg.Credentials.AppID)
	assert.Zero(t, cfg.Credentials.AppSecret)
	assert.Equal(t, 3, cfg.MaxConcurrency)
}

func TestBuildModelConfigWeKnoraCloudTenantFallback(t *testing.T) {
	// Model credentials present → untouched (also fills only the missing
	// slot when one is empty).
	svc := newBuildConfigService(&types.WeKnoraCloudCredentials{AppID: "tenant-app", AppSecret: "tenant-secret"})
	model := &types.Model{
		ID:         "m-2",
		Name:       "wkc-chat",
		Parameters: types.ModelParameters{Provider: "weknoracloud", AppID: "model-app", AppSecret: "model-secret"},
	}
	cfg, err := svc.BuildModelConfig(context.Background(), model)
	require.NoError(t, err)
	assert.Equal(t, "model-app", cfg.Credentials.AppID)
	assert.Equal(t, "model-secret", cfg.Credentials.AppSecret)

	// Model credentials empty → tenant fallback fills BOTH slots (the
	// space-level-credential life line — missing this = all-wkc-auth-fails).
	model.Parameters = types.ModelParameters{Provider: "weknoracloud"}
	cfg, err = svc.BuildModelConfig(context.Background(), model)
	require.NoError(t, err)
	assert.Equal(t, "tenant-app", cfg.Credentials.AppID)
	assert.Equal(t, "tenant-secret", cfg.Credentials.AppSecret)

	// Fallback fills only the empty slot.
	model.Parameters = types.ModelParameters{Provider: "weknoracloud", AppID: "model-app"}
	cfg, err = svc.BuildModelConfig(context.Background(), model)
	require.NoError(t, err)
	assert.Equal(t, "model-app", cfg.Credentials.AppID)
	assert.Equal(t, "tenant-secret", cfg.Credentials.AppSecret)
}

func TestBuildModelConfigLegacyLocalRecordMapping(t *testing.T) {
	svc := newBuildConfigService(nil)

	// source=local, no base_url → Provider="ollama" + OLLAMA_BASE_URL inject.
	t.Setenv("OLLAMA_BASE_URL", "http://ollama.internal:11434")
	cfg, err := svc.BuildModelConfig(context.Background(), &types.Model{
		ID:         "m-3",
		Name:       "llama3",
		Source:     types.ModelSourceLocal,
		Parameters: types.ModelParameters{},
	})
	require.NoError(t, err)
	assert.Equal(t, "ollama", cfg.Provider)
	assert.Equal(t, "http://ollama.internal:11434", cfg.BaseURL)

	// interface_type=ollama (remote source), existing /v1-suffixed base_url →
	// value kept, suffix stripped (native adapter must not build /v1/api/chat).
	cfg, err = svc.BuildModelConfig(context.Background(), &types.Model{
		ID:     "m-4",
		Name:   "llama3",
		Source: types.ModelSourceRemote,
		Parameters: types.ModelParameters{
			BaseURL:       "http://10.0.0.5:11434/v1",
			InterfaceType: "ollama",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "ollama", cfg.Provider)
	assert.Equal(t, "http://10.0.0.5:11434", cfg.BaseURL)

	// Remote non-ollama records pass through untouched.
	cfg, err = svc.BuildModelConfig(context.Background(), &types.Model{
		ID:         "m-5",
		Name:       "gpt-4o",
		Source:     types.ModelSourceRemote,
		Parameters: types.ModelParameters{Provider: "openai", BaseURL: "https://api.openai.com/v1"},
	})
	require.NoError(t, err)
	assert.Equal(t, "openai", cfg.Provider)
	assert.Equal(t, "https://api.openai.com/v1", cfg.BaseURL)
}

func TestBuildModelConfigRemoteModelNameOverride(t *testing.T) {
	// extra_config.remote_model_name overrides the wire model name (v1
	// NewRemoteAPIChat semantics — restored by the audit round).
	svc := newBuildConfigService(nil)
	cfg, err := svc.BuildModelConfig(context.Background(), &types.Model{
		ID:   "m-7",
		Name: "display-name",
		Parameters: types.ModelParameters{
			Provider:    "weknoracloud",
			ExtraConfig: map[string]string{"remote_model_name": "glm-5-air"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "glm-5-air", cfg.ModelName)

	// Whitespace-only override falls back to the record name; a real name is
	// trimmed.
	cfg, err = svc.BuildModelConfig(context.Background(), &types.Model{
		ID:   "m-8",
		Name: "display-name",
		Parameters: types.ModelParameters{
			Provider:    "openai",
			ExtraConfig: map[string]string{"remote_model_name": "  "},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "display-name", cfg.ModelName)

	cfg, err = svc.BuildModelConfig(context.Background(), &types.Model{
		ID:   "m-9",
		Name: "display-name",
		Parameters: types.ModelParameters{
			Provider:    "openai",
			ExtraConfig: map[string]string{"remote_model_name": " gpt-5 "},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "gpt-5", cfg.ModelName)
}

func TestBuildModelConfigChatShardAndCeilings(t *testing.T) {
	svc := newBuildConfigService(nil)
	cfg, err := svc.BuildModelConfig(context.Background(), &types.Model{
		ID:   "m-6",
		Name: "claude",
		Parameters: types.ModelParameters{
			Chat: &types.ChatParameters{
				ContextWindow:   200000,
				MaxOutputTokens: 8192,
				ThinkingLevel:   "medium",
				SelectedLevels:  []string{"low", "medium"},
			},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, 200000, cfg.ContextWindow)
	assert.Equal(t, 8192, cfg.MaxOutputTokens)
	assert.Equal(t, "medium", cfg.ThinkingLevel)
	assert.Equal(t, []string{"low", "medium"}, cfg.SelectedLevels)

	// Nil model rejected explicitly.
	_, err = svc.BuildModelConfig(context.Background(), nil)
	assert.Error(t, err)
}

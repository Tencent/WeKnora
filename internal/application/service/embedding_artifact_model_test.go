package service

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/require"
)

func TestEmbeddingModelRevisionTracksEffectiveConfigWithoutSecrets(t *testing.T) {
	base := &types.Model{
		ID: "embedding-1", Name: "text-embedding", Source: types.ModelSourceRemote,
		UpdatedAt: time.Date(2026, 7, 16, 10, 30, 0, 0, time.UTC),
		Parameters: types.ModelParameters{
			BaseURL:       "https://user:password@example.com/v1?token=secret#private",
			APIKey:        "first-secret",
			Provider:      "openai",
			CustomHeaders: map[string]string{"Authorization": "secret"},
			EmbeddingParameters: types.EmbeddingParameters{
				Dimension: 1536, TruncatePromptTokens: 512, SupportsDimensionOverride: true,
			},
		},
	}
	baseRevision, err := embeddingModelRevision(base)
	require.NoError(t, err)

	credentialsChanged := *base
	credentialsChanged.Parameters = base.Parameters
	credentialsChanged.Parameters.APIKey = "second-secret"
	credentialsChanged.Parameters.AppSecret = "other-secret"
	credentialsChanged.Parameters.CustomHeaders = map[string]string{"Authorization": "other"}
	credentialsChanged.Parameters.ExtraConfig = map[string]string{"api_secret": "not-an-identity"}
	credentialsChanged.Parameters.BaseURL = "https://other:credentials@example.com/v1?key=other#fragment"
	credentialsRevision, err := embeddingModelRevision(&credentialsChanged)
	require.NoError(t, err)
	require.Equal(t, baseRevision, credentialsRevision)

	for _, tt := range []struct {
		name   string
		mutate func(*types.Model)
	}{
		{name: "source", mutate: func(m *types.Model) { m.Source = types.ModelSourceLocal }},
		{name: "provider", mutate: func(m *types.Model) { m.Parameters.Provider = "nvidia" }},
		{name: "endpoint", mutate: func(m *types.Model) { m.Parameters.BaseURL = "https://example.com/v2" }},
		{name: "dimensions", mutate: func(m *types.Model) { m.Parameters.EmbeddingParameters.Dimension = 768 }},
		{name: "truncate", mutate: func(m *types.Model) { m.Parameters.EmbeddingParameters.TruncatePromptTokens = 256 }},
		{name: "dimension policy", mutate: func(m *types.Model) {
			m.Parameters.EmbeddingParameters.SupportsDimensionOverride = false
		}},
		{name: "API version", mutate: func(m *types.Model) {
			m.Parameters.ExtraConfig = map[string]string{"api_version": "2027-01-01"}
		}},
		{name: "remote model name", mutate: func(m *types.Model) {
			m.Parameters.ExtraConfig = map[string]string{"remote_model_name": "other-model"}
		}},
		{name: "updated at", mutate: func(m *types.Model) { m.UpdatedAt = m.UpdatedAt.Add(time.Second) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			changed := *base
			changed.Parameters = base.Parameters
			tt.mutate(&changed)
			revision, err := embeddingModelRevision(&changed)
			require.NoError(t, err)
			require.NotEqual(t, baseRevision, revision)
		})
	}
}

func TestEmbeddingModelRevisionPreservesRuntimeAPIVersionWhitespace(t *testing.T) {
	model := &types.Model{
		ID: "embedding-1", Name: "text-embedding", Source: types.ModelSourceRemote,
		UpdatedAt:  time.Date(2026, 7, 16, 10, 30, 0, 0, time.UTC),
		Parameters: types.ModelParameters{ExtraConfig: map[string]string{"api_version": "2024-10-21"}},
	}
	plain, err := embeddingModelRevision(model)
	require.NoError(t, err)
	model.Parameters.ExtraConfig["api_version"] = " 2024-10-21 "
	spaced, err := embeddingModelRevision(model)
	require.NoError(t, err)
	require.NotEqual(t, plain, spaced)
}

type embeddingArtifactModelRepository struct {
	interfaces.ModelRepository
	model     *types.Model
	tenantIDs []uint64
}

func (r *embeddingArtifactModelRepository) GetByID(
	_ context.Context,
	tenantID uint64,
	_ string,
) (*types.Model, error) {
	r.tenantIDs = append(r.tenantIDs, tenantID)
	return r.model, nil
}

func TestModelServiceWrapsEmbeddingArtifactsWithEffectiveTenantAndRevision(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)

	updatedAt := time.Date(2026, 7, 16, 10, 30, 0, 0, time.UTC)
	repo := &embeddingArtifactModelRepository{model: &types.Model{
		ID:       "embedding-1",
		TenantID: types.DefaultBuiltinModelTenantID,
		Name:     "text-embedding",
		Type:     types.ModelTypeEmbedding,
		Source:   types.ModelSourceRemote,
		Status:   types.ModelStatusActive,
		Parameters: types.ModelParameters{
			BaseURL:             "http://127.0.0.1/v1",
			Provider:            "openai",
			EmbeddingParameters: types.EmbeddingParameters{Dimension: 2},
		},
		UpdatedAt: updatedAt,
	}}
	store := newEmbeddingArtifactFakeStore()
	svc := NewModelService(repo, nil, nil, nil, nil, nil, store)

	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	local, err := svc.GetEmbeddingModel(ctx, repo.model.ID)
	require.NoError(t, err)
	localCache, ok := local.(*embeddingArtifactEmbedder)
	require.True(t, ok)
	require.Equal(t, uint64(7), localCache.tenantID)
	expectedRevision, err := embeddingModelRevision(repo.model)
	require.NoError(t, err)
	require.Equal(t, expectedRevision, localCache.modelRevision)

	shared, err := svc.GetEmbeddingModelForTenant(ctx, repo.model.ID, 9)
	require.NoError(t, err)
	sharedCache, ok := shared.(*embeddingArtifactEmbedder)
	require.True(t, ok)
	require.Equal(t, uint64(9), sharedCache.tenantID)
	require.Equal(t, []uint64{7, 9}, repo.tenantIDs)
}

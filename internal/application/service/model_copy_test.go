package service

import (
	"context"
	"strings"
	"testing"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubCopyModelRepo struct {
	source  *types.Model
	created *types.Model
	updates int
}

func (s *stubCopyModelRepo) Create(_ context.Context, model *types.Model) error {
	copied := *model
	s.created = &copied
	return nil
}

func (s *stubCopyModelRepo) GetByID(_ context.Context, _ uint64, id string) (*types.Model, error) {
	if s.source != nil && s.source.ID == id {
		return s.source, nil
	}
	return nil, nil
}

func (s *stubCopyModelRepo) List(context.Context, uint64, types.ModelType, types.ModelSource) ([]*types.Model, error) {
	return nil, nil
}

func (s *stubCopyModelRepo) Update(context.Context, *types.Model) error {
	s.updates++
	return nil
}

func (s *stubCopyModelRepo) Delete(context.Context, uint64, string) error { return nil }

func (s *stubCopyModelRepo) ClearDefaultByType(context.Context, uint, types.ModelType, string) error {
	return nil
}

func TestCopyModel_CopiesCredentialsAndKeepsName(t *testing.T) {
	source := &types.Model{
		ID:          "src",
		TenantID:    7,
		Name:        "gpt-4o",
		DisplayName: "生产 GPT",
		Type:        types.ModelTypeKnowledgeQA,
		Source:      types.ModelSourceRemote,
		Description: "primary",
		IsDefault:   true,
		Status:      types.ModelStatusActive,
		Parameters: types.ModelParameters{
			BaseURL:   "https://api.example.com/v1",
			APIKey:    "sk-live",
			AppSecret: "app-secret",
			Provider:  "openai",
			ExtraConfig: map[string]string{
				"region":     "us",
				"secret_key": "stored-secret",
			},
			Spec: &types.ModelSpecOverride{
				API:    "openai-completions",
				Compat: map[string]any{"temperature": "0.2"},
			},
		},
	}
	repo := &stubCopyModelRepo{source: source}
	svc := NewModelService(repo, &stubKBRepoForModelDelete{}, &stubAgentRepoForModelDelete{}, nil, nil, nil)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

	copied, err := svc.CopyModel(ctx, "src", "  生产 GPT 副本\n")
	require.NoError(t, err)
	require.NotNil(t, repo.created)
	assert.Equal(t, copied, repo.created)
	assert.Empty(t, copied.ID)
	assert.Equal(t, uint64(7), copied.TenantID)
	assert.Equal(t, "gpt-4o", copied.Name)
	assert.Equal(t, "生产 GPT 副本", copied.DisplayName)
	assert.Equal(t, "primary", copied.Description)
	assert.Equal(t, types.ModelStatusActive, copied.Status)
	assert.False(t, copied.IsDefault)
	assert.False(t, copied.IsBuiltin)
	assert.Equal(t, "sk-live", copied.Parameters.APIKey)
	assert.Equal(t, "app-secret", copied.Parameters.AppSecret)
	assert.Equal(t, "stored-secret", copied.Parameters.ExtraConfig["secret_key"])
	assert.Equal(t, "https://api.example.com/v1", copied.Parameters.BaseURL)
	assert.Zero(t, repo.updates)

	copied.Parameters.ExtraConfig["region"] = "changed"
	copied.Parameters.APIKey = "changed"
	copied.Parameters.Spec.Compat["extra"] = "1"
	assert.Equal(t, "us", source.Parameters.ExtraConfig["region"])
	assert.Equal(t, "sk-live", source.Parameters.APIKey)
	_, carried := source.Parameters.Spec.Compat["extra"]
	assert.False(t, carried)
}

func TestCopyModel_ActiveLocalModelSkipsDownload(t *testing.T) {
	repo := &stubCopyModelRepo{source: &types.Model{
		ID:     "local-src",
		Name:   "qwen3:8b",
		Type:   types.ModelTypeKnowledgeQA,
		Source: types.ModelSourceLocal,
		Status: types.ModelStatusActive,
	}}
	svc := NewModelService(repo, &stubKBRepoForModelDelete{}, &stubAgentRepoForModelDelete{}, nil, nil, nil)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

	copied, err := svc.CopyModel(ctx, "local-src", "qwen3:8b 副本")
	require.NoError(t, err)
	assert.Equal(t, "qwen3:8b", copied.Name)
	assert.Equal(t, types.ModelStatusActive, copied.Status)
	assert.Equal(t, types.ModelSourceLocal, copied.Source)
	assert.Zero(t, repo.updates)
}

func TestCopyModel_RejectsBuiltinMissingAndOverlongDisplayName(t *testing.T) {
	repo := &stubCopyModelRepo{source: &types.Model{
		ID:        "builtin",
		Name:      "gpt-4o",
		IsBuiltin: true,
		Source:    types.ModelSourceRemote,
		Status:    types.ModelStatusActive,
	}}
	svc := NewModelService(repo, &stubKBRepoForModelDelete{}, &stubAgentRepoForModelDelete{}, nil, nil, nil)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

	_, err := svc.CopyModel(ctx, "builtin", "副本")
	require.Error(t, err)
	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok)
	assert.Equal(t, apperrors.ErrBadRequest, appErr.Code)
	assert.Nil(t, repo.created)

	_, err = svc.CopyModel(ctx, "missing", "副本")
	assert.ErrorIs(t, err, ErrModelNotFound)

	_, err = svc.CopyModel(ctx, "builtin", "   ")
	appErr, ok = apperrors.IsAppError(err)
	require.True(t, ok)
	assert.Equal(t, apperrors.ErrBadRequest, appErr.Code)

	_, err = svc.CopyModel(ctx, "builtin", strings.Repeat("名", types.ModelDisplayNameMaxLen+1))
	appErr, ok = apperrors.IsAppError(err)
	require.True(t, ok)
	assert.Equal(t, apperrors.ErrBadRequest, appErr.Code)
	assert.Contains(t, appErr.Message, "too long")
}

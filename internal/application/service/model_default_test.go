package service

import (
	"context"
	"errors"
	"testing"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type defaultModelRepo struct {
	interfaces.ModelRepository
	model          *types.Model
	err            error
	saved          []types.ModelDefault
	lookedUpTenant uint64
}

func (r *defaultModelRepo) GetByID(_ context.Context, tenantID uint64, _ string) (*types.Model, error) {
	r.lookedUpTenant = tenantID
	return r.model, nil
}

func (r *defaultModelRepo) SetDefault(_ context.Context, tenantID uint64, modelType types.ModelType, id string) error {
	r.saved = append(r.saved, types.ModelDefault{TenantID: tenantID, Type: modelType, ModelID: id})
	return r.err
}

func TestSetDefaultModelAllowsBuiltinWithoutChangingSharedRow(t *testing.T) {
	stored := &types.Model{
		ID: "builtin", TenantID: 10000, IsBuiltin: true, ManagedBy: types.BuiltinModelManagedBy,
		Type: types.ModelTypeKnowledgeQA, Status: types.ModelStatusActive,
	}
	repo := &defaultModelRepo{model: stored}
	svc := NewModelService(repo, nil, nil, nil, nil, nil)
	model, err := svc.SetDefaultModel(builtinModelContext(false), stored.ID)
	require.NoError(t, err)
	require.True(t, model.IsDefault)
	require.False(t, stored.IsDefault)
	require.Equal(t, types.BuiltinModelManagedBy, stored.ManagedBy)
	require.Equal(t, uint64(7), repo.lookedUpTenant)
	require.Equal(t, []types.ModelDefault{{TenantID: 7, Type: stored.Type, ModelID: stored.ID}}, repo.saved)
}

func TestSetDefaultModelRejectsUnavailableModelsAndReportsWriteFailure(t *testing.T) {
	for _, status := range []types.ModelStatus{"", types.ModelStatusDownloading, types.ModelStatusDownloadFailed} {
		t.Run(string(status), func(t *testing.T) {
			repo := &defaultModelRepo{model: &types.Model{ID: "model", Status: status}}
			svc := NewModelService(repo, nil, nil, nil, nil, nil)
			_, err := svc.SetDefaultModel(builtinModelContext(false), "model")
			appErr, ok := apperrors.IsAppError(err)
			require.True(t, ok)
			require.Equal(t, apperrors.ErrBadRequest, appErr.Code)
			require.Empty(t, repo.saved)
		})
	}
	repo := &defaultModelRepo{}
	svc := NewModelService(repo, nil, nil, nil, nil, nil)
	_, err := svc.SetDefaultModel(builtinModelContext(false), "foreign-or-missing")
	require.ErrorIs(t, err, ErrModelNotFound)
	require.Empty(t, repo.saved)
	repo.model = &types.Model{ID: "active", Status: types.ModelStatusActive}
	repo.err = errors.New("write failed")
	model, err := svc.SetDefaultModel(builtinModelContext(false), "active")
	require.ErrorIs(t, err, repo.err)
	require.Nil(t, model)
}

package service

import (
	"context"
	"testing"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func builtinModelContext(systemAdmin bool) context.Context {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	return context.WithValue(ctx, types.SystemAdminContextKey, systemAdmin)
}

func TestUpdateBuiltinModel_RequiresSystemAdmin(t *testing.T) {
	stored := &types.Model{
		ID: "builtin-chat", TenantID: 10000, IsBuiltin: true,
		ManagedBy: types.BuiltinModelManagedBy,
	}
	updated := false
	svc := NewModelService(&stubModelRepoForDelete{
		model: stored,
		update: func(*types.Model) error {
			updated = true
			return nil
		},
	}, nil, nil, nil, nil, nil)

	err := svc.UpdateModel(builtinModelContext(false), &types.Model{ID: stored.ID})
	require.Error(t, err)
	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok)
	assert.Equal(t, apperrors.ErrForbidden, appErr.Code)
	assert.False(t, updated)
}

func TestUpdateBuiltinModel_SystemAdminCreatesRuntimeOverride(t *testing.T) {
	stored := &types.Model{
		ID: "builtin-chat", TenantID: 10000, IsBuiltin: true,
		ManagedBy: types.BuiltinModelManagedBy,
	}
	var saved *types.Model
	svc := NewModelService(&stubModelRepoForDelete{
		model: stored,
		update: func(model *types.Model) error {
			copy := *model
			saved = &copy
			return nil
		},
	}, nil, nil, nil, nil, nil)

	input := &types.Model{ID: stored.ID, Name: "edited"}
	require.NoError(t, svc.UpdateModel(builtinModelContext(true), input))
	require.NotNil(t, saved)
	assert.Equal(t, uint64(10000), saved.TenantID)
	assert.True(t, saved.IsBuiltin)
	assert.Empty(t, saved.ManagedBy, "UI edit must stop later YAML reconciliation")
}

func TestUpdateBuiltinModelCredentials_SystemAdminOnly(t *testing.T) {
	newKey := "sk-new"

	t.Run("tenant admin denied", func(t *testing.T) {
		stored := &types.Model{ID: "builtin-chat", TenantID: 10000, IsBuiltin: true}
		svc := NewModelService(&stubModelRepoForDelete{model: stored}, nil, nil, nil, nil, nil)
		_, err := svc.UpdateModelCredentials(builtinModelContext(false), stored.ID, &newKey, nil)
		require.Error(t, err)
		appErr, ok := apperrors.IsAppError(err)
		require.True(t, ok)
		assert.Equal(t, apperrors.ErrForbidden, appErr.Code)
	})

	t.Run("system admin saves runtime override", func(t *testing.T) {
		stored := &types.Model{
			ID: "builtin-chat", TenantID: 10000, IsBuiltin: true,
			ManagedBy: types.BuiltinModelManagedBy,
		}
		var saved *types.Model
		svc := NewModelService(&stubModelRepoForDelete{
			model: stored,
			update: func(model *types.Model) error {
				copy := *model
				saved = &copy
				return nil
			},
		}, nil, nil, nil, nil, nil)

		updated, err := svc.UpdateModelCredentials(
			builtinModelContext(true), stored.ID, &newKey, nil,
		)
		require.NoError(t, err)
		assert.Equal(t, newKey, updated.Parameters.APIKey)
		require.NotNil(t, saved)
		assert.Empty(t, saved.ManagedBy)
	})
}

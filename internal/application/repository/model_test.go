package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupModelRepositoryTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.Model{}))
	return db
}

func newModel(id string, tenantID uint64, isDefault, isBuiltin bool) *types.Model {
	return &types.Model{
		ID:        id,
		TenantID:  tenantID,
		Name:      id,
		Type:      types.ModelTypeKnowledgeQA,
		Source:    types.ModelSourceRemote,
		Status:    types.ModelStatusActive,
		IsDefault: isDefault,
		IsBuiltin: isBuiltin,
	}
}

func TestModelRepositoryListPrefersWorkspaceDefault(t *testing.T) {
	db := setupModelRepositoryTestDB(t)
	repo := NewModelRepository(db)
	ctx := context.Background()
	builtin := newModel("builtin", types.DefaultBuiltinModelTenantID, true, true)
	workspace := newModel("workspace", 7, true, false)
	other := newModel("other", 7, false, false)
	for _, model := range []*types.Model{builtin, other, workspace} {
		require.NoError(t, repo.Create(ctx, model))
	}

	models, err := repo.List(ctx, 7, types.ModelTypeKnowledgeQA, "")
	require.NoError(t, err)
	require.Len(t, models, 3)
	assert.Equal(t, workspace.ID, models[0].ID)
	assert.True(t, models[0].IsDefault)

	var storedBuiltin types.Model
	require.NoError(t, db.Where("id = ?", builtin.ID).First(&storedBuiltin).Error)
	assert.True(t, storedBuiltin.IsDefault)

	models, err = repo.List(ctx, 8, types.ModelTypeKnowledgeQA, "")
	require.NoError(t, err)
	require.Len(t, models, 1)
	assert.Equal(t, builtin.ID, models[0].ID)
	assert.True(t, models[0].IsDefault)
}

func TestModelRepositoryDefaultScopesDoNotOverlap(t *testing.T) {
	db := setupModelRepositoryTestDB(t)
	repo := NewModelRepository(db)
	ctx := context.Background()
	tenantID := types.DefaultBuiltinModelTenantID
	builtinOld := newModel("builtin-old", tenantID, true, true)
	workspaceOld := newModel("workspace-old", tenantID, true, false)
	workspaceNew := newModel("workspace-new", tenantID, false, false)
	for _, model := range []*types.Model{builtinOld, workspaceOld, workspaceNew} {
		require.NoError(t, repo.Create(ctx, model))
	}

	require.NoError(t, repo.ClearDefaultByType(
		ctx, tenantID, types.ModelTypeKnowledgeQA, workspaceNew.ID,
	))
	workspaceNew.IsDefault = true
	require.NoError(t, repo.Update(ctx, workspaceNew))

	var storedBuiltin types.Model
	require.NoError(t, db.Where("id = ?", builtinOld.ID).First(&storedBuiltin).Error)
	assert.True(t, storedBuiltin.IsDefault)

	var storedWorkspaceOld, storedWorkspaceNew types.Model
	require.NoError(t, db.Where("id = ?", workspaceOld.ID).First(&storedWorkspaceOld).Error)
	require.NoError(t, db.Where("id = ?", workspaceNew.ID).First(&storedWorkspaceNew).Error)
	assert.False(t, storedWorkspaceOld.IsDefault)
	assert.True(t, storedWorkspaceNew.IsDefault)
}

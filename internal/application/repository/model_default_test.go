package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func modelDefaultTestDB(t *testing.T) (*gorm.DB, *modelRepository) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&types.Model{}, &types.ModelDefault{}))
	models := []*types.Model{
		{ID: "builtin-chat", TenantID: 10000, Type: types.ModelTypeKnowledgeQA, IsBuiltin: true, IsDefault: true},
		{
			ID: "other-builtin", TenantID: 10000, Type: types.ModelTypeKnowledgeQA,
			IsBuiltin: true, ManagedBy: types.BuiltinModelManagedBy,
		},
		{ID: "local-chat", TenantID: 7, Type: types.ModelTypeKnowledgeQA},
		{ID: "embedding", TenantID: 7, Type: types.ModelTypeEmbedding},
		{ID: "foreign-chat", TenantID: 8, Type: types.ModelTypeKnowledgeQA},
		{
			ID: "downloading", TenantID: 7, Type: types.ModelTypeKnowledgeQA,
			Status: types.ModelStatusDownloading, IsDefault: true,
		},
	}
	for i, model := range models {
		model.Name = model.ID
		model.Source = types.ModelSourceRemote
		if model.Status == "" {
			model.Status = types.ModelStatusActive
		}
		model.CreatedAt = time.Unix(int64(i+1), 0)
		model.Parameters = types.ModelParameters{BaseURL: "https://example.com/v1", APIKey: "stored-key"}
		require.NoError(t, db.Create(model).Error)
	}
	return db, &modelRepository{db: db}
}

func effectiveDefaultIDs(t *testing.T, repo *modelRepository, tenantID uint64) map[types.ModelType]string {
	t.Helper()
	models, err := repo.List(context.Background(), tenantID, "", "")
	require.NoError(t, err)
	defaults := make(map[types.ModelType]string)
	for _, model := range models {
		if model.IsDefault {
			require.Empty(t, defaults[model.Type], "each type has one effective default")
			defaults[model.Type] = model.ID
			require.Equal(t, types.ModelStatusActive, model.Status)
		}
	}
	return defaults
}

func TestModelDefaultWorkspaceAndTypeIsolation(t *testing.T) {
	db, repo := modelDefaultTestDB(t)
	ctx := context.Background()
	initial := effectiveDefaultIDs(t, repo, 7)
	require.Equal(t, "builtin-chat", initial[types.ModelTypeKnowledgeQA])
	require.Equal(t, "embedding", initial[types.ModelTypeEmbedding])

	// A workspace admin can choose a shared model without rewriting it.
	var before types.Model
	require.NoError(t, db.First(&before, "id = ?", "other-builtin").Error)
	require.NoError(t, repo.SetDefault(ctx, 7, types.ModelTypeKnowledgeQA, "other-builtin"))
	require.Equal(t, "other-builtin", effectiveDefaultIDs(t, repo, 7)[types.ModelTypeKnowledgeQA])
	require.Equal(t, "builtin-chat", effectiveDefaultIDs(t, repo, 8)[types.ModelTypeKnowledgeQA])
	require.Equal(t, "embedding", effectiveDefaultIDs(t, repo, 7)[types.ModelTypeEmbedding])
	var after types.Model
	require.NoError(t, db.First(&after, "id = ?", "other-builtin").Error)
	require.Equal(t, before, after, "shared configuration, credentials and YAML ownership must not change")

	// Repeated choices replace one preference instead of accumulating flags.
	require.NoError(t, repo.SetDefault(ctx, 7, types.ModelTypeKnowledgeQA, "local-chat"))
	require.Equal(t, "local-chat", effectiveDefaultIDs(t, repo, 7)[types.ModelTypeKnowledgeQA])
	var count int64
	require.NoError(t, db.Model(&types.ModelDefault{}).
		Where("tenant_id = ? AND model_type = ?", 7, types.ModelTypeKnowledgeQA).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestModelDefaultFallsBackWhenSelectionUnavailable(t *testing.T) {
	db, repo := modelDefaultTestDB(t)
	ctx := context.Background()
	for _, id := range []string{"downloading", "foreign-chat", "missing"} {
		require.NoError(t, repo.SetDefault(ctx, 7, types.ModelTypeKnowledgeQA, id))
		require.Equal(t, "builtin-chat", effectiveDefaultIDs(t, repo, 7)[types.ModelTypeKnowledgeQA])
	}
	require.NoError(t, repo.SetDefault(ctx, 7, types.ModelTypeKnowledgeQA, "local-chat"))
	require.NoError(t, repo.Delete(ctx, 7, "local-chat"))
	require.Equal(t, "builtin-chat", effectiveDefaultIDs(t, repo, 7)[types.ModelTypeKnowledgeQA])

	// No declared default still has a stable active fallback, shown in the UI.
	require.NoError(t, db.Model(&types.Model{}).Where("is_builtin = ?", true).Update("is_default", false).Error)
	require.Equal(t, "builtin-chat", effectiveDefaultIDs(t, repo, 7)[types.ModelTypeKnowledgeQA])
	require.NoError(t, db.Model(&types.Model{}).Where("type = ?", types.ModelTypeKnowledgeQA).
		Update("status", types.ModelStatusDownloadFailed).Error)
	require.Empty(t, effectiveDefaultIDs(t, repo, 7)[types.ModelTypeKnowledgeQA])
}

func TestModelDefaultUsesNewPreferenceInsteadOfLegacyFlags(t *testing.T) {
	db, repo := modelDefaultTestDB(t)
	require.NoError(t, db.Model(&types.Model{}).Where("id = ?", "local-chat").Update("is_default", true).Error)
	require.Equal(t, "builtin-chat", effectiveDefaultIDs(t, repo, 7)[types.ModelTypeKnowledgeQA],
		"without a preference, use the first active model instead of interpreting legacy flags")
	require.NoError(t, repo.SetDefault(context.Background(), 7, types.ModelTypeKnowledgeQA, "other-builtin"))
	require.Equal(t, "other-builtin", effectiveDefaultIDs(t, repo, 7)[types.ModelTypeKnowledgeQA])
	require.Equal(t, "builtin-chat", effectiveDefaultIDs(t, repo, 8)[types.ModelTypeKnowledgeQA])
}

func TestModelDefaultSourceFilterPreservesWorkspaceSelection(t *testing.T) {
	db, repo := modelDefaultTestDB(t)
	require.NoError(t, db.Model(&types.Model{}).Where("id = ?", "local-chat").
		Update("source", types.ModelSourceLocal).Error)
	models, err := repo.List(context.Background(), 7, types.ModelTypeKnowledgeQA, types.ModelSourceLocal)
	require.NoError(t, err)
	require.Len(t, models, 1)
	require.Equal(t, "local-chat", models[0].ID)
	require.False(t, models[0].IsDefault, "the remote builtin remains the effective default")
}

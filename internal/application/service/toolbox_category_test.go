package service

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/application/repository"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// A competing writer may commit after the name lookup reports no match.
type toolboxCategoryNameRaceRepository struct {
	interfaces.ToolboxCategoryRepository
}

func (r toolboxCategoryNameRaceRepository) GetByName(context.Context, uint64, string) (*types.ToolboxCategory, error) {
	return nil, nil
}

func TestToolboxCategoryNameValidationAndWriteConflicts(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.ToolboxCategory{}))
	require.NoError(t, db.Exec("CREATE UNIQUE INDEX uq_toolbox_categories_tenant_name "+
		"ON toolbox_categories (tenant_id, name)").Error)
	repo := repository.NewToolboxCategoryRepository(db)
	svc := NewToolboxCategoryService(toolboxCategoryNameRaceRepository{repo}, nil, nil)
	ctx := context.Background()
	original, err := svc.Create(ctx, 1, "Contracts")
	require.NoError(t, err)
	other, err := svc.Create(ctx, 1, "Customers")
	require.NoError(t, err)
	for _, name := range []string{
		"", "\u200b", "\u200d\u2060", "\u0301", "\x00", "a\x00b", "a\nb", strings.Repeat("中", 65),
	} {
		t.Run("invalid_"+name, func(t *testing.T) {
			_, err := svc.Create(ctx, 1, name)
			var appErr *apperrors.AppError
			require.ErrorAs(t, err, &appErr)
			require.Equal(t, http.StatusBadRequest, appErr.HTTPCode)
			_, err = svc.Update(ctx, 1, original.ID, name)
			require.ErrorAs(t, err, &appErr)
			require.Equal(t, http.StatusBadRequest, appErr.HTTPCode)
		})
	}
	for _, name := range []string{"  合同管理  ", "👩‍💻", "e\u0301", "می\u200cروم", strings.Repeat("😀", 64)} {
		created, err := svc.Create(ctx, 1, name)
		require.NoError(t, err)
		require.Equal(t, strings.TrimSpace(name), created.Name)
	}
	t.Run("create_conflict_after_lookup", func(t *testing.T) {
		_, err := svc.Create(ctx, 1, original.Name)
		var appErr *apperrors.AppError
		require.ErrorAs(t, err, &appErr)
		require.Equal(t, http.StatusConflict, appErr.HTTPCode)
	})
	t.Run("rename_conflict_after_lookup", func(t *testing.T) {
		_, err := svc.Update(ctx, 1, other.ID, original.Name)
		var appErr *apperrors.AppError
		require.ErrorAs(t, err, &appErr)
		require.Equal(t, http.StatusConflict, appErr.HTTPCode)
		saved, err := repo.GetByID(ctx, 1, other.ID)
		require.NoError(t, err)
		require.Equal(t, "Customers", saved.Name)
	})
}

func TestToolboxCategoryServiceRejectsCrossTenantAssignmentsAtomically(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&types.ToolboxCategory{},
		&types.ToolboxCategorySkill{},
		&types.ToolboxCategoryMCPService{},
		&types.TenantSkillCatalogEntity{},
		&types.MCPService{},
	))
	ctx := context.Background()
	categoryRepo := repository.NewToolboxCategoryRepository(db)
	skillRepo := repository.NewTenantSkillRepository(db)
	mcpRepo := repository.NewMCPServiceRepository(db)
	service := NewToolboxCategoryService(categoryRepo, skillRepo, mcpRepo)

	local, err := service.Create(ctx, 1, "Contracts")
	require.NoError(t, err)
	require.Equal(t, "Contracts", local.Name)
	_, err = service.Create(ctx, 1, "  Contracts  ")
	require.ErrorContains(t, err, "already exists")
	foreign, err := service.Create(ctx, 2, "Other workspace")
	require.NoError(t, err)
	require.NoError(t, skillRepo.CreateCatalog(ctx, &types.TenantSkillCatalogEntity{
		ID: "skill-a", TenantID: 1, Name: "Skill A",
	}))

	assigned, err := service.ReplaceSkillCategories(ctx, 1, "skill-a", []string{local.ID})
	require.NoError(t, err)
	require.Equal(t, []string{"Contracts"}, serviceCategoryNames(assigned))
	_, err = service.ReplaceSkillCategories(ctx, 1, "skill-a", []string{foreign.ID})
	require.ErrorContains(t, err, "does not belong to this workspace")

	unchanged, err := service.ListByResourceIDs(ctx, 1, types.ToolboxResourceSkill, []string{"skill-a"})
	require.NoError(t, err)
	require.Equal(t, []string{"Contracts"}, serviceCategoryNames(unchanged["skill-a"]))
}

func serviceCategoryNames(categories []types.ToolboxCategory) []string {
	names := make([]string, 0, len(categories))
	for _, category := range categories {
		names = append(names, category.Name)
	}
	return names
}

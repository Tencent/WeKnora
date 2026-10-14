package repository

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/types"
)

func newToolboxCategoryTestRepository(t *testing.T) *toolboxCategoryRepository {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&types.ToolboxCategory{},
		&types.ToolboxCategorySkill{},
		&types.ToolboxCategoryMCPService{},
	))
	return &toolboxCategoryRepository{db: db}
}

func TestToolboxCategoriesSupportSharedManyToManyClassification(t *testing.T) {
	repo := newToolboxCategoryTestRepository(t)
	ctx := context.Background()
	for _, category := range []*types.ToolboxCategory{
		{ID: "contracts", TenantID: 1, Name: "Contract management"},
		{ID: "customers", TenantID: 1, Name: "Customer relations"},
	} {
		require.NoError(t, repo.Create(ctx, category))
	}

	require.NoError(t, repo.ReplaceResourceCategories(
		ctx, 1, types.ToolboxResourceSkill, "skill-a", []string{"contracts", "customers"},
	))
	require.NoError(t, repo.ReplaceResourceCategories(
		ctx, 1, types.ToolboxResourceMCPService, "mcp-a", []string{"contracts", "customers"},
	))

	skills, err := repo.ListByResourceIDs(ctx, 1, types.ToolboxResourceSkill, []string{"skill-a"})
	require.NoError(t, err)
	require.Equal(t, []string{"Contract management", "Customer relations"}, categoryNames(skills["skill-a"]))
	mcp, err := repo.ListByResourceIDs(ctx, 1, types.ToolboxResourceMCPService, []string{"mcp-a"})
	require.NoError(t, err)
	require.Equal(t, []string{"Contract management", "Customer relations"}, categoryNames(mcp["mcp-a"]))
}

func TestToolboxCategoryReplacementKeepsBuiltinMCPAssignmentsTenantScoped(t *testing.T) {
	repo := newToolboxCategoryTestRepository(t)
	ctx := context.Background()
	require.NoError(t, repo.Create(ctx, &types.ToolboxCategory{
		ID: "tenant-one", TenantID: 1, Name: "Tenant one",
	}))
	require.NoError(t, repo.Create(ctx, &types.ToolboxCategory{
		ID: "tenant-two", TenantID: 2, Name: "Tenant two",
	}))
	require.NoError(t, repo.ReplaceResourceCategories(
		ctx, 1, types.ToolboxResourceMCPService, "builtin", []string{"tenant-one"},
	))
	require.NoError(t, repo.ReplaceResourceCategories(
		ctx, 2, types.ToolboxResourceMCPService, "builtin", []string{"tenant-two"},
	))

	require.NoError(t, repo.ReplaceResourceCategories(
		ctx, 1, types.ToolboxResourceMCPService, "builtin", nil,
	))
	tenantOne, err := repo.ListByResourceIDs(ctx, 1, types.ToolboxResourceMCPService, []string{"builtin"})
	require.NoError(t, err)
	require.Empty(t, tenantOne["builtin"])
	tenantTwo, err := repo.ListByResourceIDs(ctx, 2, types.ToolboxResourceMCPService, []string{"builtin"})
	require.NoError(t, err)
	require.Equal(t, []string{"Tenant two"}, categoryNames(tenantTwo["builtin"]))
}

func TestSoftDeletingToolboxResourcesRemovesTheirCategoryRelations(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&types.TenantSkillCatalogEntity{},
		&types.MCPService{},
		&types.ToolboxCategory{},
		&types.ToolboxCategorySkill{},
		&types.ToolboxCategoryMCPService{},
	))
	ctx := context.Background()
	categories := &toolboxCategoryRepository{db: db}
	skills := &tenantSkillRepository{db: db}
	mcp := &mcpServiceRepository{db: db}
	require.NoError(t, categories.Create(ctx, &types.ToolboxCategory{
		ID: "category", TenantID: 1, Name: "Contracts",
	}))
	require.NoError(t, skills.CreateCatalog(ctx, &types.TenantSkillCatalogEntity{
		ID: "skill", TenantID: 1, Name: "Skill",
	}))
	require.NoError(t, mcp.Create(ctx, &types.MCPService{
		ID: "mcp", TenantID: 1, Name: "MCP", TransportType: types.MCPTransportSSE,
	}))
	require.NoError(t, categories.ReplaceResourceCategories(
		ctx, 1, types.ToolboxResourceSkill, "skill", []string{"category"},
	))
	require.NoError(t, categories.ReplaceResourceCategories(
		ctx, 1, types.ToolboxResourceMCPService, "mcp", []string{"category"},
	))

	require.NoError(t, skills.DeleteCatalog(ctx, 1, "skill"))
	require.NoError(t, mcp.Delete(ctx, 1, "mcp"))
	var skillRelations, mcpRelations int64
	require.NoError(t, db.Model(&types.ToolboxCategorySkill{}).Count(&skillRelations).Error)
	require.NoError(t, db.Model(&types.ToolboxCategoryMCPService{}).Count(&mcpRelations).Error)
	require.Zero(t, skillRelations)
	require.Zero(t, mcpRelations)
}

func categoryNames(categories []types.ToolboxCategory) []string {
	names := make([]string, 0, len(categories))
	for _, category := range categories {
		names = append(names, category.Name)
	}
	return names
}

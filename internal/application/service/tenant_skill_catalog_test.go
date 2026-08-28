package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestListCatalogGroupsInstallsByDefinition(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:catalog-list?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&types.TenantSkillEntity{}, &types.TenantSkillCatalogEntity{},
		&types.TenantSkillSnapshotEntity{}, &types.TenantUserEnvVar{},
	))
	repo := repository.NewTenantSkillRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.CreateCatalog(ctx, &types.TenantSkillCatalogEntity{
		ID: "cat-pdf", TenantID: 7, Name: "pdf", Description: "extract",
	}))
	require.NoError(t, repo.CreateSkill(ctx, &types.TenantSkillEntity{
		ID: "sk-1", TenantID: 7, SandboxConfigID: "cfg-a", CatalogID: "cat-pdf",
		Name: "pdf", Status: types.SkillStatusReady, Enabled: true,
	}))
	require.NoError(t, repo.CreateSkill(ctx, &types.TenantSkillEntity{
		ID: "sk-2", TenantID: 7, SandboxConfigID: "cfg-b", CatalogID: "cat-pdf",
		Name: "pdf", Status: types.SkillStatusInstalling, Enabled: true,
	}))

	svc := NewTenantSkillService(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	list, err := svc.ListCatalog(ctx, 7)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, "pdf", list[0].Name)
	require.Len(t, list[0].Installations, 2)
}

func TestResolveCatalogFindsLegacySkillID(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:catalog-legacy?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&types.TenantSkillEntity{}, &types.TenantSkillCatalogEntity{},
		&types.TenantSkillSnapshotEntity{}, &types.TenantUserEnvVar{},
	))
	repo := repository.NewTenantSkillRepository(db)
	ctx := context.Background()
	require.NoError(t, repo.CreateSkill(ctx, &types.TenantSkillEntity{
		ID: "sk-old", TenantID: 7, SandboxConfigID: "cfg-a",
		Name: "pdf", BundleRef: "local://7/tenant-skills/sk-old.zip",
		Status: types.SkillStatusReady, Enabled: true,
	}))

	svc := NewTenantSkillService(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	cat, err := svc.resolveCatalog(ctx, 7, "sk-old")
	require.NoError(t, err)
	require.NotNil(t, cat)
	require.Equal(t, "pdf", cat.Name)
	require.Equal(t, "local://7/tenant-skills/sk-old.zip", cat.BundleRef)
}

package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
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

func catalogTestRepo(t *testing.T, dsn string) repository.TenantSkillRepository {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&types.TenantSkillEntity{}, &types.TenantSkillCatalogEntity{},
		&types.TenantSkillSnapshotEntity{}, &types.TenantUserEnvVar{},
	))
	return repository.NewTenantSkillRepository(db)
}

func TestListCatalogShowsInstallsWhoseCatalogWasDeleted(t *testing.T) {
	repo := catalogTestRepo(t, "file:catalog-orphan?mode=memory&cache=shared")
	ctx := context.Background()
	require.NoError(t, repo.CreateCatalog(ctx, &types.TenantSkillCatalogEntity{
		ID: "cat-gone", TenantID: 7, Name: "pdf",
	}))
	require.NoError(t, repo.CreateSkill(ctx, &types.TenantSkillEntity{
		ID: "sk-1", TenantID: 7, SandboxConfigID: "cfg-a", CatalogID: "cat-gone",
		Name: "pdf", Status: types.SkillStatusReady, Enabled: true,
	}))
	require.NoError(t, repo.DeleteCatalog(ctx, 7, "cat-gone"))

	svc := NewTenantSkillService(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	list, err := svc.ListCatalog(ctx, 7)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, "pdf", list[0].Name)
	require.Len(t, list[0].Installations, 1)
	require.Equal(t, "sk-1", list[0].Installations[0].SkillID)
}

func TestDeleteCatalogRefusesWhileARemovalIsInFlight(t *testing.T) {
	repo := catalogTestRepo(t, "file:catalog-removing?mode=memory&cache=shared")
	ctx := context.Background()
	require.NoError(t, repo.CreateCatalog(ctx, &types.TenantSkillCatalogEntity{
		ID: "cat-pdf", TenantID: 7, Name: "pdf",
	}))
	require.NoError(t, repo.CreateSkill(ctx, &types.TenantSkillEntity{
		ID: "sk-1", TenantID: 7, SandboxConfigID: "cfg-a", CatalogID: "cat-pdf",
		Name: "pdf", Status: types.SkillStatusRemoving, Enabled: true,
	}))

	svc := NewTenantSkillService(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	err := svc.DeleteCatalog(ctx, 7, "cat-pdf")
	require.Error(t, err)
	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok)
	require.Equal(t, 409, appErr.HTTPCode)
}

func TestInstallSkillStoresTheArchiveOnTheCatalog(t *testing.T) {
	fx := newInstallFixture(t)
	ctx := context.Background()
	archive := zipBundle(t, map[string]string{
		"SKILL.md":           validSkillMD,
		"scripts/extract.py": "print('hi')\n",
	})

	id, err := fx.svc.InstallSkill(ctx, 7, "cfg-1", archive)
	require.NoError(t, err)

	cat, err := fx.skillRepo.GetCatalogByName(ctx, 7, "pdf-tools")
	require.NoError(t, err)
	require.NotNil(t, cat)
	require.NotEmpty(t, cat.BundleRef)
	require.Equal(t, 1, fx.savedBundles)

	skill, err := fx.skillRepo.GetSkill(ctx, 7, "cfg-1", id)
	require.NoError(t, err)
	require.Equal(t, cat.ID, skill.CatalogID)
	require.Empty(t, skill.BundleRef,
		"the install row must not copy the catalog object; uninstall must not be able to delete it")
	files, err := fx.svc.ListSkillFiles(ctx, 7, "cfg-1", id)
	require.NoError(t, err)
	require.NotEmpty(t, files)
}

func TestRemovingLastSandboxInstallKeepsTheCatalogArchive(t *testing.T) {
	fx := newInstallFixture(t)
	ctx := context.Background()
	archive := zipBundle(t, map[string]string{
		"SKILL.md":           validSkillMD,
		"scripts/extract.py": "print('hi')\n",
	})
	id, err := fx.svc.InstallSkill(ctx, 7, "cfg-1", archive)
	require.NoError(t, err)
	skill, err := fx.skillRepo.GetSkill(ctx, 7, "cfg-1", id)
	require.NoError(t, err)
	skill.Status = types.SkillStatusRemoving
	require.NoError(t, fx.skillRepo.UpdateSkill(ctx, skill))

	require.NoError(t, fx.svc.runRemove(ctx, 7, "cfg-1", id))

	cat, err := fx.skillRepo.GetCatalogByName(ctx, 7, "pdf-tools")
	require.NoError(t, err)
	require.NotNil(t, cat)
	files, err := fx.svc.ListCatalogFiles(ctx, 7, cat.ID)
	require.NoError(t, err)
	require.NotEmpty(t, files)
	require.Empty(t, fx.deletedBundles,
		"uninstalling from the last sandbox must not delete the definition zip")
	gone, err := fx.skillRepo.GetSkill(ctx, 7, "cfg-1", id)
	require.NoError(t, err)
	require.Nil(t, gone)

	require.NoError(t, fx.svc.DeleteCatalog(ctx, 7, cat.ID))
	require.Equal(t, []string{cat.BundleRef}, fx.deletedBundles,
		"only deleting the skill from the catalog drops the stored zip")
}

func TestUpsertCatalogDoesNotStampSHAWhenStoreFails(t *testing.T) {
	fx := newInstallFixture(t)
	fx.saveErr = errors.New("disk full")
	ctx := context.Background()
	oldSHA := strings.Repeat("c", 64)
	require.NoError(t, fx.skillRepo.CreateCatalog(ctx, &types.TenantSkillCatalogEntity{
		ID: "cat-1", TenantID: 7, Name: "pdf-tools",
		BundleRef: "file://old.zip", BundleSHA256: oldSHA,
	}))
	archive := zipBundle(t, map[string]string{"SKILL.md": validSkillMD})
	bundle, err := ParseSkillBundle(archive)
	require.NoError(t, err)
	require.NotEqual(t, oldSHA, bundle.SHA256)

	got, err := fx.svc.upsertCatalogFromBundle(ctx, 7, bundle, archive, false)
	require.NoError(t, err)
	require.Equal(t, oldSHA, got.BundleSHA256)
	require.Equal(t, "file://old.zip", got.BundleRef)
}

func TestInstallCatalogToConfigsReportsPartialFailure(t *testing.T) {
	fx := newInstallFixture(t)
	ctx := context.Background()
	archive := zipBundle(t, map[string]string{
		"SKILL.md":           validSkillMD,
		"scripts/extract.py": "print('hi')\n",
	})
	cat, err := fx.svc.RegisterCatalogFromArchive(ctx, 7, archive)
	require.NoError(t, err)
	require.NotNil(t, cat)

	result, err := fx.svc.InstallCatalogToConfigs(ctx, 7, cat.ID, []string{"cfg-1", "missing"})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(t, result.Installs, "cfg-1")
	require.Equal(t, "sandbox config not found", result.Errors["missing"])
	require.Equal(t, 1, fx.savedBundles,
		"installing onto a sandbox must reuse the zip already stored on the catalog")
}

func TestRegisterCatalogReplacesThePreviousZip(t *testing.T) {
	fx := newInstallFixture(t)
	ctx := context.Background()
	first := zipBundle(t, map[string]string{
		"SKILL.md":           validSkillMD,
		"scripts/extract.py": "print('v1')\n",
	})
	cat, err := fx.svc.RegisterCatalogFromArchive(ctx, 7, first)
	require.NoError(t, err)
	firstRef := cat.BundleRef
	require.Equal(t, 1, fx.savedBundles)

	second := zipBundle(t, map[string]string{
		"SKILL.md":           validSkillMD,
		"scripts/extract.py": "print('v2')\n",
	})
	cat, err = fx.svc.RegisterCatalogFromArchive(ctx, 7, second)
	require.NoError(t, err)
	require.Equal(t, 2, fx.savedBundles)
	require.NotEqual(t, firstRef, cat.BundleRef)
	require.Equal(t, []string{firstRef}, fx.deletedBundles,
		"replacing the definition zip must drop the previous object")
}

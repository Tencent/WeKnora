package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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

// A re-register that cannot commit the row has to be a no-op. Retiring the
// previous object before the write would leave the stored definition naming a
// ref that no longer resolves, which takes its files down for good — far worse
// than the failed upload the caller asked about.
func TestRegisterCatalogKeepsThePreviousZipWhenTheRowFailsToCommit(t *testing.T) {
	fx := newInstallFixture(t)
	ctx := context.Background()
	first := zipBundle(t, map[string]string{
		"SKILL.md":           validSkillMD,
		"scripts/extract.py": "print('v1')\n",
	})
	cat, err := fx.svc.RegisterCatalogFromArchive(ctx, 7, first)
	require.NoError(t, err)
	firstRef := cat.BundleRef

	fx.skillRepo.updateCatalogErr = errors.New("database unavailable")
	second := zipBundle(t, map[string]string{
		"SKILL.md":           validSkillMD,
		"scripts/extract.py": "print('v2')\n",
	})
	_, err = fx.svc.RegisterCatalogFromArchive(ctx, 7, second)

	require.Error(t, err)
	require.Empty(t, fx.deletedBundles,
		"the stored definition still names the previous archive, so it must survive")

	stored, err := fx.skillRepo.GetCatalog(ctx, 7, cat.ID)
	require.NoError(t, err)
	require.Equal(t, firstRef, stored.BundleRef)
	require.NotEmpty(t, fx.storedBundles[firstRef],
		"the ref the row still names has to resolve to bytes")
}

// Two first-time registrations of one name race the unique index. The loser's
// object is keyed by the row ID that was never written, so nothing will ever
// name it again and the retry stores its own copy under the winner's key.
func TestRegisterCatalogDropsTheObjectOfTheRowThatLostTheNameRace(t *testing.T) {
	fx := newInstallFixture(t)
	ctx := context.Background()
	archive := zipBundle(t, map[string]string{"SKILL.md": validSkillMD})

	fx.skillRepo.createCatalogHook = func(e *types.TenantSkillCatalogEntity) error {
		// The winner appears between this caller's name lookup and its insert.
		fx.skillRepo.catalogs["cat-winner"] = &types.TenantSkillCatalogEntity{
			ID: "cat-winner", TenantID: e.TenantID, Name: e.Name,
		}
		fx.skillRepo.createCatalogHook = nil
		return errors.New("duplicate key value violates unique constraint")
	}

	cat, err := fx.svc.RegisterCatalogFromArchive(ctx, 7, archive)

	require.NoError(t, err)
	require.Equal(t, "cat-winner", cat.ID)
	require.Equal(t, 2, fx.savedBundles, "the retry stores the archive under the winner's key")
	require.Equal(t, []string{"file://bundle-1.zip"}, fx.deletedBundles,
		"the object of the row that was never written is unreachable and must go")
	require.NotEqual(t, "file://bundle-1.zip", cat.BundleRef)
}

// The sandbox that installed v1 goes on running v1 no matter what the
// definition says next, so the archive it was built from has to outlive the
// re-registration that supersedes it. Deleting it here is what would take
// read_skill and the admin file browser down for an install that works.
func TestRegisterCatalogKeepsTheReplacedZipForInstallsStillOnIt(t *testing.T) {
	fx := newInstallFixture(t)
	ctx := context.Background()
	first := zipBundle(t, map[string]string{
		"SKILL.md":           validSkillMD,
		"scripts/extract.py": "print('v1')\n",
	})
	firstBundle, err := ParseSkillBundle(first)
	require.NoError(t, err)
	skillID, err := fx.svc.InstallSkill(ctx, 7, "cfg-1", first)
	require.NoError(t, err)
	installed, err := fx.skillRepo.GetSkill(ctx, 7, "cfg-1", skillID)
	require.NoError(t, err)
	require.Empty(t, installed.BundleRef, "a fresh install reads the definition's copy")
	firstRef := fx.catalogRefFor(t, installed.CatalogID)

	second := zipBundle(t, map[string]string{
		"SKILL.md":           validSkillMD,
		"scripts/extract.py": "print('v2')\n",
	})
	_, err = fx.svc.RegisterCatalogFromArchive(ctx, 7, second)
	require.NoError(t, err)

	require.Empty(t, fx.deletedBundles,
		"an archive a live install was built from must survive the definition moving on")
	installed, err = fx.skillRepo.GetSkill(ctx, 7, "cfg-1", skillID)
	require.NoError(t, err)
	require.Equal(t, firstRef, installed.BundleRef,
		"the install now names the archive itself, so nothing has to remember its version")
	require.Equal(t, firstBundle.SHA256, installed.BundleSHA256)

	// The point of keeping the object: the file browser still answers with the
	// tree this sandbox actually has.
	files, err := fx.svc.ListSkillFiles(ctx, 7, "cfg-1", skillID)
	require.NoError(t, err)
	require.NotEmpty(t, files)
	content, err := fx.svc.ReadSkillFile(ctx, 7, "cfg-1", skillID, "scripts/extract.py")
	require.NoError(t, err)
	require.Contains(t, content.Content, "v1")
}

// A stamp that fails must not move the definition: the install still has an
// empty BundleRef, so committing the new catalog object would make read_skill
// and the file browser follow v2 while this sandbox is still running v1.
func TestRegisterCatalogDoesNotMoveTheDefinitionWhenPinFails(t *testing.T) {
	fx := newInstallFixture(t)
	ctx := context.Background()
	first := zipBundle(t, map[string]string{
		"SKILL.md":           validSkillMD,
		"scripts/extract.py": "print('v1')\n",
	})
	skillID, err := fx.svc.InstallSkill(ctx, 7, "cfg-1", first)
	require.NoError(t, err)
	installed, err := fx.skillRepo.GetSkill(ctx, 7, "cfg-1", skillID)
	require.NoError(t, err)
	catalogID := installed.CatalogID
	firstRef := fx.catalogRefFor(t, catalogID)

	fx.skillRepo.updateFailsWhen = func(e *types.TenantSkillEntity) bool {
		return strings.TrimSpace(e.BundleRef) == firstRef
	}

	second := zipBundle(t, map[string]string{
		"SKILL.md":           validSkillMD,
		"scripts/extract.py": "print('v2')\n",
	})
	_, err = fx.svc.RegisterCatalogFromArchive(ctx, 7, second)
	require.Error(t, err)

	stored, err := fx.skillRepo.GetCatalog(ctx, 7, catalogID)
	require.NoError(t, err)
	require.Equal(t, firstRef, stored.BundleRef,
		"the definition must keep naming v1 when the install could not be pinned")
	installed, err = fx.skillRepo.GetSkill(ctx, 7, "cfg-1", skillID)
	require.NoError(t, err)
	require.Empty(t, installed.BundleRef)

	content, err := fx.svc.ReadSkillFile(ctx, 7, "cfg-1", skillID, "scripts/extract.py")
	require.NoError(t, err)
	require.Contains(t, content.Content, "v1")
}

func TestRegisterCatalogSkipsSaveWhenTheSameZipIsStillStored(t *testing.T) {
	fx := newInstallFixture(t)
	ctx := context.Background()
	archive := zipBundle(t, map[string]string{"SKILL.md": validSkillMD})
	cat, err := fx.svc.RegisterCatalogFromArchive(ctx, 7, archive)
	require.NoError(t, err)
	require.Equal(t, 1, fx.savedBundles)
	firstRef := cat.BundleRef

	cat, err = fx.svc.RegisterCatalogFromArchive(ctx, 7, archive)
	require.NoError(t, err)
	require.Equal(t, 1, fx.savedBundles, "the same bytes must not mint a second object")
	require.Equal(t, firstRef, cat.BundleRef)
}

func TestRegisterCatalogRewritesAMissingObjectWithTheSameSHA(t *testing.T) {
	fx := newInstallFixture(t)
	ctx := context.Background()
	archive := zipBundle(t, map[string]string{"SKILL.md": validSkillMD})
	cat, err := fx.svc.RegisterCatalogFromArchive(ctx, 7, archive)
	require.NoError(t, err)
	firstRef := cat.BundleRef
	delete(fx.storedBundles, firstRef)

	cat, err = fx.svc.RegisterCatalogFromArchive(ctx, 7, archive)
	require.NoError(t, err)
	require.Equal(t, 2, fx.savedBundles)
	require.NotEqual(t, firstRef, cat.BundleRef)

	files, err := fx.svc.ListCatalogFiles(ctx, 7, cat.ID)
	require.NoError(t, err)
	require.NotEmpty(t, files)
}

// The mirror of the test above: once the last install of a pinned archive is
// gone, nothing can reach those bytes again, so removal is what reclaims them.
func TestRemovingThePinnedInstallReclaimsTheReplacedZip(t *testing.T) {
	fx := newInstallFixture(t)
	ctx := context.Background()
	first := zipBundle(t, map[string]string{
		"SKILL.md":           validSkillMD,
		"scripts/extract.py": "print('v1')\n",
	})
	skillID, err := fx.svc.InstallSkill(ctx, 7, "cfg-1", first)
	require.NoError(t, err)
	installed, err := fx.skillRepo.GetSkill(ctx, 7, "cfg-1", skillID)
	require.NoError(t, err)
	catalogID := installed.CatalogID
	firstRef := fx.catalogRefFor(t, catalogID)

	second := zipBundle(t, map[string]string{
		"SKILL.md":           validSkillMD,
		"scripts/extract.py": "print('v2')\n",
	})
	_, err = fx.svc.RegisterCatalogFromArchive(ctx, 7, second)
	require.NoError(t, err)
	secondRef := fx.catalogRefFor(t, catalogID)
	require.NotEqual(t, firstRef, secondRef)

	pinned, err := fx.skillRepo.GetSkill(ctx, 7, "cfg-1", skillID)
	require.NoError(t, err)
	require.Equal(t, firstRef, pinned.BundleRef)
	pinned.Status = types.SkillStatusRemoving
	require.NoError(t, fx.skillRepo.UpdateSkill(ctx, pinned))

	require.NoError(t, fx.svc.runRemove(ctx, 7, "cfg-1", skillID))

	require.Equal(t, []string{firstRef}, fx.deletedBundles,
		"the pinned archive is reclaimed with its last reader, the definition's is not")
}

// A sandbox still on v1 is pinned to v1's archive once the catalog moves to
// v2. Retrying it replays v1 for that sandbox alone: writing v1 back to the
// definition would turn every other sandbox's upgrade target into v1 and delete
// the only copy of v2.
func TestReinstallOfAnInstallStillOnTheReplacedArchiveLeavesTheCatalogAlone(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   string
		guidance []string
	}{
		{name: "failed retry", status: types.SkillStatusFailed},
		{name: "ready with guidance", status: types.SkillStatusReady, guidance: []string{"Install and verify the CLI"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newInstallFixture(t)
			ctx := context.Background()
			first := zipBundle(t, map[string]string{
				"SKILL.md":           validSkillMD,
				"scripts/extract.py": "print('v1')\n",
			})
			second := zipBundle(t, map[string]string{
				"SKILL.md":           validSkillMD,
				"scripts/extract.py": "print('v2')\n",
			})
			firstBundle, err := ParseSkillBundle(first)
			require.NoError(t, err)
			secondBundle, err := ParseSkillBundle(second)
			require.NoError(t, err)

			skillID, err := fx.svc.InstallSkill(ctx, 7, "cfg-1", first)
			require.NoError(t, err)
			fx.awaitSkillSettled(t, skillID)
			installed, err := fx.skillRepo.GetSkill(ctx, 7, "cfg-1", skillID)
			require.NoError(t, err)
			catalogID := installed.CatalogID
			firstRef := fx.catalogRefFor(t, catalogID)

			_, err = fx.svc.RegisterCatalogFromArchive(ctx, 7, second)
			require.NoError(t, err)
			secondRef := fx.catalogRefFor(t, catalogID)

			installed, err = fx.skillRepo.GetSkill(ctx, 7, "cfg-1", skillID)
			require.NoError(t, err)
			installed.Status = tc.status
			require.NoError(t, fx.skillRepo.UpdateSkill(ctx, installed))

			_, err = fx.svc.ReinstallSkill(ctx, 7, "cfg-1", skillID, tc.guidance...)
			require.NoError(t, err)
			fx.awaitSkillSettled(t, skillID)

			cat, err := fx.skillRepo.GetCatalog(ctx, 7, catalogID)
			require.NoError(t, err)
			require.Equal(t, secondBundle.SHA256, cat.BundleSHA256, "the definition stays on v2")
			require.Equal(t, secondRef, cat.BundleRef)
			require.NotContains(t, fx.deletedBundles, secondRef, "v2's archive must survive a v1 retry")
			require.NotContains(t, fx.deletedBundles, firstRef, "the retried install still reads v1's archive")

			installed, err = fx.skillRepo.GetSkill(ctx, 7, "cfg-1", skillID)
			require.NoError(t, err)
			require.Equal(t, firstRef, installed.BundleRef, "the retry keeps its pin to v1")
			require.Equal(t, firstBundle.SHA256, installed.BundleSHA256)
		})
	}
}

// Upgrading is installing the catalog onto a sandbox that is still on the
// replaced archive: the install moves to v2 and v1's archive, with no reader
// left, is reclaimed.
func TestInstallCatalogToConfigsUpgradesAnInstallStillOnTheReplacedArchive(t *testing.T) {
	fx := newInstallFixture(t)
	ctx := context.Background()
	first := zipBundle(t, map[string]string{
		"SKILL.md":           validSkillMD,
		"scripts/extract.py": "print('v1')\n",
	})
	second := zipBundle(t, map[string]string{
		"SKILL.md":           validSkillMD,
		"scripts/extract.py": "print('v2')\n",
	})
	secondBundle, err := ParseSkillBundle(second)
	require.NoError(t, err)

	skillID, err := fx.svc.InstallSkill(ctx, 7, "cfg-1", first)
	require.NoError(t, err)
	fx.awaitSkillSettled(t, skillID)
	installed, err := fx.skillRepo.GetSkill(ctx, 7, "cfg-1", skillID)
	require.NoError(t, err)
	firstRef := fx.catalogRefFor(t, installed.CatalogID)

	cat, err := fx.svc.RegisterCatalogFromArchive(ctx, 7, second)
	require.NoError(t, err)
	result, err := fx.svc.InstallCatalogToConfigs(ctx, 7, cat.ID, []string{"cfg-1"})
	require.NoError(t, err)
	require.Equal(t, skillID, result.Installs["cfg-1"], "an upgrade reuses the sandbox's row")
	fx.awaitSkillSettled(t, skillID)

	installed, err = fx.skillRepo.GetSkill(ctx, 7, "cfg-1", skillID)
	require.NoError(t, err)
	require.Equal(t, types.SkillStatusReady, installed.Status)
	require.Equal(t, secondBundle.SHA256, installed.BundleSHA256)
	require.Empty(t, installed.BundleRef, "the upgraded install follows the definition again")
	require.Equal(t, secondBundle.SHA256, fx.catalogSHAFor(t, cat.ID))
	require.Contains(t, fx.deletedBundles, firstRef, "v1's archive lost its last reader")
}

// Installing a definition onto a sandbox reads it; it does not re-register it.
// Rewriting the row would move its updated_at every time someone installs it.
func TestInstallCatalogToConfigsLeavesTheDefinitionUntouched(t *testing.T) {
	fx := newInstallFixture(t)
	ctx := context.Background()
	cat, err := fx.svc.RegisterCatalogFromArchive(ctx, 7, zipBundle(t, map[string]string{
		"SKILL.md":           validSkillMD,
		"scripts/extract.py": "print('v1')\n",
	}))
	require.NoError(t, err)
	registered, err := fx.skillRepo.GetCatalog(ctx, 7, cat.ID)
	require.NoError(t, err)
	savedBefore := fx.savedBundles
	fx.svc.now = func() time.Time { return registered.UpdatedAt.Add(time.Hour) }

	result, err := fx.svc.InstallCatalogToConfigs(ctx, 7, cat.ID, []string{"cfg-1"})
	require.NoError(t, err)
	fx.awaitSkillSettled(t, result.Installs["cfg-1"])

	after, err := fx.skillRepo.GetCatalog(ctx, 7, cat.ID)
	require.NoError(t, err)
	require.Equal(t, registered.UpdatedAt, after.UpdatedAt)
	require.Equal(t, registered.BundleRef, after.BundleRef)
	require.Equal(t, savedBefore, fx.savedBundles, "the definition's bytes are not stored again")
}

// Bytes neither the definition nor the row holds mean the catalog moved after
// its archive was read. Installing them would write that older archive back.
func TestStoredArchiveThatNoLongerMatchesTheCatalogIsRefused(t *testing.T) {
	fx := newInstallFixture(t)
	ctx := context.Background()
	first := zipBundle(t, map[string]string{
		"SKILL.md":           validSkillMD,
		"scripts/extract.py": "print('v1')\n",
	})
	second := zipBundle(t, map[string]string{
		"SKILL.md":           validSkillMD,
		"scripts/extract.py": "print('v2')\n",
	})
	firstBundle, err := ParseSkillBundle(first)
	require.NoError(t, err)
	_, err = fx.svc.RegisterCatalogFromArchive(ctx, 7, second)
	require.NoError(t, err)

	_, err = fx.svc.storedArchiveKeepsItsPin(ctx, 7, nil, firstBundle)

	var appErr *apperrors.AppError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, apperrors.ErrConflict, appErr.Code)
}

func (f *installFixture) awaitSkillSettled(t *testing.T, skillID string) {
	t.Helper()
	require.Eventually(t, func() bool {
		row, err := f.skillRepo.GetSkill(context.Background(), 7, "cfg-1", skillID)
		return err == nil && row != nil && row.Status != types.SkillStatusInstalling
	}, 5*time.Second, 10*time.Millisecond)
}

func (f *installFixture) catalogSHAFor(t *testing.T, catalogID string) string {
	t.Helper()
	cat, err := f.skillRepo.GetCatalog(context.Background(), 7, catalogID)
	require.NoError(t, err)
	require.NotNil(t, cat)
	return cat.BundleSHA256
}

func (f *installFixture) catalogRefFor(t *testing.T, catalogID string) string {
	t.Helper()
	cat, err := f.skillRepo.GetCatalog(context.Background(), 7, catalogID)
	require.NoError(t, err)
	require.NotNil(t, cat)
	return cat.BundleRef
}

package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	builtin "github.com/Tencent/WeKnora/internal/builtin/skills"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestBuiltinArchivesUseExistingInstallerFormat(t *testing.T) {
	for _, entry := range builtin.List() {
		if entry.Distribution != "builtin" {
			continue
		}
		archive, err := builtin.Archive(entry.ID)
		require.NoError(t, err)
		bundle, err := ParseSkillBundle(archive)
		require.NoError(t, err, entry.ID)
		require.Equal(t, entry.Name, bundle.Name)
		require.Equal(t, entry.Version, bundle.Version)
		require.True(t, builtin.Matches(bundle.Name, bundle.Files))
	}
}

func TestBuiltinRegistrationPreservesSameNameCustomSkill(t *testing.T) {
	fx := newInstallFixture(t)
	ctx := context.Background()
	original := &types.TenantSkillCatalogEntity{
		ID:           "custom-pdf",
		TenantID:     7,
		Name:         "pdf",
		BundleSHA256: strings.Repeat("a", 64),
		Instructions: "user instructions",
	}
	require.NoError(t, fx.skillRepo.CreateCatalog(ctx, original))
	_, err := fx.svc.RegisterBuiltin(ctx, 7, "pdf")
	require.Error(t, err)
	stored, err := fx.skillRepo.GetCatalogByName(ctx, 7, "pdf")
	require.NoError(t, err)
	require.Equal(t, original, stored)
	_, err = fx.svc.RegisterBuiltin(ctx, 7, "anthropic-pdf")
	require.Error(t, err)
}

func TestCatalogBuiltinFlagRequiresExactArchive(t *testing.T) {
	archive, err := builtin.Archive("pdf")
	require.NoError(t, err)
	row := &types.TenantSkillCatalogEntity{
		Name:         "pdf",
		Version:      builtin.Version,
		BundleSHA256: skillArchiveSHA256(archive),
	}
	require.True(t, catalogView(row, nil, nil).Builtin)
	row.BundleSHA256 = strings.Repeat("a", 64)
	require.False(t, catalogView(row, nil, nil).Builtin, "same name and version must not mislabel custom code")
	row.Name = "anthropic-pdf"
	require.False(t, catalogView(row, nil, nil).Builtin)
}

func TestPinnedRuntimeOnlyOffersMatchingSkillVersions(t *testing.T) {
	rows := []*types.TenantSkillEntity{
		{ID: "old", Name: "pdf", BundleSHA256: "new-version"},
		{ID: "same", Name: "xlsx", BundleSHA256: "original"},
		{ID: "absent", Name: "browser", BundleSHA256: "browser-version"},
	}
	manifest := []byte(
		`{"skills":[{"id":"old","name":"pdf","sha256":"old-version"},{"id":"same","name":"xlsx","sha256":"original"}]}`,
	)
	result := skillsMatchingRuntimeManifest(rows, manifest)
	require.Equal(t, []*types.TenantSkillEntity{rows[1]}, result)
	require.Empty(t, skillsMatchingRuntimeManifest(rows, []byte(`{}`)))
	require.Empty(t, skillsMatchingRuntimeManifest(rows, []byte(`invalid`)))
}

func TestMigrationBlocksMissingOriginalAndForeignConfiguration(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:migrate-discovery?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(
		t,
		db.AutoMigrate(
			&types.TenantSkillEntity{},
			&types.TenantSkillCatalogEntity{},
			&types.TenantSandboxConfigEntity{},
		),
	)
	configs := repository.NewTenantSandboxConfigRepository(db)
	skills := repository.NewTenantSkillRepository(db)
	ctx := context.Background()
	for _, config := range []*types.TenantSandboxConfigEntity{
		{ID: "old", TenantID: 7, Name: "old"},
		{ID: "new", TenantID: 7, Name: "new"},
		{ID: "foreign", TenantID: 8, Name: "foreign"},
	} {
		require.NoError(t, configs.Create(ctx, config))
	}
	for _, row := range []*types.TenantSkillEntity{
		{ID: "legacy", TenantID: 7, SandboxConfigID: "old", Name: "pdf", Enabled: true, Status: types.SkillStatusReady},
		{
			ID: "lost", TenantID: 7, SandboxConfigID: "old", Name: "xlsx", Enabled: true,
			Status: types.SkillStatusReady, BundleSHA256: strings.Repeat("b", 64),
		},
	} {
		require.NoError(t, skills.CreateSkill(ctx, row))
	}
	svc := &TenantSkillService{skills: skills, configs: configs}
	plan, err := svc.PlanSkillMigration(ctx, 7, "old", "new")
	require.NoError(t, err)
	blockers := map[string]string{}
	for _, item := range plan {
		blockers[item.Name] = item.Blocker
	}
	require.Equal(t, map[string]string{"pdf": "missing_digest", "xlsx": "missing_original_archive"}, blockers)
	_, err = svc.PlanSkillMigration(ctx, 7, "old", "foreign")
	require.Error(t, err)
	_, err = svc.PlanSkillMigration(ctx, 7, "old", "old")
	require.Error(t, err)
}

func TestBrowserCommandRejectsUnknownActionsAndOversizedPayloads(t *testing.T) {
	for _, command := range []BrowserCommand{
		{Action: "eval"}, {Action: "shell"}, {Action: "open", URL: strings.Repeat("x", 8193)},
	} {
		require.Error(t, command.Validate())
	}
	require.NoError(t, (BrowserCommand{Action: "frame"}).Validate())
}

func TestPreinstalledBuiltinRequiresExactDigestAndRealVerification(t *testing.T) {
	fx := newInstallFixture(t)
	archive, err := builtin.Archive("browser")
	require.NoError(t, err)
	bundle, err := ParseSkillBundle(archive)
	require.NoError(t, err)
	dir, err := sandbox.SkillDirFor("browser")
	require.NoError(t, err)
	job := installerJob{
		tenantID: 7, configID: "cfg-1", skillID: "sk-1", bundle: bundle,
		mgr: fx.sandboxMgr, sess: &types.Session{ID: "maintenance"}, skillDir: dir,
	}
	// A legacy image is eligible for normal dependency installation, not an
	// invented preinstalled capability.
	handled, err := fx.svc.tryPreinstalledBuiltin(context.Background(), job)
	require.NoError(t, err)
	require.False(t, handled)
	require.Empty(t, fx.commands)
	fx.sandboxMgr.files = map[string][]byte{
		builtin.ImageRoot + "/browser/.bundle-digest": []byte(builtin.DigestFiles(bundle.Files)),
		dir + "/.weknora/install-report.json":         []byte(`{"commands":[],"blockers":[]}`),
	}
	handled, err = fx.svc.tryPreinstalledBuiltin(context.Background(), job)
	require.NoError(t, err)
	require.True(t, handled)
	require.Contains(t, strings.Join(fx.commands, "\n"), "weknora_smoke.py")
	require.Empty(t, fx.agentPrompts)
	// A matching marker cannot turn a failed functional probe into success or
	// silently fall back to the LLM and conceal a broken official image.
	fx.depsExitCode = 9
	handled, err = fx.svc.tryPreinstalledBuiltin(context.Background(), job)
	require.True(t, handled)
	require.Error(t, err)
}

func TestMigrationPinsOldArchiveWithoutDowngradingCatalog(t *testing.T) {
	fx := newInstallFixture(t)
	ctx := context.Background()
	archive := []byte("the original pinned archive")
	row, err := fx.skillRepo.GetSkill(ctx, 7, "cfg-1", "sk-1")
	require.NoError(t, err)
	row.BundleSHA256 = skillArchiveSHA256(archive)
	require.NoError(t, fx.skillRepo.UpdateSkill(ctx, row))
	newer := &types.TenantSkillCatalogEntity{
		ID:           "cat-new",
		TenantID:     7,
		Name:         row.Name,
		Version:      "2",
		BundleSHA256: "new-digest",
	}
	require.NoError(t, fx.skillRepo.CreateCatalog(ctx, newer))
	require.NoError(
		t,
		fx.svc.pinMigratedBundle(ctx, 7, "cfg-1", row.ID, &types.TenantSkillEntity{CatalogID: newer.ID}, archive),
	)
	stored, err := fx.skillRepo.GetCatalog(ctx, 7, newer.ID)
	require.NoError(t, err)
	require.Equal(t, newer, stored)
	pinned, err := fx.svc.skillBundleArchive(ctx, 7, "cfg-1", row.ID)
	require.NoError(t, err)
	require.Equal(t, archive, pinned)
}

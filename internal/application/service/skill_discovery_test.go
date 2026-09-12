package service

import (
	"context"
	"strings"
	"testing"

	builtin "github.com/Tencent/WeKnora/internal/builtin/skills"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
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
	_, err := fx.svc.RegisterBuiltin(ctx, 7, "pdf", false)
	require.Error(t, err)
	stored, err := fx.skillRepo.GetCatalogByName(ctx, 7, "pdf")
	require.NoError(t, err)
	require.Equal(t, original, stored)
	_, err = fx.svc.RegisterBuiltin(ctx, 7, "anthropic-pdf", false)
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

func TestBuiltinReplacementPreservesExistingInstallResources(t *testing.T) {
	fx := newInstallFixture(t)
	ctx := context.Background()
	oldArchive := zipBundle(t, map[string]string{
		"SKILL.md": "---\nname: pdf\ndescription: Previous pdf skill\n" +
			"version: 2026.09.2\n---\nOld instructions\n",
	})
	oldCatalog, err := fx.svc.RegisterCatalogFromArchive(ctx, 7, oldArchive)
	require.NoError(t, err)
	oldRef, oldDigest := oldCatalog.BundleRef, oldCatalog.BundleSHA256
	require.NoError(t, fx.skillRepo.CreateSkill(ctx, &types.TenantSkillEntity{
		ID: "old-pdf", TenantID: 7, SandboxConfigID: "cfg-1", CatalogID: oldCatalog.ID,
		Name: "pdf", BundleSHA256: oldDigest, Status: types.SkillStatusReady, Enabled: true,
	}))

	_, err = fx.svc.RegisterBuiltin(ctx, 7, "pdf", false)
	require.Error(t, err, "different bytes must require explicit replacement")
	require.Equal(t, 1, fx.savedBundles)
	current, err := fx.svc.RegisterBuiltin(ctx, 7, "pdf", true)
	require.NoError(t, err)
	require.Equal(t, oldCatalog.ID, current.ID)
	require.Equal(t, "2026.09.5", current.Version)
	require.NotEqual(t, oldDigest, current.BundleSHA256)
	installed, err := fx.skillRepo.GetSkill(ctx, 7, "cfg-1", "old-pdf")
	require.NoError(t, err)
	require.Equal(t, oldRef, installed.BundleRef)
	require.Equal(t, oldDigest, installed.BundleSHA256)
	require.Equal(t, types.SkillStatusReady, installed.Status)
	require.Empty(t, fx.deletedBundles)

	files, err := fx.svc.ReadSkillFile(ctx, 7, "cfg-1", "old-pdf", "SKILL.md")
	require.NoError(t, err)
	require.Contains(t, files.Content, "Old instructions")
	_, err = fx.svc.RegisterBuiltin(ctx, 7, "pdf", false)
	require.NoError(t, err, "the current package can be registered again without replacement")
	require.Equal(t, 2, fx.savedBundles)
}

func TestPinnedRuntimeOnlyOffersMatchingSkillVersions(t *testing.T) {
	rows := []*types.TenantSkillEntity{
		{ID: "old", Name: "pdf", BundleSHA256: "new-version"},
		{ID: "same", Name: "xlsx", BundleSHA256: "original"},
		{ID: "absent", Name: "docx", BundleSHA256: "docx-version"},
	}
	manifest := []byte(
		`{"skills":[{"id":"old","name":"pdf","sha256":"old-version"},{"id":"same","name":"xlsx","sha256":"original"}]}`,
	)
	result := skillsMatchingRuntimeManifest(rows, manifest)
	require.Equal(t, []*types.TenantSkillEntity{rows[1]}, result)
	require.Empty(t, skillsMatchingRuntimeManifest(rows, []byte(`{}`)))
	require.Empty(t, skillsMatchingRuntimeManifest(rows, []byte(`invalid`)))
}

func TestPreinstalledBuiltinRequiresExactDigestAndRealVerification(t *testing.T) {
	fx := newInstallFixture(t)
	archive, err := builtin.Archive("pdf")
	require.NoError(t, err)
	bundle, err := ParseSkillBundle(archive)
	require.NoError(t, err)
	dir, err := sandbox.SkillDirFor("pdf")
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
		builtin.ImageRoot + "/pdf/.bundle-digest": []byte(builtin.DigestFiles(bundle.Files)),
		dir + "/.weknora/install-report.json":     []byte(`{"commands":[],"blockers":[]}`),
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

func TestPreinstalledPowerpointLinksLockedNodeEnvironment(t *testing.T) {
	fx := newInstallFixture(t)
	archive, err := builtin.Archive("powerpoint")
	require.NoError(t, err)
	bundle, err := ParseSkillBundle(archive)
	require.NoError(t, err)
	dir, err := sandbox.SkillDirFor("powerpoint")
	require.NoError(t, err)
	fx.sandboxMgr.files = map[string][]byte{
		builtin.ImageRoot + "/powerpoint/.bundle-digest": []byte(builtin.DigestFiles(bundle.Files)),
		dir + "/.weknora/install-report.json":            []byte(`{"commands":[],"blockers":[]}`),
	}
	handled, err := fx.svc.tryPreinstalledBuiltin(context.Background(), installerJob{
		tenantID: 7, configID: "cfg-1", skillID: "sk-1", bundle: bundle,
		mgr: fx.sandboxMgr, sess: &types.Session{ID: "maintenance"}, skillDir: dir,
	})
	require.NoError(t, err)
	require.True(t, handled)
	commands := strings.Join(fx.commands, "\n")
	require.Contains(t, commands, "ln -s "+sandbox.ShellQuote(builtin.ImageRoot+"/powerpoint/node_modules")+
		" "+sandbox.ShellQuote(dir+"/node_modules"))
	require.Contains(t, commands, "weknora_smoke.py")
	require.Empty(t, fx.agentPrompts)
}

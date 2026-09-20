package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

func servedSkillMD(version, body string) string {
	return "---\nname: pdf-tools\ndescription: Extract text from PDF files\n" +
		"version: " + version + "\n---\n\n" + body + "\n"
}

func servedSkillZip(t *testing.T, version, body, script string) []byte {
	t.Helper()
	return zipBundle(t, map[string]string{
		"SKILL.md":           servedSkillMD(version, body),
		"scripts/extract.py": script,
	})
}

func (f *installFixture) usableSkills(t *testing.T) []*types.TenantSkillEntity {
	t.Helper()
	return effectiveTenantSkills(context.Background(), f.configRepo, f.skillRepo, 7, "cfg-1")
}

func (f *installFixture) installReady(t *testing.T, archive []byte) string {
	t.Helper()
	skillID, err := f.svc.InstallSkill(context.Background(), 7, "cfg-1", archive)
	require.NoError(t, err)
	f.awaitSkillSettled(t, skillID)
	row, err := f.skillRepo.GetSkill(context.Background(), 7, "cfg-1", skillID)
	require.NoError(t, err)
	require.Equal(t, types.SkillStatusReady, row.Status)
	return skillID
}

// The agent is told about a skill for as long as the image carries it. An
// upgrade only replaces the image when it succeeds, so while it runs the
// previous version is what every sandbox executes, and the agent must keep
// seeing exactly that version rather than losing the skill for minutes.
func TestUpgradeKeepsThePreviousVersionServingWhileItRuns(t *testing.T) {
	fx := newInstallFixture(t)
	ctx := context.Background()
	first := servedSkillZip(t, "1.0.0", "Use v1.", "print('v1')\n")
	firstBundle, err := ParseSkillBundle(first)
	require.NoError(t, err)
	skillID := fx.installReady(t, first)
	installed, err := fx.skillRepo.GetSkill(ctx, 7, "cfg-1", skillID)
	require.NoError(t, err)
	firstRef := fx.catalogRefFor(t, installed.CatalogID)

	cat, err := fx.svc.RegisterCatalogFromArchive(ctx, 7, servedSkillZip(t, "2.0.0", "Use v2.", "print('v2')\n"))
	require.NoError(t, err)

	var during []*types.TenantSkillEntity
	var rowDuring *types.TenantSkillEntity
	var deletedDuring []string
	fx.beforeExecute = func() {
		during = fx.usableSkills(t)
		rowDuring, _ = fx.skillRepo.GetSkill(ctx, 7, "cfg-1", skillID)
		deletedDuring = append([]string(nil), fx.deletedBundles...)
	}
	_, err = fx.svc.InstallCatalogToConfigs(ctx, 7, cat.ID, []string{"cfg-1"})
	require.NoError(t, err)
	fx.awaitSkillSettled(t, skillID)

	require.Equal(t, types.SkillStatusInstalling, rowDuring.Status)
	require.Equal(t, "2.0.0", rowDuring.Version, "the row describes the install in flight")
	require.Len(t, during, 1, "the skill must not drop out of the agent's set mid-upgrade")
	require.Equal(t, "1.0.0", during[0].Version)
	require.Contains(t, during[0].Instructions, "Use v1.")
	require.Equal(t, firstBundle.SHA256, during[0].BundleSHA256)
	require.Equal(t, firstRef, during[0].BundleRef, "resource files are read from the archive v1 was built from")
	// Pointing the install at v2 releases the v1 archive its row was pinned
	// to; the served version still reads it, so it must survive that.
	require.NotContains(t, deletedDuring, firstRef)
	require.Equal(t, types.SkillStatusReady, during[0].Status)

	after := fx.usableSkills(t)
	require.Len(t, after, 1)
	require.Equal(t, "2.0.0", after[0].Version)
	require.Contains(t, after[0].Instructions, "Use v2.")
	final, err := fx.skillRepo.GetSkill(ctx, 7, "cfg-1", skillID)
	require.NoError(t, err)
	require.Nil(t, final.Served, "once the pointer moves nothing older is served")
	require.Contains(t, fx.deletedBundles, firstRef, "v1's archive lost its last reader with the upgrade")
}

// An upgrade that fails never moved the pointer, so v1 is still what runs.
// Dropping the skill until someone fixes the upgrade would turn a failed
// attempt at an improvement into an outage.
func TestFailedUpgradeLeavesThePreviousVersionServing(t *testing.T) {
	fx := newInstallFixture(t)
	ctx := context.Background()
	skillID := fx.installReady(t, servedSkillZip(t, "1.0.0", "Use v1.", "print('v1')\n"))
	installed, err := fx.skillRepo.GetSkill(ctx, 7, "cfg-1", skillID)
	require.NoError(t, err)
	firstRef := fx.catalogRefFor(t, installed.CatalogID)

	fx.agentErr = errAgentBoom
	// An upload is the harder path: the catalog object v1 was read from is
	// replaced by v2's before the install even starts.
	_, err = fx.svc.InstallSkill(ctx, 7, "cfg-1", servedSkillZip(t, "2.0.0", "Use v2.", "print('v2')\n"))
	require.NoError(t, err)
	fx.awaitSkillSettled(t, skillID)

	failed, err := fx.skillRepo.GetSkill(ctx, 7, "cfg-1", skillID)
	require.NoError(t, err)
	require.Equal(t, types.SkillStatusFailed, failed.Status)
	require.Equal(t, &SkillServedInfo{Version: "1.0.0"}, ServedInfoOf(failed))
	require.NotContains(t, fx.deletedBundles, firstRef,
		"the served version still reads its files from v1's archive")

	usable := fx.usableSkills(t)
	require.Len(t, usable, 1)
	require.Equal(t, "1.0.0", usable[0].Version)
	require.Equal(t, firstRef, usable[0].BundleRef)

	// A retry of the failed upgrade retries v2, and a success retires v1.
	fx.agentErr = nil
	_, err = fx.svc.ReinstallSkill(ctx, 7, "cfg-1", skillID)
	require.NoError(t, err)
	fx.awaitSkillSettled(t, skillID)
	usable = fx.usableSkills(t)
	require.Len(t, usable, 1)
	require.Equal(t, "2.0.0", usable[0].Version)
	require.Contains(t, fx.deletedBundles, firstRef)
}

// A first install has nothing in the image yet, so there is nothing to serve
// while it runs or after it fails.
func TestFirstInstallServesNothingUntilItIsReady(t *testing.T) {
	fx := newInstallFixture(t)
	fx.agentErr = errAgentBoom

	skillID, err := fx.svc.InstallSkill(context.Background(), 7, "cfg-1",
		servedSkillZip(t, "1.0.0", "Use v1.", "print('v1')\n"))
	require.NoError(t, err)
	fx.awaitSkillSettled(t, skillID)

	row, err := fx.skillRepo.GetSkill(context.Background(), 7, "cfg-1", skillID)
	require.NoError(t, err)
	require.Equal(t, types.SkillStatusFailed, row.Status)
	require.Nil(t, row.Served)
	require.Nil(t, ServedInfoOf(row))
	require.Empty(t, fx.usableSkills(t))
}

// The declaration replaces the previous one and drops the values of variables
// the new version stops reading. Written before the pointer moves, an upgrade
// that then fails would leave v1 running without a credential an admin typed.
func TestUpgradeFailingAfterVerificationKeepsTheServedVersionsEnvValues(t *testing.T) {
	fx := newInstallFixture(t)
	ctx := context.Background()
	fx.seedReadySkillWithSHA(fx.bundle.SHA256, "snap-v1")
	require.NoError(t, fx.skillRepo.UpdateSkillEnvs(ctx, 7, "cfg-1", "sk-1", types.SkillEnvVars{
		{Name: "LEGACY_KEY", Required: true, Value: "admin-typed"},
	}))
	row, err := fx.skillRepo.GetSkill(ctx, 7, "cfg-1", "sk-1")
	require.NoError(t, err)
	row.Served = fx.svc.servedVersionOf(ctx, row)
	row.Status = types.SkillStatusInstalling
	require.NoError(t, fx.skillRepo.UpdateSkill(ctx, row))

	fx.readsTavilyKey()
	fx.declareEnvFile(`{"env":[{"name":"TAVILY_API_KEY","required":true}]}`)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	fx.cancelDuringSnapshot = cancel

	require.Error(t, fx.svc.runInstall(runCtx, 7, "cfg-1", "sk-1", fx.bundle))

	after, err := fx.skillRepo.GetSkill(ctx, 7, "cfg-1", "sk-1")
	require.NoError(t, err)
	require.Equal(t, types.SkillEnvVars{{Name: "LEGACY_KEY", Required: true, Value: "admin-typed"}}, after.Envs)
	require.NotNil(t, after.ServedView(), "v1 still runs, and still reads its credential")
}

func TestServedViewReadsThePreviousVersionOnlyWhileItIsServed(t *testing.T) {
	served := &types.SkillServedVersion{
		Version: "1.0.0", Description: "v1", Instructions: "Use v1.",
		BundleSHA256: "sha-v1", BundleRef: "file://v1.zip", SnapshotID: "snap-v1",
	}
	base := types.TenantSkillEntity{
		ID: "sk-1", Name: "pdf-tools", Version: "2.0.0", Instructions: "Use v2.",
		BundleSHA256: "sha-v2", Enabled: true, Served: served, Error: "boom",
	}

	for _, status := range []string{types.SkillStatusInstalling, types.SkillStatusFailed} {
		row := base
		row.Status = status
		view := row.ServedView()
		require.NotNil(t, view, status)
		require.Equal(t, types.SkillStatusReady, view.Status)
		require.Equal(t, "1.0.0", view.Version)
		require.Equal(t, "Use v1.", view.Instructions)
		require.Equal(t, "file://v1.zip", view.BundleRef)
		require.Equal(t, "snap-v1", view.InstalledSnapshotID)
		require.Empty(t, view.Error)
		require.Nil(t, view.Served)
		require.Equal(t, "2.0.0", row.Version, "the row itself is not rewritten")
	}

	removing := base
	removing.Status = types.SkillStatusRemoving
	require.Nil(t, removing.ServedView(), "a removal takes the skill away on purpose")

	ready := base
	ready.Status = types.SkillStatusReady
	require.Same(t, &ready, ready.ServedView())

	firstInstall := base
	firstInstall.Status = types.SkillStatusInstalling
	firstInstall.Served = nil
	require.Nil(t, firstInstall.ServedView())
}

func TestServedVersionRoundTripsThroughTheRepository(t *testing.T) {
	repo := catalogTestRepo(t, "file:"+t.Name()+"?mode=memory&cache=shared")
	ctx := context.Background()
	now := time.Now()
	require.NoError(t, repo.CreateSkill(ctx, &types.TenantSkillEntity{
		ID: "sk-1", TenantID: 7, SandboxConfigID: "cfg-1", Name: "pdf-tools",
		Status: types.SkillStatusReady, CreatedAt: now, UpdatedAt: now,
	}))
	row, err := repo.GetSkill(ctx, 7, "cfg-1", "sk-1")
	require.NoError(t, err)
	require.Nil(t, row.Served)

	row.Status = types.SkillStatusInstalling
	row.Served = &types.SkillServedVersion{Version: "1.0.0", BundleRef: "file://v1.zip", SnapshotID: "snap-v1"}
	require.NoError(t, repo.UpdateSkill(ctx, row))
	row, err = repo.GetSkill(ctx, 7, "cfg-1", "sk-1")
	require.NoError(t, err)
	require.Equal(t, &types.SkillServedVersion{Version: "1.0.0", BundleRef: "file://v1.zip", SnapshotID: "snap-v1"},
		row.Served)

	row.Served = nil
	require.NoError(t, repo.UpdateSkill(ctx, row))
	row, err = repo.GetSkill(ctx, 7, "cfg-1", "sk-1")
	require.NoError(t, err)
	require.Nil(t, row.Served)
}

func (f *reaperFixture) stuckUpgrade() {
	staleSince := f.now.Add(-skillInstallStuckTTL - time.Minute)
	f.skills.put(&types.TenantSkillEntity{
		ID: "sk-1", TenantID: 7, SandboxConfigID: "cfg-1", Name: "pdf", Enabled: true,
		Version: "2.0.0", Instructions: "Use v2.", BundleSHA256: "sha-v2", BundleRef: "file://v2.zip",
		InstalledSnapshotID: "snap-v1", Status: types.SkillStatusInstalling, InstallingSince: &staleSince,
		Served: &types.SkillServedVersion{
			Version: "1.0.0", Instructions: "Use v1.", BundleSHA256: "sha-v1",
			BundleRef: "file://v1.zip", SnapshotID: "snap-v1",
		},
	})
	f.installed("sk-1", "snap-v1", "")
}

// An upgrade whose process died before the pointer moved left v1 in the image.
// Healing the row to ready as it stands would label v1's files as v2.
func TestReapStuckRunsRestoresTheServedVersionOfAnUpgradeThatNeverLanded(t *testing.T) {
	fx := newReaperFixture(t)
	fx.stuckUpgrade()
	fx.live("snap-v1")

	n, err := fx.svc.ReapStuckRuns(context.Background())

	require.NoError(t, err)
	require.Equal(t, 1, n)
	got := fx.skills.mustGet("sk-1")
	require.Equal(t, types.SkillStatusReady, got.Status)
	require.Equal(t, "1.0.0", got.Version)
	require.Equal(t, "Use v1.", got.Instructions)
	require.Equal(t, "sha-v1", got.BundleSHA256)
	require.Equal(t, "file://v1.zip", got.BundleRef)
	require.Equal(t, "snap-v1", got.InstalledSnapshotID)
	require.Nil(t, got.Served)
	require.Nil(t, got.InstallingSince)
}

// The upgrade's own generation is on the live chain: only its terminal write
// was lost, so the row already says what the image serves.
func TestReapStuckRunsKeepsAnUpgradeThatLanded(t *testing.T) {
	fx := newReaperFixture(t)
	fx.stuckUpgrade()
	fx.installed("sk-1", "snap-v2", "snap-v1")
	fx.live("snap-v2")

	n, err := fx.svc.ReapStuckRuns(context.Background())

	require.NoError(t, err)
	require.Equal(t, 1, n)
	got := fx.skills.mustGet("sk-1")
	require.Equal(t, types.SkillStatusReady, got.Status)
	require.Equal(t, "2.0.0", got.Version)
	require.Equal(t, "snap-v2", got.InstalledSnapshotID)
	require.Nil(t, got.Served)
}

// v1 keeps the skill available while the row stays installing, so a chain the
// reaper cannot read is no reason to guess which version the image has.
func TestReapStuckRunsLeavesAnUpgradeAloneWhenTheChainCannotBeFollowed(t *testing.T) {
	fx := newReaperFixture(t)
	fx.stuckUpgrade()
	fx.live("snap-missing")

	n, err := fx.svc.ReapStuckRuns(context.Background())

	require.NoError(t, err)
	require.Zero(t, n)
	got := fx.skills.mustGet("sk-1")
	require.Equal(t, types.SkillStatusInstalling, got.Status)
	require.NotNil(t, got.Served)
	require.NotNil(t, got.ServedView())
}

// Removing a skill whose upgrade failed, and failing at that, puts the row back
// as the version the image still has rather than the upgrade that never landed.
func TestFailedRemovalOfAFailedUpgradeRestoresTheServedVersion(t *testing.T) {
	fx := newInstallFixture(t)
	fx.seedInstalledSkill("sk-1", "snap-old", 2)
	fx.seedInstalledSkill("sk-2", "snap-old", 2)
	require.NoError(t, fx.svc.updateSkillFields(context.Background(), 7, "cfg-1", "sk-1",
		func(e *types.TenantSkillEntity) {
			e.Served = &types.SkillServedVersion{
				Version: e.Version, Instructions: "Use v1.", BundleRef: e.BundleRef, SnapshotID: "snap-old",
			}
			e.Version = "2.0.0"
			e.Instructions = "Use v2."
			e.BundleRef = ""
		}))
	fx.rmExitCode = 1

	require.Error(t, fx.svc.runRemove(context.Background(), 7, "cfg-1", "sk-1"))

	skill, err := fx.skillRepo.GetSkill(context.Background(), 7, "cfg-1", "sk-1")
	require.NoError(t, err)
	require.Equal(t, types.SkillStatusReady, skill.Status)
	require.Equal(t, "1.0.0", skill.Version)
	require.Equal(t, "Use v1.", skill.Instructions)
	require.Equal(t, "file://sk-1.zip", skill.BundleRef)
	require.Nil(t, skill.Served)
}

func TestReapStuckRunsRestoresTheServedVersionOfAnAbandonedRemoval(t *testing.T) {
	fx := newReaperFixture(t)
	fx.stuckUpgrade()
	row := fx.skills.mustGet("sk-1")
	row.Status = types.SkillStatusRemoving
	fx.skills.put(row)
	fx.live("snap-v1")

	n, err := fx.svc.ReapStuckRuns(context.Background())

	require.NoError(t, err)
	require.Equal(t, 1, n)
	got := fx.skills.mustGet("sk-1")
	require.Equal(t, types.SkillStatusReady, got.Status)
	require.Equal(t, "1.0.0", got.Version)
	require.Equal(t, "file://v1.zip", got.BundleRef)
	require.Nil(t, got.Served)
}

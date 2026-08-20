package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// The removal command is asserted verbatim for the same reason the install
// commands are: it runs as root with the skills root writable.
const removeSkillDirCommand = "rm -rf " + installSkillDir

func TestRunRemoveProducesANewSnapshotWithoutTheSkillDir(t *testing.T) {
	fx := newInstallFixture(t)
	fx.seedInstalledSkill("sk-1", "snap-old", 2)
	// A second skill is what keeps the image worth carrying. With sk-1 the
	// only one installed, the flow falls back to the base template instead
	// and spends no snapshot at all (the test below).
	fx.seedInstalledSkill("sk-2", "snap-old", 2)

	err := fx.svc.runRemove(context.Background(), 7, "cfg-1", "sk-1")

	require.NoError(t, err)
	require.Contains(t, fx.commands, removeSkillDirCommand)
	require.Equal(t, "snap-1", fx.configRepo.saved.Config.SkillImage.SnapshotID)
	require.Equal(t, 3, fx.configRepo.saved.Config.SkillImage.Generation)
	require.NotContains(t, fx.deletedSnapshots, "snap-old",
		"the ledger records the old snapshot ID, so it must stay resolvable")
	require.Equal(t, []string{"sess-1"}, fx.destroyedSandboxes,
		"the maintenance sandbox is released, and only that one")
	require.Equal(t, []string{"CreateSession"}, fx.sessionCalls,
		"the maintenance session is kept for troubleshooting")
	require.Equal(t, []string{"Skill remove"}, fx.sessionTitles,
		"the transcript is kept to troubleshoot this operation, so it must name it")
	require.Equal(t, []string{"file://sk-1.zip"}, fx.deletedBundles,
		"the archive outlives nothing: its only readers are the removed row and read_skill")

	skill, err := fx.skillRepo.GetSkill(context.Background(), 7, "cfg-1", "sk-1")
	require.NoError(t, err)
	require.Nil(t, skill, "the DB row goes away only after the image no longer has the skill")

	// The other half of "never delete, mark superseded": the delete-side
	// assertion above says snap-old survives on the provider, this says the
	// ledger stops calling it current instead of losing track of it.
	rows := listSnapshotRows(t, fx)
	require.Len(t, rows, 3, "the two seeded rows are kept and the removal adds its own")
	for _, row := range rows {
		switch row.SnapshotID {
		case "snap-old":
			require.Equal(t, types.SkillSnapshotStateSuperseded, row.State,
				"a snapshot nothing points at any more is superseded, never deleted")
		case "snap-1":
			require.Equal(t, types.SkillSnapshotStateActive, row.State)
			require.Equal(t, types.SkillSnapshotTriggerRemove, row.Trigger,
				"the ledger has to say which operation produced this image")
		default:
			t.Fatalf("unexpected ledger row for snapshot %q", row.SnapshotID)
		}
	}
}

func listSnapshotRows(
	t *testing.T, fx *installFixture,
) []*types.TenantSkillSnapshotEntity {
	t.Helper()
	rows, err := fx.skillRepo.ListSnapshotsByConfig(context.Background(), 7, "cfg-1")
	require.NoError(t, err)
	return rows
}

func TestRunRemoveDropsTheSkillFromTheImageManifest(t *testing.T) {
	fx := newInstallFixture(t)
	fx.seedInstalledSkill("sk-1", "snap-old", 2)
	fx.seedInstalledSkill("sk-2", "snap-old", 2)

	require.NoError(t, fx.svc.runRemove(context.Background(), 7, "cfg-1", "sk-1"))

	var manifest skillImageManifest
	require.NoError(t, json.Unmarshal(
		fx.sandboxMgr.writeContents[sandbox.SkillsManifestPath], &manifest))
	require.Len(t, manifest.Skills, 1,
		"the manifest is what an operator reads to see what the image claims to carry")
	require.Equal(t, "sk-2", manifest.Skills[0].ID)
}

func TestRunRemoveFallsBackToBaseTemplateWhenNoSkillsRemain(t *testing.T) {
	fx := newInstallFixture(t)
	fx.seedInstalledSkill("sk-1", "snap-old", 2)

	require.NoError(t, fx.svc.runRemove(context.Background(), 7, "cfg-1", "sk-1"))

	require.Empty(t, fx.configRepo.saved.Config.SkillImage.SnapshotID,
		"an image with no skills left is just the base template; do not spend a snapshot on it")
	require.Equal(t, []string{"switch-pointer"}, fx.events,
		"clearing the pointer is the whole job: no sandbox is started, so none is billed")
	require.Empty(t, fx.commands)

	skill, err := fx.skillRepo.GetSkill(context.Background(), 7, "cfg-1", "sk-1")
	require.NoError(t, err)
	require.Nil(t, skill)

	// Nothing points at snap-old any more, so no ledger row may still call
	// itself active - and the snapshot itself is still not deleted.
	rows := listSnapshotRows(t, fx)
	require.Len(t, rows, 1, "no snapshot was taken, so no row was added")
	require.Equal(t, types.SkillSnapshotStateSuperseded, rows[0].State)
	require.Equal(t, "snap-old", rows[0].SnapshotID)
	require.Empty(t, fx.deletedSnapshots)
}

// A snapshot that the live credentials cannot resolve is not a snapshot this
// run may build on: the sandbox would boot the BASE TEMPLATE, the removal would
// wipe a directory that is not there, and the pointer would move past an image
// that is still recoverable by restoring the credentials.
func TestRunRemoveRefusesAnImageThatBelongsToAnotherProviderAccount(t *testing.T) {
	fx := newInstallFixture(t)
	fx.seedInstalledSkill("sk-1", "snap-old", 2)
	fx.seedInstalledSkill("sk-2", "snap-old", 2)
	fx.configRepo.entity.Config.E2B.APIKey = "key-2"

	err := fx.svc.runRemove(context.Background(), 7, "cfg-1", "sk-1")

	require.Error(t, err)
	require.ErrorContains(t, err, "provider account")
	require.Empty(t, fx.events, "no sandbox may be started on the wrong image")
	require.Empty(t, fx.commands)
	require.Nil(t, fx.configRepo.saved,
		"moving the pointer here would commit a loss that restoring the credentials still undoes")

	skill, _ := fx.skillRepo.GetSkill(context.Background(), 7, "cfg-1", "sk-1")
	require.NotNil(t, skill)
	require.Equal(t, types.SkillStatusReady, skill.Status,
		"every other skill in that image is still ready, and so is this one")
}

// The lock check has to sit above every write, not just above the snapshot:
// the fallback moves the image pointer without taking one.
func TestRunRemoveStopsBeforeClearingThePointerWhenTheLockIsLost(t *testing.T) {
	fx := newInstallFixture(t)
	fx.seedInstalledSkill("sk-1", "snap-old", 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fx.cancelDuringConfigRead = cancel

	err := fx.svc.runRemove(ctx, 7, "cfg-1", "sk-1")

	require.Error(t, err)
	require.ErrorContains(t, err, "lock lost",
		"the run must stop on the lost lock itself, not on whatever write happens to fail next")
	require.Nil(t, fx.configRepo.saved,
		"another run may already be building on this config; do not move its pointer")
}

// RemoveSkill validates the skill outside the lock, so two submissions queue
// two runs. The second one has nothing left to do and must not spend a sandbox,
// a snapshot and a generation bump proving it.
func TestRunRemoveDoesNothingWhenTheSkillIsAlreadyGone(t *testing.T) {
	fx := newInstallFixture(t)
	fx.seedInstalledSkill("sk-1", "snap-old", 2)
	fx.seedInstalledSkill("sk-2", "snap-old", 2)
	require.NoError(t, fx.skillRepo.DeleteSkill(context.Background(), 7, "cfg-1", "sk-1"))

	require.NoError(t, fx.svc.runRemove(context.Background(), 7, "cfg-1", "sk-1"))

	require.Empty(t, fx.events, "no session, no sandbox, no snapshot")
	require.Empty(t, fx.commands)
	require.Nil(t, fx.configRepo.saved)
}

// The snapshot exists before the ledger can be told about it, so a failure in
// between leaves its ID nowhere but a local variable. That is the same orphan
// the failed pointer switch produces and it gets the same treatment.
func TestRunRemoveDeletesTheSnapshotWhenTheLedgerCannotRecordIt(t *testing.T) {
	fx := newInstallFixture(t)
	fx.seedInstalledSkill("sk-1", "snap-old", 2)
	fx.seedInstalledSkill("sk-2", "snap-old", 2)
	fx.skillRepo.markStateFails = func(state string) bool {
		return state == types.SkillSnapshotStateActive
	}

	err := fx.svc.runRemove(context.Background(), 7, "cfg-1", "sk-1")

	require.Error(t, err)
	require.Contains(t, fx.deletedSnapshots, "snap-1",
		"a snapshot no row names is unreachable and billed; it must not be leaked")
	require.Nil(t, fx.configRepo.saved)

	skill, _ := fx.skillRepo.GetSkill(context.Background(), 7, "cfg-1", "sk-1")
	require.NotNil(t, skill)
	require.Equal(t, types.SkillStatusReady, skill.Status)
}

// The orphan cleanup must survive the cancellation that produced the orphan:
// the switch fails because the context died, and the delete is a provider call
// on that same context.
func TestRunRemoveDeletesTheOrphanSnapshotAfterTheLockIsLost(t *testing.T) {
	fx := newInstallFixture(t)
	fx.seedInstalledSkill("sk-1", "snap-old", 2)
	fx.seedInstalledSkill("sk-2", "snap-old", 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fx.cancelDuringSnapshot = cancel

	err := fx.svc.runRemove(ctx, 7, "cfg-1", "sk-1")

	require.Error(t, err)
	require.Nil(t, fx.configRepo.saved, "the pointer switch runs on the dead context and fails")
	require.Contains(t, fx.deletedSnapshots, "snap-1",
		"cleaning up on the context that just died leaves a billed snapshot nobody can reach")
	require.Equal(t, []string{"sess-1"}, fx.destroyedSandboxes)
}

// The archive is deleted only once the row that names it is gone. The other
// order leaves a row pointing at an object that no longer exists.
func TestRunRemoveKeepsTheBundleWhenTheRowCannotBeDeleted(t *testing.T) {
	fx := newInstallFixture(t)
	fx.seedInstalledSkill("sk-1", "snap-old", 2)
	fx.seedInstalledSkill("sk-2", "snap-old", 2)
	fx.skillRepo.deleteSkillErr = errUpdateBoom

	err := fx.svc.runRemove(context.Background(), 7, "cfg-1", "sk-1")

	require.Error(t, err)
	require.Equal(t, 3, fx.skillRepo.deleteSkillAttempts,
		"the image already lost the skill, so the row delete is retried, not reported as failed")
	require.Empty(t, fx.deletedBundles,
		"a surviving row must keep pointing at an archive that still exists")

	skill, _ := fx.skillRepo.GetSkill(context.Background(), 7, "cfg-1", "sk-1")
	require.NotNil(t, skill)
}

func TestRunRemoveKeepsTheSkillWhenTheImageStepFails(t *testing.T) {
	fx := newInstallFixture(t)
	fx.seedInstalledSkill("sk-1", "snap-old", 2)
	fx.seedInstalledSkill("sk-2", "snap-old", 2)
	fx.rmExitCode = 1

	err := fx.svc.runRemove(context.Background(), 7, "cfg-1", "sk-1")

	require.Error(t, err)
	require.Nil(t, fx.configRepo.saved)
	require.NotContains(t, fx.events, "create-snapshot")
	require.Equal(t, []string{"sess-1"}, fx.destroyedSandboxes)

	skill, _ := fx.skillRepo.GetSkill(context.Background(), 7, "cfg-1", "sk-1")
	require.NotNil(t, skill, "a failed removal must leave the skill usable, not half-deleted")
	require.Equal(t, types.SkillStatusReady, skill.Status)
	require.NotEmpty(t, skill.Error, "the admin's only diagnostic is this row")
}

// A skill that never reached any image has nothing to restore to, so a failed
// removal must not advertise it as ready.
func TestRunRemoveLeavesANeverInstalledSkillFailed(t *testing.T) {
	fx := newInstallFixture(t)
	fx.seedInstalledSkill("sk-1", "snap-old", 2)
	fx.seedInstalledSkill("sk-2", "snap-old", 2)
	require.NoError(t, fx.svc.updateSkillFields(context.Background(), 7, "cfg-1", "sk-1",
		func(e *types.TenantSkillEntity) {
			e.InstalledSnapshotID = ""
			e.Status = types.SkillStatusRemoving
		}))
	fx.rmExitCode = 1

	require.Error(t, fx.svc.runRemove(context.Background(), 7, "cfg-1", "sk-1"))

	skill, _ := fx.skillRepo.GetSkill(context.Background(), 7, "cfg-1", "sk-1")
	require.Equal(t, types.SkillStatusFailed, skill.Status,
		"calling a skill ready when no image ever carried it sends the agent at missing files")
}

func TestRunRemoveRefusesAnEmptySkillID(t *testing.T) {
	fx := newInstallFixture(t)
	fx.seedInstalledSkill("sk-1", "snap-old", 2)

	err := fx.svc.runRemove(context.Background(), 7, "cfg-1", "")

	require.Error(t, err)
	require.ErrorContains(t, err, "skill id is required")
	require.Empty(t, fx.commands, "the command must not be issued at all")
	require.Nil(t, fx.configRepo.saved)
}

func TestRunRemoveRefusesASkillNameThatCollapsesToTheSkillsRoot(t *testing.T) {
	fx := newInstallFixture(t)
	fx.seedInstalledSkill("sk-1", "snap-old", 2)
	require.NoError(t, fx.svc.updateSkillFields(context.Background(), 7, "cfg-1", "sk-1",
		func(e *types.TenantSkillEntity) { e.Name = "" }))

	err := fx.svc.runRemove(context.Background(), 7, "cfg-1", "sk-1")

	require.Error(t, err,
		"an empty skill name collapses to the skills root, and rm -rf there destroys every skill")
	require.Empty(t, fx.commands, "the command must not be issued at all")
	require.Nil(t, fx.configRepo.saved)
}

func TestRunRemoveStopsWhenTheLockIsLost(t *testing.T) {
	fx := newInstallFixture(t)
	fx.seedInstalledSkill("sk-1", "snap-old", 2)
	fx.seedInstalledSkill("sk-2", "snap-old", 2)
	ctx, cancel := context.WithCancel(context.Background())
	// withConfigLock cancels the run's context the moment lock renewal fails,
	// which happens while the run is under way rather than before it starts.
	fx.cancelDuringRemove = cancel
	defer cancel()

	err := fx.svc.runRemove(ctx, 7, "cfg-1", "sk-1")

	require.Error(t, err)
	require.NotContains(t, fx.events, "create-snapshot",
		"losing the lock means another run may already be writing; do not snapshot")
	require.Nil(t, fx.configRepo.saved)
	require.Equal(t, []string{"sess-1"}, fx.destroyedSandboxes,
		"a sandbox left running on the provider is billed until its TTL expires")

	skill, err := fx.skillRepo.GetSkill(context.Background(), 7, "cfg-1", "sk-1")
	require.NoError(t, err)
	require.Equal(t, types.SkillStatusReady, skill.Status,
		"a row stuck at removing blocks the next attempt and tells the admin nothing")
}

// The compensating work must get a budget that starts when it starts. Every
// fixture removal finishes in microseconds, so a budget anchored at runRemove's
// entry looks fine here and expires on a real (minutes-long) removal.
func TestRunRemoveCleanupSurvivesARemovalLongerThanTheCleanupBudget(t *testing.T) {
	fx := newInstallFixture(t)
	fx.seedInstalledSkill("sk-1", "snap-old", 2)
	fx.seedInstalledSkill("sk-2", "snap-old", 2)
	fx.svc.cleanupTimeout = 50 * time.Millisecond
	fx.removeDelay = 200 * time.Millisecond

	start := time.Now()
	err := fx.svc.runRemove(context.Background(), 7, "cfg-1", "sk-1")
	require.Greater(t, time.Since(start), fx.svc.cleanupTimeout,
		"the removal must outlast the cleanup budget for this test to mean anything")

	require.NoError(t, err)
	require.Equal(t, []string{"sess-1"}, fx.destroyedSandboxes)

	skill, getErr := fx.skillRepo.GetSkill(context.Background(), 7, "cfg-1", "sk-1")
	require.NoError(t, getErr)
	require.Nil(t, skill, "the row must still be dropped after the image lost the skill")
}

func TestRunRemoveDeletesTheSnapshotWhenSwitchFails(t *testing.T) {
	fx := newInstallFixture(t)
	fx.seedInstalledSkill("sk-1", "snap-old", 2)
	fx.seedInstalledSkill("sk-2", "snap-old", 2)
	fx.configRepo.updateErr = errUpdateBoom

	err := fx.svc.runRemove(context.Background(), 7, "cfg-1", "sk-1")

	require.Error(t, err)
	require.Contains(t, fx.deletedSnapshots, "snap-1",
		"a snapshot nobody points at is an orphan; it must be cleaned up here")
	require.NotContains(t, fx.deletedSnapshots, "snap-old",
		"the previous image keeps serving and the ledger still names it")

	skill, _ := fx.skillRepo.GetSkill(context.Background(), 7, "cfg-1", "sk-1")
	require.NotNil(t, skill)
	require.Equal(t, types.SkillStatusReady, skill.Status)
}

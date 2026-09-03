package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

var errInvalidateBoom = errors.New("binding store unavailable")

func TestRunInstallMarksBoundSandboxesStaleAfterThePointerSwitch(t *testing.T) {
	fx := newInstallFixture(t)

	require.NoError(t, fx.svc.runInstall(context.Background(), 7, "cfg-1", "sk-1", fx.bundle))

	require.Equal(t, []staleMark{{tenantID: 7, configID: "cfg-1"}}, fx.staleMarks,
		"an open session would otherwise keep serving the previous image forever")
	require.Less(t,
		indexOfEvent(fx.events, "switch-pointer"), indexOfEvent(fx.events, "mark-stale"),
		"marking before the pointer moved would rebuild sandboxes on the OLD image")
}

func TestRunRemoveMarksBoundSandboxesStaleAfterThePointerSwitch(t *testing.T) {
	fx := newInstallFixture(t)
	fx.seedInstalledSkill("sk-1", "snap-old", 3)
	fx.seedInstalledSkill("sk-2", "snap-old", 3)

	require.NoError(t, fx.svc.runRemove(context.Background(), 7, "cfg-1", "sk-1"))

	require.Equal(t, []staleMark{{tenantID: 7, configID: "cfg-1"}}, fx.staleMarks)
	require.Less(t,
		indexOfEvent(fx.events, "switch-pointer"), indexOfEvent(fx.events, "mark-stale"))
}

// The base-template fallback moves the pointer without taking a snapshot, so
// the image every session boots really did change and the bound sandboxes have
// to rebuild.
func TestRunRemoveMarksBoundSandboxesStaleWhenTheImageFallsBackToTheBaseTemplate(
	t *testing.T,
) {
	fx := newInstallFixture(t)
	fx.seedInstalledSkill("sk-1", "snap-old", 3)

	require.NoError(t, fx.svc.runRemove(context.Background(), 7, "cfg-1", "sk-1"))

	require.Empty(t, fx.configRepo.saved.Config.SkillImage.SnapshotID)
	require.Equal(t, []staleMark{{tenantID: 7, configID: "cfg-1"}}, fx.staleMarks,
		"the pointer moved to the base template, so every bound sandbox boots the wrong image")
}

// Removing a skill that never reached an image changes no image: there is no
// snapshot to grow from and no pointer write happens. Marking here would
// destroy and rebuild every live sandbox of the config - throwing away each
// session's /workspace scratch - for a change no sandbox can observe.
func TestRunRemoveDoesNotMarkSandboxesStaleWhenTheImageNeverHadTheSkill(t *testing.T) {
	fx := newInstallFixture(t)
	// The row has to be the one this run owns, the way RemoveSkill leaves it
	// before queueing the run. Without that, removeStillOwnsTheRow reads a row
	// still marked installing and declines to touch a newer upload's work.
	require.NoError(t, fx.svc.updateSkillFields(context.Background(), 7, "cfg-1", "sk-1",
		func(e *types.TenantSkillEntity) { e.Status = types.SkillStatusRemoving }))

	require.NoError(t, fx.svc.runRemove(context.Background(), 7, "cfg-1", "sk-1"))

	skill, err := fx.skillRepo.GetSkill(context.Background(), 7, "cfg-1", "sk-1")
	require.NoError(t, err)
	require.Nil(t, skill, "the removal still completes: the row and its bundle go")
	require.Empty(t, fx.staleMarks,
		"no pointer write happened, so no live sandbox is out of date")
}

// The pointer has already moved by the time marking runs. A binding that could
// not be marked is a session serving a stale image until it ends - an
// annoyance, not a corruption - so it must never turn a finished install into a
// failed one.
func TestMarkingStaleSandboxesFailureLeavesTheInstallSuccessful(t *testing.T) {
	fx := newInstallFixture(t)
	fx.invalidateErr = errInvalidateBoom

	require.NoError(t, fx.svc.runInstall(context.Background(), 7, "cfg-1", "sk-1", fx.bundle))

	skill, err := fx.skillRepo.GetSkill(context.Background(), 7, "cfg-1", "sk-1")
	require.NoError(t, err)
	require.Equal(t, types.SkillStatusReady, skill.Status)
	require.Empty(t, skill.Error)
	require.Equal(t, "snap-1", fx.configRepo.saved.Config.SkillImage.SnapshotID,
		"the image the install produced stays the one every new session boots")
}

// Marking runs past the point of no return, so it must not inherit the
// cancellation of whatever it compensates for: withConfigLock cancels the
// install context the moment lock renewal fails, and the binding store's writes
// are ordinary Redis calls that a dead context fails.
func TestMarkConfigSandboxesStaleRunsOnADetachedContext(t *testing.T) {
	fx := newInstallFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	fx.svc.markConfigSandboxesStale(ctx, 7, "cfg-1")

	require.Equal(t, []staleMark{{tenantID: 7, configID: "cfg-1"}}, fx.staleMarks,
		"the sandbox fake refuses a cancelled context, exactly as Redis would")
}

func TestMarkConfigSandboxesStaleSkipsWhenRolloutIsNewSession(t *testing.T) {
	fx := newInstallFixture(t)
	fx.configRepo.entity.Config.SkillRollout = types.SkillRolloutNewSession

	require.NoError(t, fx.svc.runInstall(context.Background(), 7, "cfg-1", "sk-1", fx.bundle))

	require.Empty(t, fx.staleMarks,
		"new_session rollout must not rebuild sandboxes already bound to a session")
	require.Equal(t, "snap-1", fx.configRepo.saved.Config.SkillImage.SnapshotID,
		"new sessions still boot the image this install produced")
}

func TestRunRemoveSkipsStaleMarkWhenRolloutIsNewSession(t *testing.T) {
	fx := newInstallFixture(t)
	fx.configRepo.entity.Config.SkillRollout = types.SkillRolloutNewSession
	fx.seedInstalledSkill("sk-1", "snap-old", 3)
	fx.seedInstalledSkill("sk-2", "snap-old", 3)

	require.NoError(t, fx.svc.runRemove(context.Background(), 7, "cfg-1", "sk-1"))

	require.Empty(t, fx.staleMarks)
	require.NotEmpty(t, fx.configRepo.saved.Config.SkillImage.SnapshotID)
}

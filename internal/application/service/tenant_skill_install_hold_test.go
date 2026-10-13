package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

type heldInstallMarker struct{}

func isHeld(ctx context.Context) bool {
	return ctx != nil && ctx.Value(heldInstallMarker{}) != nil
}

// holdingInstallManager is the install fake plus the hold capability, so the
// rest of the suite keeps exercising backends that cannot hold a sandbox.
type holdingInstallManager struct {
	*installSandboxManager
	holdErr      error
	heldCommands []bool
	snapshotHeld bool
}

func (m *holdingInstallManager) HoldSessionSandbox(
	ctx context.Context, sessionID string,
) (context.Context, func(), error) {
	m.fx.record("hold-sandbox")
	if m.holdErr != nil {
		return nil, nil, m.holdErr
	}
	return context.WithValue(ctx, heldInstallMarker{}, sessionID), func() { m.fx.record("release-hold") }, nil
}

func (m *holdingInstallManager) SessionShellExecutor() sandbox.SessionShellExecutor { return m }

func (m *holdingInstallManager) SessionInstallShellExecutor() sandbox.SessionInstallShellExecutor {
	return m
}

func (m *holdingInstallManager) ExecShellCommand(
	ctx context.Context, sessionID, command, workDir string, timeout time.Duration, env map[string]string,
) (*sandbox.ExecuteResult, error) {
	return m.ExecShellCommandWithOptions(ctx, sessionID, command, sandbox.ShellExecOptions{
		WorkDir: workDir, Timeout: timeout, Env: env,
	})
}

func (m *holdingInstallManager) ExecShellCommandWithOptions(
	ctx context.Context, sessionID, command string, opts sandbox.ShellExecOptions,
) (*sandbox.ExecuteResult, error) {
	m.heldCommands = append(m.heldCommands, isHeld(ctx))
	return m.installSandboxManager.ExecShellCommandWithOptions(ctx, sessionID, command, opts)
}

func (m *holdingInstallManager) CreateSnapshot(
	ctx context.Context, sessionID, name string,
) (sandbox.RemoteSnapshotRef, error) {
	m.snapshotHeld = isHeld(ctx)
	return m.installSandboxManager.CreateSnapshot(ctx, sessionID, name)
}

func newHoldingInstallFixture(t *testing.T) (*installFixture, *holdingInstallManager) {
	t.Helper()
	fx := newInstallFixture(t)
	mgr := &holdingInstallManager{installSandboxManager: fx.sandboxMgr}
	fx.svc.sandboxes = &installSandboxResolver{mgr: mgr}
	return fx, mgr
}

func TestRunInstallHoldsTheSandboxFromSeedToSnapshot(t *testing.T) {
	fx, mgr := newHoldingInstallFixture(t)

	require.NoError(t, fx.svc.runInstall(context.Background(), 7, "cfg-1", "sk-1", fx.bundle))

	require.Less(t, indexOfEvent(fx.events, "prepare-skill-dir"), indexOfEvent(fx.events, "hold-sandbox"),
		"the sandbox exists only once the first command has run")
	require.Less(t, indexOfEvent(fx.events, "hold-sandbox"), indexOfEvent(fx.events, "seed-files"))
	require.Less(t, indexOfEvent(fx.events, "create-snapshot"), indexOfEvent(fx.events, "release-hold"))
	require.Less(t, indexOfEvent(fx.events, "release-hold"), indexOfEvent(fx.events, "destroy-sandbox"))

	require.NotEmpty(t, mgr.heldCommands)
	require.False(t, mgr.heldCommands[0], "the directory reset is what provisions the sandbox")
	for i, held := range mgr.heldCommands[1:] {
		require.True(t, held, "command %d ran outside the held sandbox: %s", i+1, fx.commands[i+1])
	}
	require.True(t, isHeld(fx.agentCtx), "the installer agent must work in the held sandbox")
	require.True(t, mgr.snapshotHeld, "the snapshot must be taken of the held sandbox")
}

func TestRunInstallFailsWhenTheSandboxCannotBeHeld(t *testing.T) {
	fx, mgr := newHoldingInstallFixture(t)
	mgr.holdErr = errors.New("no live sandbox for session sess-1")

	err := fx.svc.runInstall(context.Background(), 7, "cfg-1", "sk-1", fx.bundle)

	require.ErrorContains(t, err, "no live sandbox for session sess-1")
	require.NotContains(t, fx.events, "seed-files")
	require.NotContains(t, fx.events, "create-snapshot")
	skill, _ := fx.skillRepo.GetSkill(context.Background(), 7, "cfg-1", "sk-1")
	require.Equal(t, types.SkillStatusFailed, skill.Status)
}

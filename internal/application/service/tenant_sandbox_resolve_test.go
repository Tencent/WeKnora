package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

type stubHostManager struct{}

func (stubHostManager) Execute(context.Context, *sandbox.ExecuteConfig) (*sandbox.ExecuteResult, error) {
	return nil, nil
}
func (stubHostManager) Cleanup(context.Context) error { return nil }
func (stubHostManager) GetSandbox() sandbox.Sandbox   { return nil }
func (stubHostManager) GetType() sandbox.SandboxType  { return sandbox.SandboxTypeHost }

type fakeResolver struct {
	typ sandbox.SandboxType
}

func (f fakeResolver) Resolve(context.Context, uint64, string) (sandbox.Manager, error) {
	return &pinTestManager{typ: f.typ}, nil
}

type disabledPolicy struct{}

func (disabledPolicy) WorkspaceScriptsDisabled(context.Context, uint64) (bool, error) {
	return true, nil
}

// Web and Lite share this helper. An empty config is "no remote sandbox", not
// "use whatever host manager happens to be in process".
func TestResolveTenantSandboxEmptyConfigIsDisabledEvenWithHostPresent(t *testing.T) {
	mgr, err := resolveTenantSandboxForConfig(
		context.Background(), nil, stubHostManager{}, 1, "", nil,
	)
	require.NoError(t, err)
	require.Equal(t, sandbox.SandboxTypeDisabled, mgr.GetType())
}

func TestResolveTenantSandboxGlobalDefaultIsDisabledEvenWithHostPresent(t *testing.T) {
	mgr, err := resolveTenantSandboxForConfig(
		context.Background(), nil, stubHostManager{}, 1,
		types.SandboxConfigIDGlobalDefault, nil,
	)
	require.NoError(t, err)
	require.Equal(t, sandbox.SandboxTypeDisabled, mgr.GetType())
}

func TestResolveNamedConfigIgnoresLiteHost(t *testing.T) {
	mgr, err := resolveTenantSandboxForConfig(
		context.Background(), fakeResolver{typ: sandbox.SandboxTypeE2B}, stubHostManager{},
		1, "cfg-1", nil,
	)
	require.NoError(t, err)
	require.Equal(t, sandbox.SandboxTypeE2B, mgr.GetType())
}

func TestResolveWithoutLiteHostStaysDisabled(t *testing.T) {
	mgr, err := resolveTenantSandboxForConfig(
		context.Background(), nil, nil, 1, "", nil,
	)
	require.NoError(t, err)
	require.Equal(t, sandbox.SandboxTypeDisabled, mgr.GetType())
}

func TestResolveKillSwitchBeatsNamedConfig(t *testing.T) {
	mgr, err := resolveTenantSandboxForConfig(
		context.Background(), fakeResolver{typ: sandbox.SandboxTypeE2B}, stubHostManager{},
		1, "cfg-1", disabledPolicy{},
	)
	require.NoError(t, err)
	require.Equal(t, sandbox.SandboxTypeDisabled, mgr.GetType())
}

// Lite opts into host by passing withLiteHostSandbox. Web never passes a
// host-typed manager (the stub provider is nil), so the same empty config
// stays disabled.
func TestResolveSandboxForExecutionUsesLiteHostWhenUnconfigured(t *testing.T) {
	pinner := NewSessionSandboxPinner(newPinTestDB(t))
	host := stubHostManager{}

	got, pin, err := resolveSandboxForExecution(
		context.Background(), nil, nil, pinner,
		7, "s-1", "", nil, withLiteHostSandbox(host),
	)

	require.NoError(t, err)
	require.Equal(t, sandbox.SandboxTypeHost, got.GetType())
	require.True(t, pin.IsZero(), "host has no named config to pin")
	pinned, err := pinner.Read(context.Background(), "s-1")
	require.NoError(t, err)
	require.True(t, pinned.IsZero(), "host has no session-scoped instance to pin")
}

func TestResolveSandboxForExecutionUnconfiguredWithoutLiteHostStaysDisabled(t *testing.T) {
	got, pin, err := resolveSandboxForExecution(
		context.Background(), nil, stubHostManager{}, nil,
		7, "s-1", "", nil,
	)
	require.NoError(t, err)
	require.Equal(t, sandbox.SandboxTypeDisabled, got.GetType())
	require.True(t, pin.IsZero())
}

func TestResolveSandboxForExecutionNamedConfigDoesNotUseLiteHost(t *testing.T) {
	pinner := NewSessionSandboxPinner(newPinTestDB(t))
	got, pin, err := resolveSandboxForExecution(
		context.Background(), fakeResolver{typ: sandbox.SandboxTypeE2B}, nil, pinner,
		7, "s-1", "cfg-1", nil, withLiteHostSandbox(stubHostManager{}),
	)
	require.NoError(t, err)
	require.Equal(t, sandbox.SandboxTypeE2B, got.GetType())
	require.Equal(t, "cfg-1", pin.ConfigID)
}

func TestResolveSandboxForExecutionKillSwitchBeatsLiteHost(t *testing.T) {
	got, pin, err := resolveSandboxForExecution(
		context.Background(), nil, nil, nil,
		7, "s-1", "", disabledPolicy{}, withLiteHostSandbox(stubHostManager{}),
	)
	require.NoError(t, err)
	require.Equal(t, sandbox.SandboxTypeDisabled, got.GetType())
	require.True(t, pin.IsZero())
}

func TestResolveOnLiteDesktopIgnoresNamedConfig(t *testing.T) {
	mgr, pin, err := resolveSandboxForExecution(
		context.Background(), nil, nil, nil, 7, "s1", "cfg-remote", nil,
		withLiteHostSandbox(stubHostManager{}), withLiteDesktop(true),
	)
	require.NoError(t, err)
	require.Equal(t, sandbox.SandboxTypeHost, mgr.GetType())
	require.True(t, pin.IsZero())
}

func TestResolveOnLiteDesktopWithoutHostIsDisabled(t *testing.T) {
	mgr, _, err := resolveSandboxForExecution(
		context.Background(), nil, nil, nil, 7, "s1", "cfg-remote", nil,
		withLiteDesktop(true),
	)
	require.NoError(t, err)
	require.Equal(t, sandbox.SandboxTypeDisabled, mgr.GetType())
}

func TestResolveOnLiteDesktopSendsInstallerToInstallSandbox(t *testing.T) {
	installer := &capableManager{typ: sandbox.SandboxTypeHost}
	mgr, _, err := resolveSandboxForExecution(
		context.Background(), nil, nil, nil, 7, "install-1", sandbox.HostSkillTargetID, nil,
		withLiteHostSandbox(stubHostManager{}), withLiteDesktop(true), withHostSkillInstaller(installer),
	)
	require.NoError(t, err)
	require.Same(t, installer, mgr)
}

func TestResolveHostTargetWithoutInstallerIsTheChatHost(t *testing.T) {
	mgr, _, err := resolveSandboxForExecution(
		context.Background(), nil, nil, nil, 7, "s1", sandbox.HostSkillTargetID, nil,
		withLiteHostSandbox(stubHostManager{}), withLiteDesktop(true),
	)
	require.NoError(t, err)
	require.Equal(t, stubHostManager{}, mgr)
}

func TestHasNamedSandboxConfigExcludesHostTarget(t *testing.T) {
	require.False(t, hasNamedSandboxConfig(sandbox.HostSkillTargetID))
	require.True(t, hasNamedSandboxConfig("cfg-1"))
}

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type stubPinReader struct {
	configID string
	// tenantID is the workspace owning configID; zero keeps the "the session's
	// own workspace" fallback that every non-borrowed pin uses.
	tenantID uint64
	err      error
}

func (s stubPinReader) Read(context.Context, string) (SandboxPin, error) {
	return SandboxPin{ConfigID: s.configID, TenantID: s.tenantID}, s.err
}

type stubTenantSandboxResolver struct {
	mgr     sandbox.Manager
	err     error
	calls   int
	lastCfg string
}

func (s *stubTenantSandboxResolver) Resolve(_ context.Context, _ uint64, configID string) (sandbox.Manager, error) {
	s.calls++
	s.lastCfg = configID
	return s.mgr, s.err
}

type stubPinnedManager struct {
	id      string
	ok      bool
	execs   int
	execErr error
	execOut *sandbox.ExecuteResult
}

func (m *stubPinnedManager) Execute(context.Context, *sandbox.ExecuteConfig) (*sandbox.ExecuteResult, error) {
	return nil, nil
}
func (m *stubPinnedManager) Cleanup(context.Context) error { return nil }
func (m *stubPinnedManager) GetSandbox() sandbox.Sandbox   { return nil }
func (m *stubPinnedManager) GetType() sandbox.SandboxType  { return sandbox.SandboxTypeCube }

func (m *stubPinnedManager) BoundSandboxID(context.Context, string) (string, bool) {
	return m.id, m.ok
}

func (m *stubPinnedManager) ExecShellCommand(
	_ context.Context, _ string, _, _ string, _ time.Duration, _ map[string]string,
) (*sandbox.ExecuteResult, error) {
	m.execs++
	if m.execErr != nil {
		return nil, m.execErr
	}
	return m.execOut, nil
}

func TestPinnedSessionSandboxBoundSandboxIDUsesPinnedManager(t *testing.T) {
	mgr := &stubPinnedManager{id: "sbx-1", ok: true}
	resolver := &stubTenantSandboxResolver{mgr: mgr}
	access := NewPinnedSessionSandbox(stubPinReader{configID: "cfg-1"}, resolver, nil, nil)

	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(9))
	id, ok := access.BoundSandboxID(ctx, "sess-1")

	require.True(t, ok)
	require.Equal(t, "sbx-1", id)
	require.Equal(t, 1, resolver.calls)
	require.Equal(t, "cfg-1", resolver.lastCfg)
	require.Zero(t, mgr.execs)
}

func TestPinnedSessionSandboxBoundSandboxIDSkipsWhenUnpinned(t *testing.T) {
	resolver := &stubTenantSandboxResolver{mgr: &stubPinnedManager{id: "sbx-1", ok: true}}
	access := NewPinnedSessionSandbox(stubPinReader{}, resolver, nil, nil)

	id, ok := access.BoundSandboxID(context.Background(), "sess-1")

	require.False(t, ok)
	require.Empty(t, id)
	require.Zero(t, resolver.calls)
}

type stubForkPinnedManager struct {
	stubPinnedManager
	busy       bool
	busyErr    error
	snapshotID string
	snapErr    error
	snapCalls  int
	snapName   string
}

func (m *stubForkPinnedManager) HasActiveTurn(context.Context, string) (bool, error) {
	return m.busy, m.busyErr
}

func (m *stubForkPinnedManager) CreateForkSnapshot(_ context.Context, _, name string) (string, error) {
	m.snapCalls++
	m.snapName = name
	return m.snapshotID, m.snapErr
}

func TestPinnedSessionSandboxHasActiveTurnDelegates(t *testing.T) {
	mgr := &stubForkPinnedManager{busy: true}
	access := NewPinnedSessionSandbox(
		stubPinReader{configID: "cfg-1"},
		&stubTenantSandboxResolver{mgr: mgr},
		nil,
		nil,
	)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(9))

	busy, err := access.HasActiveTurn(ctx, "sess-1")
	require.NoError(t, err)
	require.True(t, busy)
}

func TestPinnedSessionSandboxHasActiveTurnWhenManagerLacksMethod(t *testing.T) {
	access := NewPinnedSessionSandbox(
		stubPinReader{configID: "cfg-1"},
		&stubTenantSandboxResolver{mgr: &stubPinnedManager{id: "sbx-1", ok: true}},
		nil,
		nil,
	)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(9))

	busy, err := access.HasActiveTurn(ctx, "sess-1")
	require.NoError(t, err)
	require.False(t, busy)
}

func TestPinnedSessionSandboxCreateForkSnapshotDelegates(t *testing.T) {
	mgr := &stubForkPinnedManager{snapshotID: "snap-9"}
	access := NewPinnedSessionSandbox(
		stubPinReader{configID: "cfg-1"},
		&stubTenantSandboxResolver{mgr: mgr},
		nil,
		nil,
	)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(9))

	id, err := access.CreateForkSnapshot(ctx, "sess-1", "fork-name")
	require.NoError(t, err)
	require.Equal(t, "snap-9", id)
	require.Equal(t, 1, mgr.snapCalls)
	require.Equal(t, "fork-name", mgr.snapName)
}

func TestPinnedSessionSandboxCreateForkSnapshotErrorsWhenUnsupported(t *testing.T) {
	access := NewPinnedSessionSandbox(
		stubPinReader{configID: "cfg-1"},
		&stubTenantSandboxResolver{mgr: &stubPinnedManager{id: "sbx-1", ok: true}},
		nil,
		nil,
	)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(9))

	_, err := access.CreateForkSnapshot(ctx, "sess-1", "fork-name")
	require.Error(t, err)
}

type stubHostPinnedManager struct {
	stubPinnedManager
}

func (m *stubHostPinnedManager) GetType() sandbox.SandboxType { return sandbox.SandboxTypeHost }

func TestPinnedSessionSandboxVersionsWorkspaceFalseWhenHostResolved(t *testing.T) {
	host := &stubHostPinnedManager{}
	access := NewPinnedSessionSandbox(
		stubPinReader{},
		&stubTenantSandboxResolver{mgr: &stubPinnedManager{id: "sbx-1", ok: true}},
		nil,
		NewHostSessionResolver(stubPinReader{}, host, false),
	)

	require.False(t, access.VersionsWorkspace(context.Background(), "sess-1"))
	_, err := access.ExecShellCommand(context.Background(), "sess-1", "true", "", time.Second, nil)
	require.Error(t, err, "manager() must not fall back to host")
	require.Zero(t, host.execs, "must not hand ExecShellCommand a live host runner")
}

func TestPinnedSessionSandboxVersionsWorkspaceTrueWithoutPinOrHost(t *testing.T) {
	access := NewPinnedSessionSandbox(stubPinReader{}, &stubTenantSandboxResolver{}, nil, nil)

	require.True(t, access.VersionsWorkspace(context.Background(), "sess-1"),
		"without host, fork must keep the remote degrade chain rather than pretend this is host")
}

func TestPinnedSessionSandboxVersionsWorkspaceFalseWhenManagerTypeIsHost(t *testing.T) {
	mgr := &stubHostPinnedManager{}
	access := NewPinnedSessionSandbox(
		stubPinReader{configID: "cfg-1"},
		&stubTenantSandboxResolver{mgr: mgr},
		nil,
		nil,
	)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(9))

	require.False(t, access.VersionsWorkspace(ctx, "sess-1"))
}

func TestHostManagerForReturnsHostWhenSessionHasNoNamedConfig(t *testing.T) {
	host := stubHostManager{}
	r := NewHostSessionResolver(stubPinReader{}, host, false)

	require.Equal(t, host, r.HostManagerFor(context.Background(), "s1"))
}

func TestHostManagerForNilWhenSessionHasNamedConfig(t *testing.T) {
	r := NewHostSessionResolver(stubPinReader{configID: "cfg-1"}, stubHostManager{}, false)

	require.Nil(t, r.HostManagerFor(context.Background(), "s1"))
}

func TestHostManagerForNilWhenHostMissing(t *testing.T) {
	r := NewHostSessionResolver(stubPinReader{}, nil, false)

	require.Nil(t, r.HostManagerFor(context.Background(), "s1"))
}

func TestHostManagerForTreatsGlobalDefaultAsUnnamed(t *testing.T) {
	host := stubHostManager{}
	r := NewHostSessionResolver(stubPinReader{configID: types.SandboxConfigIDGlobalDefault}, host, false)

	require.Equal(t, host, r.HostManagerFor(context.Background(), "s1"))
}

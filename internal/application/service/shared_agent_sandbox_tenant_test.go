package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

// A shared agent runs in ITS OWNER's workspace, so its sandbox is created on
// that workspace's config. Everything that touches the sandbox outside the
// chat turn — session teardown, the terminal and desktop panels, fork
// snapshots — runs from a plain request whose only tenant is the session
// owner's. Resolving a lent config there finds nothing, because
// tenant_sandbox_configs is keyed by (tenant_id, id).
//
// These tests pin the fix: the workspace travels with the config id.
const (
	borrowerTenant    = uint64(7)  // owns the session
	lenderTenant      = uint64(99) // owns the shared agent and its sandbox config
	lentSandboxConfig = "cfg-owned-by-lender"
)

// lendingResolver mirrors tenantSandboxResolver.Resolve: a config resolves in
// exactly one workspace and is "not found" anywhere else.
type lendingResolver struct {
	mgr        sandbox.Manager
	lastTenant uint64
	lastConfig string
}

func (r *lendingResolver) Resolve(
	_ context.Context, tenantID uint64, configID string,
) (sandbox.Manager, error) {
	r.lastTenant, r.lastConfig = tenantID, configID
	if tenantID != lenderTenant || configID != lentSandboxConfig {
		return nil, fmt.Errorf("%w: %s", sandbox.ErrSandboxConfigNotFound, configID)
	}
	return r.mgr, nil
}

// destroyRecordingManager records the sessions it was asked to tear down.
type destroyRecordingManager struct {
	destroyed []string
	bound     string
}

func (m *destroyRecordingManager) Execute(
	context.Context, *sandbox.ExecuteConfig,
) (*sandbox.ExecuteResult, error) {
	return nil, nil
}
func (m *destroyRecordingManager) Cleanup(context.Context) error { return nil }
func (m *destroyRecordingManager) GetSandbox() sandbox.Sandbox   { return nil }
func (m *destroyRecordingManager) GetType() sandbox.SandboxType  { return sandbox.SandboxTypeCube }

func (m *destroyRecordingManager) DestroySession(_ context.Context, sessionID string) error {
	m.destroyed = append(m.destroyed, sessionID)
	return nil
}

func (m *destroyRecordingManager) BoundSandboxID(context.Context, string) (string, bool) {
	return m.bound, m.bound != ""
}

func borrowedSessionDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	_ = db.Migrator().DropTable(&types.Session{})
	require.NoError(t, db.AutoMigrate(&types.Session{}))
	require.NoError(t, db.Model(&types.Session{}).Create(map[string]any{
		"id": "s-1", "tenant_id": borrowerTenant,
	}).Error)
	return db
}

// borrowerCtx is what a DELETE /sessions/:id or a panel-open handshake carries:
// the session owner, with no trace of the workspace that lent the agent.
func borrowerCtx() context.Context {
	return context.WithValue(context.Background(), types.TenantIDContextKey, borrowerTenant)
}

// Session sandboxes are created with onTimeout=pause on Cube and E2B, so a
// teardown that resolves nothing does not merely log a warning: it abandons a
// paused MicroVM that keeps billing with nobody holding its id.
func TestDestroyBoundSandboxTearsDownALentWorkspacesSandbox(t *testing.T) {
	pinner := NewSessionSandboxPinner(borrowedSessionDB(t))
	_, err := pinner.Pin(context.Background(), "s-1",
		SandboxPin{ConfigID: lentSandboxConfig, TenantID: lenderTenant})
	require.NoError(t, err)

	mgr := &destroyRecordingManager{}
	resolver := &lendingResolver{mgr: mgr}
	svc := &sessionService{sandboxPinner: pinner, sandboxResolver: resolver}

	svc.destroyBoundSandbox(borrowerCtx(), "s-1")

	require.Equal(t, []string{"s-1"}, mgr.destroyed)
	require.Equal(t, lenderTenant, resolver.lastTenant,
		"teardown must look the config up where it exists")

	pin, err := pinner.Read(context.Background(), "s-1")
	require.NoError(t, err)
	require.True(t, pin.IsZero(), "a destroyed sandbox releases its pin")
}

// An own agent's pin carries no workspace, so teardown keeps resolving it in
// the session's own — unchanged behaviour, and what every pre-existing row
// falls back to after the migration.
func TestDestroyBoundSandboxStillUsesTheSessionWorkspaceForOwnAgents(t *testing.T) {
	pinner := NewSessionSandboxPinner(borrowedSessionDB(t))
	_, err := pinner.Pin(context.Background(), "s-1", SandboxPin{ConfigID: "cfg-local"})
	require.NoError(t, err)

	mgr := &destroyRecordingManager{}
	resolver := &tenantRecordingResolver{mgr: mgr}
	svc := &sessionService{sandboxPinner: pinner, sandboxResolver: resolver}

	svc.destroyBoundSandbox(borrowerCtx(), "s-1")

	require.Equal(t, []string{"s-1"}, mgr.destroyed)
	require.Equal(t, borrowerTenant, resolver.lastTenant)
	require.Equal(t, "cfg-local", resolver.lastConfig)
}

// Attaching to an existing sandbox: the panel open is a GET as the session
// owner, and the pin is the only thing that knows where the sandbox lives.
func TestTerminalAttachResolvesTheLentWorkspace(t *testing.T) {
	pinner := NewSessionSandboxPinner(borrowedSessionDB(t))
	_, err := pinner.Pin(context.Background(), "s-1",
		SandboxPin{ConfigID: lentSandboxConfig, TenantID: lenderTenant})
	require.NoError(t, err)

	mgr := &destroyRecordingManager{}
	resolver := &lendingResolver{mgr: mgr}
	svc := NewSandboxTerminalService(pinner, resolver, nil, nil, HostSandboxManager{})

	got, pin, err := svc.resolveSessionManager(borrowerCtx(), "s-1")

	require.NoError(t, err)
	require.Same(t, mgr, got)
	require.Equal(t, SandboxPin{ConfigID: lentSandboxConfig, TenantID: lenderTenant}, pin)
	require.Equal(t, lenderTenant, resolver.lastTenant)
}

// Creating the first sandbox from the panel: there is no pin yet, so the
// workspace has to arrive with the agent's config from the WebSocket handler.
func TestTerminalProvisionUsesTheAgentsWorkspace(t *testing.T) {
	pinner := NewSessionSandboxPinner(borrowedSessionDB(t))
	resolver := &lendingResolver{mgr: &destroyRecordingManager{}}
	svc := NewSandboxTerminalService(pinner, resolver, nil, nil, HostSandboxManager{})

	// The PTY open that follows fails for this fake manager; what matters here
	// is that resolution and the pin claim happened in the lending workspace.
	_, _ = svc.EnsureSessionTerminal(borrowerCtx(), "s-1",
		SandboxPin{ConfigID: lentSandboxConfig, TenantID: lenderTenant},
		sandbox.RemoteTerminalOptions{})

	require.Equal(t, lenderTenant, resolver.lastTenant)
	require.Equal(t, lentSandboxConfig, resolver.lastConfig)

	pin, err := pinner.Read(context.Background(), "s-1")
	require.NoError(t, err)
	require.Equal(t, SandboxPin{ConfigID: lentSandboxConfig, TenantID: lenderTenant}, pin,
		"a panel-created sandbox records its workspace like a chat turn does")
}

// Lite never resolves a remote sandbox, whatever the session pinned before
// the upgrade or the panel asks to provision.
func TestTerminalOnLiteIgnoresRemotePinsAndConfigs(t *testing.T) {
	pinner := NewSessionSandboxPinner(borrowedSessionDB(t))
	_, err := pinner.Pin(context.Background(), "s-1",
		SandboxPin{ConfigID: lentSandboxConfig, TenantID: lenderTenant})
	require.NoError(t, err)
	resolver := &lendingResolver{mgr: &destroyRecordingManager{}}
	svc := NewSandboxTerminalService(pinner, resolver, nil, nil, HostSandboxManager{Desktop: true})

	_, err = svc.EnsureSessionTerminal(borrowerCtx(), "s-1",
		SandboxPin{ConfigID: lentSandboxConfig, TenantID: lenderTenant},
		sandbox.RemoteTerminalOptions{})

	require.ErrorIs(t, err, sandbox.ErrNoLiveSessionSandbox)
	require.Zero(t, resolver.lastTenant, "no remote config may be resolved on Lite")
}

// A zero provision pin stays lookup-only: opening a panel must never create
// infrastructure on its own.
func TestTerminalProvisionStaysLookupOnlyWithoutAPin(t *testing.T) {
	pinner := NewSessionSandboxPinner(borrowedSessionDB(t))
	resolver := &lendingResolver{mgr: &destroyRecordingManager{}}
	svc := NewSandboxTerminalService(pinner, resolver, nil, nil, HostSandboxManager{})

	_, err := svc.EnsureSessionTerminal(
		borrowerCtx(), "s-1", SandboxPin{}, sandbox.RemoteTerminalOptions{})

	require.ErrorIs(t, err, sandbox.ErrNoLiveSessionSandbox)
	require.Zero(t, resolver.lastTenant, "no config means nothing to resolve")
}

// Fork snapshots go through PinnedSessionSandbox, which runs from a plain POST
// as the session owner. Resolving the lent config there used to return nil,
// so BoundSandboxID failed and the fork degraded to SANDBOX_GONE.
func TestPinnedSessionSandboxResolvesTheLentWorkspace(t *testing.T) {
	mgr := &destroyRecordingManager{bound: "sbx-1"}
	resolver := &lendingResolver{mgr: mgr}
	access := NewPinnedSessionSandbox(
		stubPinReader{configID: lentSandboxConfig, tenantID: lenderTenant}, resolver, nil, nil)

	id, ok := access.BoundSandboxID(borrowerCtx(), "s-1")

	require.True(t, ok)
	require.Equal(t, "sbx-1", id)
	require.Equal(t, lenderTenant, resolver.lastTenant)
}

// The fork snapshot lease is reaped by (tenant, config) as a pair, so the
// session has to report the workspace that owns its config, not its own.
func TestSessionSandboxConfigOwnerPrefersTheRecordedWorkspace(t *testing.T) {
	borrowed := &types.Session{TenantID: borrowerTenant, SandboxConfigTenantID: lenderTenant}
	require.Equal(t, lenderTenant, borrowed.SandboxConfigOwner())

	own := &types.Session{TenantID: borrowerTenant}
	require.Equal(t, borrowerTenant, own.SandboxConfigOwner(),
		"an own agent's config lives in the session's own workspace")

	require.Zero(t, (*types.Session)(nil).SandboxConfigOwner())
}

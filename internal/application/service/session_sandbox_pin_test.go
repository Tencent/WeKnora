package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

type pinTestManager struct {
	typ sandbox.SandboxType
}

func (m *pinTestManager) Execute(context.Context, *sandbox.ExecuteConfig) (*sandbox.ExecuteResult, error) {
	return nil, nil
}
func (m *pinTestManager) Cleanup(context.Context) error { return nil }
func (m *pinTestManager) GetSandbox() sandbox.Sandbox   { return nil }
func (m *pinTestManager) GetType() sandbox.SandboxType  { return m.typ }

func newPinTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Migrator().DropTable(&types.Session{}))
	require.NoError(t, db.AutoMigrate(&types.Session{}))
	require.NoError(t, db.Model(&types.Session{}).Create(map[string]any{
		"id":        "s-1",
		"tenant_id": uint64(7),
	}).Error)
	return db
}

func TestPinWritesWhenUnset(t *testing.T) {
	pinner := NewSessionSandboxPinner(newPinTestDB(t))
	ctx := context.Background()

	got, err := pinner.Pin(ctx, "s-1", SandboxPin{ConfigID: "cfg-a"})
	require.NoError(t, err)
	require.Equal(t, SandboxPin{ConfigID: "cfg-a"}, got)

	read, err := pinner.Read(ctx, "s-1")
	require.NoError(t, err)
	require.Equal(t, SandboxPin{ConfigID: "cfg-a"}, read)
}

// Two concurrent first-sandbox creations must converge on one config, or the
// session would end up with two sandboxes on two backends.
func TestPinIsIdempotentAndReturnsExistingWinner(t *testing.T) {
	pinner := NewSessionSandboxPinner(newPinTestDB(t))
	ctx := context.Background()

	first, err := pinner.Pin(ctx, "s-1", SandboxPin{ConfigID: "cfg-a"})
	require.NoError(t, err)
	require.Equal(t, SandboxPin{ConfigID: "cfg-a"}, first)

	second, err := pinner.Pin(ctx, "s-1", SandboxPin{ConfigID: "cfg-b"})
	require.NoError(t, err)
	require.Equal(t, SandboxPin{ConfigID: "cfg-a"}, second, "the first writer wins; later callers adopt it")
}

func TestPinLeavesEmptyConfigUnpinned(t *testing.T) {
	pinner := NewSessionSandboxPinner(newPinTestDB(t))
	ctx := context.Background()

	got, err := pinner.Pin(ctx, "s-1", SandboxPin{ConfigID: ""})
	require.NoError(t, err)
	require.Empty(t, got)
}

// A padded ID must land in the column exactly as Read will compare it, or the
// conditional claim would never recognise its own write.
func TestPinTrimsConfigIDBeforeStoring(t *testing.T) {
	pinner := NewSessionSandboxPinner(newPinTestDB(t))
	ctx := context.Background()

	got, err := pinner.Pin(ctx, "s-1", SandboxPin{ConfigID: "  cfg-a  "})
	require.NoError(t, err)
	require.Equal(t, SandboxPin{ConfigID: "cfg-a"}, got)

	read, err := pinner.Read(ctx, "s-1")
	require.NoError(t, err)
	require.Equal(t, SandboxPin{ConfigID: "cfg-a"}, read)
}

// Pin runs right after a sandbox was created, so a vanished session must be an
// error: "" would read as "no live sandbox" and abandon a real one.
func TestPinFailsWhenSessionIsGone(t *testing.T) {
	db := newPinTestDB(t)
	pinner := NewSessionSandboxPinner(db)
	ctx := context.Background()

	_, err := pinner.Pin(ctx, "missing", SandboxPin{ConfigID: "cfg-a"})
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)

	require.NoError(t, db.Delete(&types.Session{}, "id = ?", "s-1").Error)
	_, err = pinner.Pin(ctx, "s-1", SandboxPin{ConfigID: "cfg-a"})
	require.ErrorIs(t, err, gorm.ErrRecordNotFound,
		"a soft-deleted session already had its sandbox destroyed")
}

// Read keeps the lenient contract: absent session and unpinned session are
// both simply "no live sandbox".
func TestReadReportsNoSandboxForMissingSession(t *testing.T) {
	pinner := NewSessionSandboxPinner(newPinTestDB(t))

	read, err := pinner.Read(context.Background(), "missing")
	require.NoError(t, err)
	require.Empty(t, read)
}

// The pin dies with the sandbox: after teardown the session must be free to
// follow its agent's CURRENT backend choice.
func TestClearReleasesPin(t *testing.T) {
	pinner := NewSessionSandboxPinner(newPinTestDB(t))
	ctx := context.Background()

	_, err := pinner.Pin(ctx, "s-1", SandboxPin{ConfigID: "cfg-a"})
	require.NoError(t, err)
	require.NoError(t, pinner.Clear(ctx, "s-1"))

	read, err := pinner.Read(ctx, "s-1")
	require.NoError(t, err)
	require.Empty(t, read)
}

// Session delete soft-deletes the row before any follow-up work. Destroy must
// therefore read sandbox_config_id first; otherwise the pinner sees an absent
// session and teardown resolves the wrong backend (T4 regression).
func TestSoftDeleteHidesSandboxPin(t *testing.T) {
	db := newPinTestDB(t)
	pinner := NewSessionSandboxPinner(db)
	ctx := context.Background()

	_, err := pinner.Pin(ctx, "s-1", SandboxPin{ConfigID: "cfg-a"})
	require.NoError(t, err)

	require.NoError(t, db.Delete(&types.Session{}, "id = ?", "s-1").Error)

	read, err := pinner.Read(ctx, "s-1")
	require.NoError(t, err)
	require.Empty(t, read, "soft-deleted session must not expose its pin")
}

func TestResolveSandboxForExecutionDoesNotPinStatelessBackend(t *testing.T) {
	pinner := NewSessionSandboxPinner(newPinTestDB(t))
	want := &pinTestManager{typ: sandbox.SandboxTypeDisabled}

	got, pin, err := resolveSandboxForExecution(
		context.Background(), stubSandboxResolver{mgr: want}, nil, pinner,
		7, "s-1", "cfg-local", nil,
	)

	require.NoError(t, err)
	require.Same(t, want, got)
	require.Equal(t, SandboxPin{ConfigID: "cfg-local", TenantID: 7}, pin)
	pinned, err := pinner.Read(context.Background(), "s-1")
	require.NoError(t, err)
	require.Empty(t, pinned, "disabled backends must not leave a session binding")
}

func TestResolveSandboxForExecutionPinsRemoteBackend(t *testing.T) {
	pinner := NewSessionSandboxPinner(newPinTestDB(t))
	want := &pinTestManager{typ: sandbox.SandboxTypeCube}

	got, pin, err := resolveSandboxForExecution(
		context.Background(), stubSandboxResolver{mgr: want}, nil, pinner,
		7, "s-1", "cfg-cube", nil,
	)

	require.NoError(t, err)
	require.Same(t, want, got)
	require.Equal(t, SandboxPin{ConfigID: "cfg-cube", TenantID: 7}, pin)
	pinned, err := pinner.Read(context.Background(), "s-1")
	require.NoError(t, err)
	require.Equal(t, SandboxPin{ConfigID: "cfg-cube", TenantID: 7}, pinned)
}

// Docker is a session-persistent remote backend, same as Cube/E2B. Skipping
// the pin used to make ArtifactCollector treat the turn as "no live sandbox"
// and leave generated HTML/files showing as unavailable in chat.
func TestResolveSandboxForExecutionPinsDockerBackend(t *testing.T) {
	pinner := NewSessionSandboxPinner(newPinTestDB(t))
	want := &pinTestManager{typ: sandbox.SandboxTypeDocker}

	got, pin, err := resolveSandboxForExecution(
		context.Background(), stubSandboxResolver{mgr: want}, nil, pinner,
		7, "s-1", "cfg-docker", nil,
	)

	require.NoError(t, err)
	require.Same(t, want, got)
	require.Equal(t, SandboxPin{ConfigID: "cfg-docker", TenantID: 7}, pin)
	pinned, err := pinner.Read(context.Background(), "s-1")
	require.NoError(t, err)
	require.Equal(t, SandboxPin{ConfigID: "cfg-docker", TenantID: 7}, pinned)
}

func TestResolveSandboxForExecutionKeepsExistingRemotePin(t *testing.T) {
	pinner := NewSessionSandboxPinner(newPinTestDB(t))
	_, err := pinner.Pin(context.Background(), "s-1", SandboxPin{ConfigID: "cfg-existing"})
	require.NoError(t, err)
	want := &pinTestManager{typ: sandbox.SandboxTypeE2B}

	got, pin, err := resolveSandboxForExecution(
		context.Background(), stubSandboxResolver{mgr: want}, nil, pinner,
		7, "s-1", "cfg-new-agent-choice", nil,
	)

	require.NoError(t, err)
	require.Same(t, want, got)
	require.Equal(t, SandboxPin{ConfigID: "cfg-existing", TenantID: 7}, pin,
		"re-pointing an agent must not move an existing remote session")
}

// A shared agent runs on ITS OWNER's sandbox config, so the pin has to record
// that workspace: everything outside the chat turn (teardown, the terminal and
// desktop panels, fork snapshots) runs as the session owner and would
// otherwise look the config up in a workspace that does not have it.
func TestResolveSandboxForExecutionPinsTheConfigOwningWorkspace(t *testing.T) {
	const sessionOwner, agentOwner = uint64(7), uint64(99)
	pinner := NewSessionSandboxPinner(newPinTestDB(t))
	resolver := &tenantRecordingResolver{mgr: &pinTestManager{typ: sandbox.SandboxTypeCube}}

	_, pin, err := resolveSandboxForExecution(
		context.Background(), resolver, nil, pinner,
		agentOwner, "s-1", "cfg-owned-by-99", nil,
	)
	require.NoError(t, err)
	require.Equal(t, SandboxPin{ConfigID: "cfg-owned-by-99", TenantID: agentOwner}, pin)

	stored, err := pinner.Read(context.Background(), "s-1")
	require.NoError(t, err)
	require.Equal(t, agentOwner, stored.TenantID, "the lending workspace is durable")
	require.Equal(t, agentOwner, stored.TenantOr(sessionOwner),
		"a borrowed pin ignores the session's own workspace")
}

// An own agent's config already lives in the session's workspace, so the pin
// stays owner-less and TenantOr keeps resolving it exactly as before. This is
// what makes the new column a no-op for every existing session.
func TestSandboxPinTenantOrFallsBackToTheSessionWorkspace(t *testing.T) {
	require.Equal(t, uint64(7), SandboxPin{ConfigID: "cfg-a"}.TenantOr(7))
	require.Equal(t, uint64(99), SandboxPin{ConfigID: "cfg-a", TenantID: 99}.TenantOr(7))
	require.True(t, SandboxPin{}.IsZero())
	require.False(t, SandboxPin{ConfigID: "cfg-a"}.IsZero())
}

// Pre-migration pins (and any row the SQL backfill missed) store TenantID=0.
// The next chat turn already knows the workspace that can resolve the config;
// persisting it is what lets DELETE / the panel / fork find that sandbox later
// instead of looking it up as the session owner and leaking a paused MicroVM.
func TestResolveSandboxForExecutionPersistsOwnerOnLegacyPin(t *testing.T) {
	const agentOwner = uint64(99)
	pinner := NewSessionSandboxPinner(newPinTestDB(t))
	_, err := pinner.Pin(context.Background(), "s-1",
		SandboxPin{ConfigID: "cfg-owned-by-99"})
	require.NoError(t, err)

	resolver := &tenantRecordingResolver{mgr: &pinTestManager{typ: sandbox.SandboxTypeCube}}
	_, pin, err := resolveSandboxForExecution(
		context.Background(), resolver, nil, pinner,
		agentOwner, "s-1", "", nil,
	)
	require.NoError(t, err)
	require.Equal(t, SandboxPin{ConfigID: "cfg-owned-by-99", TenantID: agentOwner}, pin)

	stored, err := pinner.Read(context.Background(), "s-1")
	require.NoError(t, err)
	require.Equal(t, agentOwner, stored.TenantID,
		"a successful resolve must record the workspace that owns the pin")
}

// A pin resolves in the workspace it recorded, not the one on the context.
func TestResolveSandboxForExecutionResolvesPinnedConfigInItsOwnWorkspace(t *testing.T) {
	pinner := NewSessionSandboxPinner(newPinTestDB(t))
	_, err := pinner.Pin(context.Background(), "s-1",
		SandboxPin{ConfigID: "cfg-owned-by-99", TenantID: 99})
	require.NoError(t, err)
	resolver := &tenantRecordingResolver{mgr: &pinTestManager{typ: sandbox.SandboxTypeCube}}

	_, pin, err := resolveSandboxForExecution(
		context.Background(), resolver, nil, pinner,
		7 /* the borrower, e.g. a teardown or panel-open caller */, "s-1", "", nil,
	)

	require.NoError(t, err)
	require.Equal(t, uint64(99), resolver.lastTenant)
	require.Equal(t, "cfg-owned-by-99", resolver.lastConfig)
	require.Equal(t, SandboxPin{ConfigID: "cfg-owned-by-99", TenantID: 99}, pin)
}

// A failed lookup must not stamp the caller's workspace onto a legacy pin:
// the config may belong to a previous shared agent, and the pin is sticky.
func TestResolveSandboxForExecutionDoesNotPersistOwnerWhenResolveFails(t *testing.T) {
	pinner := NewSessionSandboxPinner(newPinTestDB(t))
	_, err := pinner.Pin(context.Background(), "s-1",
		SandboxPin{ConfigID: "cfg-owned-by-99"})
	require.NoError(t, err)

	resolver := failingSandboxResolver{}
	_, pin, err := resolveSandboxForExecution(
		context.Background(), resolver, nil, pinner,
		7, "s-1", "", nil,
	)
	require.ErrorIs(t, err, sandbox.ErrSandboxConfigNotFound)
	require.Equal(t, uint64(0), pin.TenantID)

	stored, err := pinner.Read(context.Background(), "s-1")
	require.NoError(t, err)
	require.Zero(t, stored.TenantID, "a failed lookup must leave the owner unset")
}

type failingSandboxResolver struct{}

func (failingSandboxResolver) Resolve(context.Context, uint64, string) (sandbox.Manager, error) {
	return nil, sandbox.ErrSandboxConfigNotFound
}

// tenantRecordingResolver reports which workspace a config was looked up in.
type tenantRecordingResolver struct {
	mgr        sandbox.Manager
	lastTenant uint64
	lastConfig string
}

func (r *tenantRecordingResolver) Resolve(
	_ context.Context, tenantID uint64, configID string,
) (sandbox.Manager, error) {
	r.lastTenant, r.lastConfig = tenantID, configID
	return r.mgr, nil
}

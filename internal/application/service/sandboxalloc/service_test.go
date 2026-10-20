package sandboxalloc

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/sandbox/allocation"
	"github.com/Tencent/WeKnora/internal/types"
)

type fakeConfigs map[string]string // config id -> allocation_mode

func (f fakeConfigs) GetByID(_ context.Context, tenantID uint64, id string) (*types.TenantSandboxConfigEntity, error) {
	return &types.TenantSandboxConfigEntity{
		ID: id, TenantID: tenantID,
		Config: &types.TenantSandboxConfig{AllocationMode: f[id]},
	}, nil
}

type testEnv struct {
	svc *Service
	db  *gorm.DB
}

func newEnv(t *testing.T, configs fakeConfigs, sessions ...*types.Session) *testEnv {
	t.Helper()
	// Same SQLite pool as the allocation repository tests: one connection and
	// a busy timeout, so concurrent first-writers queue instead of failing
	// with "database table is locked".
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_busy_timeout=5000", uuid.NewString())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&types.Session{}, &types.SessionSandboxAssignment{},
		&types.SandboxUserMachine{}, &types.Message{}))
	for _, s := range sessions {
		require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(s).Error)
	}
	svc := New(repository.NewSandboxAllocationRepository(db), configs, repository.NewSessionRepository(db), "weknora")
	return &testEnv{svc: svc, db: db}
}

func webSession(id string) *types.Session {
	return &types.Session{ID: id, TenantID: 7, UserID: "u-1", OwnerAuthSource: types.AuthSourceWeb}
}

func (e *testEnv) place(t *testing.T, sessionID, configID string) allocation.Assignment {
	t.Helper()
	a, _ := e.placeWithReason(t, sessionID, configID)
	return a
}

func (e *testEnv) placeWithReason(
	t *testing.T, sessionID, configID string,
) (allocation.Assignment, allocation.FallbackReason) {
	t.Helper()
	a, reason, err := e.svc.place(context.Background(), PlaceRequest{
		SessionTenantID: 7, SessionID: sessionID, ConfigTenantID: 7, ConfigID: configID,
	})
	require.NoError(t, err)
	return a, reason
}

func (e *testEnv) reason(t *testing.T, sessionID, configID string) allocation.FallbackReason {
	t.Helper()
	_, r := e.placeWithReason(t, sessionID, configID)
	return r
}

func (e *testEnv) addSession(t *testing.T, s *types.Session) {
	t.Helper()
	require.NoError(t, e.db.Session(&gorm.Session{SkipHooks: true}).Create(s).Error)
}

func (e *testEnv) digest(t *testing.T, sessionID string) string {
	t.Helper()
	row, err := e.svc.repo.GetAssignment(context.Background(), sessionID)
	require.NoError(t, err)
	require.NotNil(t, row)
	return row.MachineDigest
}

func (e *testEnv) pin(t *testing.T, sessionID string) (string, uint64) {
	t.Helper()
	var s types.Session
	require.NoError(t, e.db.Session(&gorm.Session{SkipHooks: true}).Where("id = ?", sessionID).Take(&s).Error)
	return s.SandboxConfigID, s.SandboxConfigTenantID
}

func enableUserMode(t *testing.T) {
	t.Helper()
	allocation.SetUserModeAvailable(true)
	t.Cleanup(func() { allocation.SetUserModeAvailable(false) })
}

type errConfigs struct{ err error }

func (e errConfigs) GetByID(context.Context, uint64, string) (*types.TenantSandboxConfigEntity, error) {
	return nil, e.err
}

func TestPlaceDoesNotRecordAModeWhenTheConfigReadFails(t *testing.T) {
	enableUserMode(t)
	id := uuid.NewString()
	e := newEnv(t, fakeConfigs{"cfg-a": "user"}, webSession(id))
	e.svc.configs = errConfigs{err: errors.New("timeout")}

	_, err := e.svc.Place(context.Background(), PlaceRequest{
		SessionTenantID: 7, SessionID: id, ConfigTenantID: 7, ConfigID: "cfg-a",
	})
	require.Error(t, err)
	row, err := e.svc.repo.GetAssignment(context.Background(), id)
	require.NoError(t, err)
	require.Nil(t, row, "a failed config read must be retried, not stored as session mode")
}

func TestPlacePutsAVerifiedWebUserOnASharedMachineAndPinsIt(t *testing.T) {
	enableUserMode(t)
	id := uuid.NewString()
	e := newEnv(t, fakeConfigs{"cfg-a": "user"}, webSession(id))

	a := e.place(t, id, "cfg-a")
	require.Equal(t, allocation.ModeUser, a.Mode())
	m, ok := a.UserMachine()
	require.True(t, ok)
	require.Equal(t, allocation.UserMachine{OwnerTenantID: 7, OwnerID: "u-1", ConfigTenantID: 7, ConfigID: "cfg-a"}, m)
	cfg, tenant := e.pin(t, id)
	require.Equal(t, "cfg-a", cfg)
	require.EqualValues(t, 7, tenant)

	// The stored row wins over a later request for another config, and reads
	// back the same machine.
	again := e.place(t, id, "cfg-b")
	require.Equal(t, a, again)
}

// The owner's kind is derived from the stored source and the id's shape; a
// source that disagrees with the id never makes a verified person.
func TestPlaceDerivesTheOwnerKindFromItsSource(t *testing.T) {
	enableUserMode(t)
	ext, webWithColon, unknown := uuid.NewString(), uuid.NewString(), uuid.NewString()
	e := newEnv(t, fakeConfigs{"cfg-a": "user"},
		&types.Session{
			ID: ext, TenantID: 7, UserID: "api_external_user:7:alice",
			OwnerAuthSource: types.AuthSourceAPISignedToken,
		},
		&types.Session{
			ID: webWithColon, TenantID: 7, UserID: "api_tenant_key:7:3",
			OwnerAuthSource: types.AuthSourceWeb,
		},
		&types.Session{ID: unknown, TenantID: 7, UserID: "system-7"})

	require.Equal(t, allocation.ModeUser, e.place(t, ext, "cfg-a").Mode())
	require.Equal(t, allocation.ReasonOwnerIdentityUnverified, e.reason(t, webWithColon, "cfg-a"))
	require.Equal(t, allocation.ReasonOwnerIdentityUnverified, e.reason(t, unknown, "cfg-a"),
		"a row from before the column existed keeps a machine per session")
}

func TestLookupRefusesAUserAssignmentWithoutItsMachine(t *testing.T) {
	enableUserMode(t)
	id := uuid.NewString()
	e := newEnv(t, fakeConfigs{"cfg-a": "user"}, webSession(id))
	e.place(t, id, "cfg-a")
	require.NoError(t, e.svc.Repo().DeleteUserMachinesByConfig(context.Background(), 7, "cfg-a"))

	_, _, err := e.svc.Lookup(context.Background(), id)
	require.ErrorIs(t, err, ErrMachineUnregistered, "never read back as a machine of its own")
}

func TestPlaceConcurrentCandidatesConvergeOnOneConfig(t *testing.T) {
	enableUserMode(t)
	id := uuid.NewString()
	e := newEnv(t, fakeConfigs{"cfg-a": "user", "cfg-b": "session"}, webSession(id))
	var wg sync.WaitGroup
	got := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cfg := "cfg-a"
			if i%2 == 1 {
				cfg = "cfg-b"
			}
			got <- e.place(t, id, cfg).ConfigID
		}(i)
	}
	wg.Wait()
	close(got)
	winner := ""
	for c := range got {
		if winner == "" {
			winner = c
		}
		require.Equal(t, winner, c, "every caller is told the same config")
	}
	cfg, _ := e.pin(t, id)
	require.Equal(t, winner, cfg, "the pin is the assignment's projection")
}

func TestPlaceFallbacks(t *testing.T) {
	enableUserMode(t)
	apiKey, legacy, maint, fork := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	legacyRow := webSession(legacy)
	legacyRow.SandboxConfigID, legacyRow.SandboxConfigTenantID = "cfg-a", 7
	maintRow := webSession(maint)
	maintRow.Description = types.SkillMaintenanceSessionMarker + "install"
	forkRow := webSession(fork)
	forkRow.ForkBootstrap = &types.ForkBootstrap{SnapshotID: "snap"}
	e := newEnv(t, fakeConfigs{"cfg-a": "user"},
		&types.Session{
			ID: apiKey, TenantID: 7, UserID: "api_tenant_key:7:3",
			OwnerAuthSource: types.AuthSourceAPITenantKey,
		},
		legacyRow, maintRow, forkRow)

	require.Equal(t, allocation.ReasonSharedAPIKey, e.reason(t, apiKey, "cfg-a"))
	got, reason := e.placeWithReason(t, legacy, "cfg-a")
	require.Equal(t, allocation.ModeSession, got.Mode(),
		"a session that already has a pin keeps its per-session sandbox")
	require.Equal(t, allocation.ReasonLegacySessionSandbox, reason)
	require.Equal(t, allocation.ReasonMaintenanceDedicated, e.reason(t, maint, "cfg-a"))
	require.Equal(t, allocation.ReasonLegacySessionSandbox, e.reason(t, fork, "cfg-a"),
		"an unopened snapshot fork keeps its per-session bootstrap")
}

func TestPlaceWithTheGateOffFallsBack(t *testing.T) {
	allocation.SetUserModeAvailable(false)
	id := uuid.NewString()
	e := newEnv(t, fakeConfigs{"cfg-a": "user"}, webSession(id))
	a, reason := e.placeWithReason(t, id, "cfg-a")
	require.Equal(t, allocation.ModeSession, a.Mode())
	require.Equal(t, allocation.ReasonUserModeUnavailable, reason)
}

func TestReleaseAndForkAssignment(t *testing.T) {
	enableUserMode(t)
	id := uuid.NewString()
	e := newEnv(t, fakeConfigs{"cfg-a": "user"}, webSession(id))
	a := e.place(t, id, "cfg-a")

	target := uuid.NewString()
	child := e.svc.ForkAssignment(a, target)
	require.Equal(t, target, child.SessionID)
	require.Equal(t, a.Machine, child.Machine)
	require.Equal(t, a.ConfigID, child.ConfigID)
	require.Nil(t, child.ReleasedAt)

	r, err := e.svc.Release(context.Background(), id)
	require.NoError(t, err)
	require.NotNil(t, r.Assignment)
	require.True(t, r.MachineEmpty)
	require.NotNil(t, r.Assignment.ReleasedAt)
	require.NotNil(t, r.Assignment.CleanupDueAt)

	released, ok, err := e.svc.Lookup(context.Background(), id)
	require.NoError(t, err)
	require.True(t, ok, "cleanup and inventory still find a released session's machine")
	require.True(t, released.Released())
	require.Equal(t, a.Machine, released.Machine)
}

func TestPlaceOnARetiredSessionIsRefused(t *testing.T) {
	enableUserMode(t)
	id := uuid.NewString()
	e := newEnv(t, fakeConfigs{"cfg-a": "user"}, webSession(id))
	out, err := e.svc.Retire(context.Background(), []string{id})
	require.NoError(t, err)
	require.Nil(t, out[0].Assignment, "retiring works before any placement")

	_, err = e.svc.Place(types.WithSandboxTenantID(context.Background(), 7), PlaceRequest{
		SessionTenantID: 7, SessionID: id, ConfigTenantID: 7, ConfigID: "cfg-a",
	})
	require.ErrorIs(t, err, sandbox.ErrSandboxSessionDeleted)
	_, ok, err := e.svc.Lookup(context.Background(), id)
	require.NoError(t, err)
	require.False(t, ok)
}

// Lookup keeps a released row for cleanup; Place must refuse it, or a turn
// already in flight would rebuild the workspace of a deleted session.
func TestPlaceOnAReleasedAssignmentIsRefused(t *testing.T) {
	enableUserMode(t)
	id := uuid.NewString()
	e := newEnv(t, fakeConfigs{"cfg-a": "user"}, webSession(id))
	e.place(t, id, "cfg-a")
	_, err := e.svc.Release(context.Background(), id)
	require.NoError(t, err)

	_, err = e.svc.Place(context.Background(), PlaceRequest{
		SessionTenantID: 7, SessionID: id, ConfigTenantID: 7, ConfigID: "cfg-a",
	})
	require.ErrorIs(t, err, sandbox.ErrSandboxSessionDeleted)
}

func TestReleaseWithoutASessionIDIsAnError(t *testing.T) {
	e := newEnv(t, fakeConfigs{})
	_, err := e.svc.Release(context.Background(), " ")
	require.Error(t, err)
}

func (e *testEnv) updateSession(t *testing.T, id string, cols map[string]any) {
	t.Helper()
	require.NoError(t, e.db.Session(&gorm.Session{SkipHooks: true}).
		Model(&types.Session{}).Where("id = ?", id).Updates(cols).Error)
}

var _ sandbox.AllocationDirectory = (*Service)(nil)

func TestVerifySessionAllocationChecksTheCurrentOwnerAndRetirement(t *testing.T) {
	enableUserMode(t)
	id := uuid.NewString()
	e := newEnv(t, fakeConfigs{"cfg-a": "user"}, webSession(id))
	a := e.place(t, id, "cfg-a")
	ctx := context.Background()
	require.NoError(t, e.svc.VerifySessionAllocation(ctx, 7, id, a))

	stale := a
	stale.ConfigID = "cfg-b"
	require.ErrorIs(t, e.svc.VerifySessionAllocation(ctx, 7, id, stale), sandbox.ErrUserAllocationRejected,
		"a cached address that no longer matches the row is refused")

	e.updateSession(t, id, map[string]any{"user_id": "u-2"})
	require.ErrorIs(t, e.svc.VerifySessionAllocation(ctx, 7, id, a), sandbox.ErrUserAllocationRejected,
		"the session changed hands; the old owner's machine is off limits (C15)")
	e.updateSession(t, id, map[string]any{"user_id": "u-1", "owner_auth_source": "api_key"})
	require.ErrorIs(t, e.svc.VerifySessionAllocation(ctx, 7, id, a), sandbox.ErrUserAllocationRejected)
	e.updateSession(t, id, map[string]any{"owner_auth_source": "web"})
	require.NoError(t, e.svc.VerifySessionAllocation(ctx, 7, id, a))

	_, err := e.svc.Retire(ctx, []string{id})
	require.NoError(t, err)
	require.ErrorIs(t, e.svc.VerifySessionAllocation(ctx, 7, id, a), sandbox.ErrSandboxSessionDeleted)
}

package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service/sandboxalloc"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/sandbox/admission"
	"github.com/Tencent/WeKnora/internal/sandbox/allocation"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// userDeleteSessionID is a canonical UUID. Place refuses user mode for any
// other shape, so the brief's illustrative "s-1" cannot be placed on a shared
// machine. Session-mode cases below keep "s-1".
const userDeleteSessionID = "0f8fad5b-d9cb-469f-a165-70867728950e"

type recordingUserMachine struct {
	t             *testing.T
	mu            sync.Mutex
	deleted       [][]string
	destroyed     []string
	rejectDestroy bool
	onReclaim     func()
	onDelete      func()
}

func (m *recordingUserMachine) Execute(context.Context, *sandbox.ExecuteConfig) (*sandbox.ExecuteResult, error) {
	return nil, nil
}
func (m *recordingUserMachine) Cleanup(context.Context) error { return nil }
func (m *recordingUserMachine) GetSandbox() sandbox.Sandbox   { return nil }
func (m *recordingUserMachine) GetType() sandbox.SandboxType  { return sandbox.SandboxTypeCube }

func (m *recordingUserMachine) DestroySession(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.destroyed = append(m.destroyed, id)
	if m.rejectDestroy {
		m.t.Errorf("DestroySession(%s) called on a shared machine", id)
	}
	return nil
}

func (m *recordingUserMachine) DeleteSessionWorkspaces(
	_ context.Context, _ allocation.UserMachine, ids []string,
) ([]string, error) {
	if m.onDelete != nil {
		m.onDelete()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	copied := append([]string(nil), ids...)
	m.deleted = append(m.deleted, copied)
	return copied, nil
}

func (m *recordingUserMachine) ReclaimUserMachine(
	context.Context, allocation.UserMachine, func(context.Context) (bool, error),
) (bool, error) {
	if m.onReclaim != nil {
		m.onReclaim()
	}
	return true, nil
}

func (m *recordingUserMachine) workspaceDeletes() [][]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([][]string, len(m.deleted))
	for i := range m.deleted {
		out[i] = append([]string(nil), m.deleted[i]...)
	}
	return out
}

func (m *recordingUserMachine) destroyedIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.destroyed...)
}

type allocationModeConfig string

func (m allocationModeConfig) GetByID(
	_ context.Context, tenantID uint64, id string,
) (*types.TenantSandboxConfigEntity, error) {
	return &types.TenantSandboxConfigEntity{
		ID: id, TenantID: tenantID,
		Config: &types.TenantSandboxConfig{AllocationMode: string(m)},
	}, nil
}

type retireErrorRepo struct {
	interfaces.SandboxAllocationRepository
	err error
}

func (r retireErrorRepo) RetireSessions(context.Context, []string, time.Time) ([]interfaces.RetiredSession, error) {
	return nil, r.err
}

func (r retireErrorRepo) DeleteSessionsRetiring(
	context.Context, uint64, map[string][]string, time.Time,
) ([]interfaces.RetiredSession, error) {
	return nil, r.err
}

type userDeleteFixture struct {
	svc   *sessionService
	alloc *sandboxalloc.Service
	ops   *recordingUserMachine
	db    *gorm.DB
}

func newUserDeleteFixture(t *testing.T, mode string, retireErr error) *userDeleteFixture {
	t.Helper()
	allocation.SetUserModeAvailable(true)
	t.Cleanup(func() { allocation.SetUserModeAvailable(false) })

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_busy_timeout=5000", uuid.NewString())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(
		&types.Session{}, &types.SessionSandboxAssignment{}, &types.SandboxUserMachine{}, &types.Message{},
	))

	repo := repository.NewSandboxAllocationRepository(db)
	if retireErr != nil {
		repo = retireErrorRepo{SandboxAllocationRepository: repo, err: retireErr}
	}
	alloc := sandboxalloc.New(repo, allocationModeConfig(mode), repository.NewSessionRepository(db), "weknora")
	ops := &recordingUserMachine{t: t, rejectDestroy: mode == "user" && retireErr == nil}
	resolver := stubSandboxResolver{mgr: ops}
	svc := &sessionService{
		sessionRepo:        repository.NewSessionRepository(db),
		messageRepo:        deleteForkMessageRepo{},
		webSearchStateRepo: deleteForkWebSearchState{},
		sandboxPinner:      NewSessionSandboxPinner(db),
		sandboxResolver:    resolver,
		forkSnapshots:      &fakeForkSessionSnapshotDeleter{},
		workspaceCleaner:   sandboxalloc.NewWorkspaceCleaner(alloc, resolver),
	}
	return &userDeleteFixture{svc: svc, alloc: alloc, ops: ops, db: db}
}

func (f *userDeleteFixture) place(t *testing.T, id string) {
	t.Helper()
	require.NoError(t, f.db.Session(&gorm.Session{SkipHooks: true}).Create(&types.Session{
		ID: id, TenantID: 7, UserID: "u-1", Title: "chat", OwnerAuthSource: types.AuthSourceWeb,
	}).Error)
	_, err := f.alloc.Place(context.Background(), sandboxalloc.PlaceRequest{
		SessionTenantID: 7, SessionID: id, ConfigTenantID: 7, ConfigID: "cfg-a",
	})
	require.NoError(t, err)
}

func (f *userDeleteFixture) assignment(t *testing.T, id string) *types.SessionSandboxAssignment {
	t.Helper()
	row, err := f.alloc.Repo().GetAssignment(context.Background(), id)
	require.NoError(t, err)
	return row
}

func TestDeleteSessionOnUserMachineReleasesThenCleansAndNeverDestroys(t *testing.T) {
	f := newUserDeleteFixture(t, "user", nil)
	f.place(t, userDeleteSessionID)
	ctx := testSessionScopeContext(7, "u-1")

	require.NoError(t, f.svc.DeleteSession(ctx, userDeleteSessionID))

	row := f.assignment(t, userDeleteSessionID)
	require.NotNil(t, row)
	require.NotNil(t, row.ReleasedAt, "the assignment is released before the row goes")
	require.Eventually(t, func() bool {
		again := f.assignment(t, userDeleteSessionID)
		return again != nil && again.CleanupDueAt == nil && len(f.ops.workspaceDeletes()) == 1
	}, 2*time.Second, 10*time.Millisecond)
	require.Equal(t, []string{userDeleteSessionID}, f.ops.workspaceDeletes()[0])
	require.Empty(t, f.ops.destroyedIDs())
}

func TestDeleteSessionFailsWhenTheReleaseCannotBePersisted(t *testing.T) {
	boom := errors.New("retire sessions: database is closed")
	f := newUserDeleteFixture(t, "user", boom)
	f.place(t, userDeleteSessionID)
	ctx := testSessionScopeContext(7, "u-1")

	err := f.svc.DeleteSession(ctx, userDeleteSessionID)
	require.ErrorIs(t, err, boom)
	got, err := f.svc.sessionRepo.GetByID(ctx, 7, userDeleteSessionID)
	require.NoError(t, err)
	require.Equal(t, userDeleteSessionID, got.ID)
	require.Nil(t, f.assignment(t, userDeleteSessionID).ReleasedAt)
	require.Empty(t, f.ops.destroyedIDs())
	require.Empty(t, f.ops.workspaceDeletes())
}

func TestSessionDeleteFailureRollsBackRetirement(t *testing.T) {
	for _, operation := range []string{"single", "batch", "all"} {
		t.Run(operation, func(t *testing.T) {
			f := newUserDeleteFixture(t, "user", nil)
			f.place(t, userDeleteSessionID)
			ids := []string{userDeleteSessionID}
			if operation != "single" {
				ids = append(ids, uuid.NewString())
				f.place(t, ids[1])
			}
			// Fail the actual soft delete after retirement has written its rows.
			require.NoError(t, f.db.Exec(`CREATE TRIGGER reject_session_delete
BEFORE UPDATE OF deleted_at ON sessions
BEGIN SELECT RAISE(ABORT, 'injected session delete failure'); END`).Error)
			ctx := testSessionScopeContext(7, "u-1")
			var err error
			switch operation {
			case "single":
				err = f.svc.DeleteSession(ctx, ids[0])
			case "batch":
				err = f.svc.BatchDeleteSessions(ctx, ids)
			case "all":
				err = f.svc.DeleteAllSessions(ctx)
			}
			require.ErrorContains(t, err, "injected session delete failure")
			for _, id := range ids {
				_, err := f.svc.sessionRepo.GetByID(ctx, 7, id)
				require.NoError(t, err, "the failed deletion must leave the session usable")
				row := f.assignment(t, id)
				require.Nil(t, row.ReleasedAt)
				require.Nil(t, row.CleanupDueAt, "GC must not remove files of the surviving session")
				machine, err := f.alloc.Repo().GetUserMachine(ctx, row.MachineDigest)
				require.NoError(t, err)
				require.Nil(t, machine.EmptySince)
				a, found, err := f.alloc.Lookup(ctx, id)
				require.NoError(t, err)
				require.True(t, found)
				require.NoError(t, f.alloc.VerifySessionAllocation(ctx, 7, id, a))
			}
			require.Empty(t, f.ops.workspaceDeletes())
			require.Empty(t, f.ops.destroyedIDs())
		})
	}
}

type reclaimLifecycleClient struct {
	sealRemoteClient
	deleted []string
}

func (c *reclaimLifecycleClient) Delete(_ context.Context, id string) error {
	c.deleted = append(c.deleted, id)
	return nil
}

func TestUserMachineReclaimCommitsWithRealManagerAndRepository(t *testing.T) {
	for _, operation := range []string{"release", "gc"} {
		t.Run(operation, func(t *testing.T) {
			f := newUserDeleteFixture(t, "user", nil)
			f.place(t, userDeleteSessionID)
			client := &reclaimLifecycleClient{}
			cfg := sandbox.DefaultConfig()
			cfg.Type = sandbox.SandboxTypeCube
			cfg.CubeTemplate = "tpl-test"
			mgr, err := sandbox.NewSessionBoundManager(sandbox.SessionBoundManagerConfig{
				Config: cfg, Client: client, Store: sandbox.NewMemorySessionSandboxBindingStore(),
				Checker: alwaysExistsChecker{}, SkipHealthProbe: true,
				ConfigID: "cfg-a", ConfigTenantID: 7, Namespace: "weknora",
				Allocations: f.alloc, Admission: admission.NewMemory(),
			})
			require.NoError(t, err)
			ctx := types.WithSandboxTenantID(context.Background(), 7)
			_, err = mgr.ExecShellCommand(ctx, userDeleteSessionID, "true", "", time.Second, nil)
			require.NoError(t, err)
			sandboxID, found := mgr.BoundSandboxID(ctx, userDeleteSessionID)
			require.True(t, found)
			a, found, err := f.alloc.Lookup(ctx, userDeleteSessionID)
			require.NoError(t, err)
			require.True(t, found)
			machine, ok := a.UserMachine()
			require.True(t, ok)
			_, err = f.alloc.Retire(ctx, []string{userDeleteSessionID})
			require.NoError(t, err)
			digest := f.alloc.Digest(machine)
			require.NoError(t, f.db.Model(&types.SandboxUserMachine{}).
				Where("machine_digest = ?", digest).
				Update("empty_since", time.Now().Add(-8*24*time.Hour)).Error)
			cleaner := sandboxalloc.NewWorkspaceCleaner(f.alloc, stubSandboxResolver{mgr: mgr})
			if operation == "release" {
				deleted, err := cleaner.ReleaseMachine(ctx, machine)
				require.NoError(t, err)
				require.True(t, deleted)
			} else {
				gc := sandboxalloc.NewUserMachineGC(f.alloc, cleaner, allocationModeConfig("user"), nil)
				report, err := gc.RunOnce(ctx)
				require.NoError(t, err)
				require.Zero(t, report.Failed)
				require.Equal(t, 1, report.Reclaimed)
			}
			require.Equal(t, []string{sandboxID}, client.deleted)
			row, err := f.alloc.Repo().GetUserMachine(ctx, digest)
			require.NoError(t, err)
			require.Nil(t, row, "provider deletion and registry deletion must both complete")
		})
	}
}

func TestDeleteSessionOnPerSessionSandboxStillDestroys(t *testing.T) {
	f := newUserDeleteFixture(t, "session", nil)
	f.place(t, "s-1")
	ctx := testSessionScopeContext(7, "u-1")

	require.NoError(t, f.svc.DeleteSession(ctx, "s-1"))

	require.Equal(t, []string{"s-1"}, f.ops.destroyedIDs())
	require.Empty(t, f.ops.workspaceDeletes())
	row := f.assignment(t, "s-1")
	require.NotNil(t, row.ReleasedAt, "session mode is retired too")
	require.Nil(t, row.CleanupDueAt, "a per-session sandbox has no directory cleanup task")
}

func TestBatchDeleteSessionsReleasesUserModeAndStillDestroysSessionMode(t *testing.T) {
	f := newUserDeleteFixture(t, "user", nil)
	f.ops.rejectDestroy = false
	f.place(t, userDeleteSessionID)
	// Place the per-session row through a session-mode allocator that shares
	// the repository. The cleaner keeps the user-mode service it was built with.
	legacy := sandboxalloc.New(f.alloc.Repo(), allocationModeConfig("session"),
		repository.NewSessionRepository(f.db), "weknora")
	require.NoError(t, f.db.Session(&gorm.Session{SkipHooks: true}).Create(&types.Session{
		ID: "s-1", TenantID: 7, UserID: "u-1", Title: "legacy", OwnerAuthSource: types.AuthSourceWeb,
	}).Error)
	_, err := legacy.Place(context.Background(), sandboxalloc.PlaceRequest{
		SessionTenantID: 7, SessionID: "s-1", ConfigTenantID: 7, ConfigID: "cfg-a",
	})
	require.NoError(t, err)
	ctx := testSessionScopeContext(7, "u-1")

	require.NoError(t, f.svc.BatchDeleteSessions(ctx, []string{userDeleteSessionID, "s-1"}))

	require.Eventually(t, func() bool {
		return len(f.ops.workspaceDeletes()) == 1
	}, 2*time.Second, 10*time.Millisecond)
	require.ElementsMatch(t, []string{userDeleteSessionID}, f.ops.workspaceDeletes()[0])
	require.Equal(t, []string{"s-1"}, f.ops.destroyedIDs())
	require.NotNil(t, f.assignment(t, userDeleteSessionID).ReleasedAt)
	require.NotNil(t, f.assignment(t, "s-1").ReleasedAt)
	require.Nil(t, f.assignment(t, "s-1").CleanupDueAt)
}

func TestDeleteAllSessionsOnUserMachineReleasesThenCleans(t *testing.T) {
	f := newUserDeleteFixture(t, "user", nil)
	f.place(t, userDeleteSessionID)
	ctx := testSessionScopeContext(7, "u-1")

	require.NoError(t, f.svc.DeleteAllSessions(ctx))

	_, err := f.svc.sessionRepo.GetByID(ctx, 7, userDeleteSessionID)
	require.Error(t, err, "the listed session row is deleted")
	require.Eventually(t, func() bool {
		again := f.assignment(t, userDeleteSessionID)
		return again != nil && again.CleanupDueAt == nil && len(f.ops.workspaceDeletes()) == 1
	}, 2*time.Second, 10*time.Millisecond)
	require.Equal(t, []string{userDeleteSessionID}, f.ops.workspaceDeletes()[0])
	require.Empty(t, f.ops.destroyedIDs())
	require.NotNil(t, f.assignment(t, userDeleteSessionID).ReleasedAt)
}

func TestDeleteAllSessionsReclaimsBeforeDirectoryCleanup(t *testing.T) {
	f := newUserDeleteFixture(t, "user", nil)
	f.place(t, userDeleteSessionID)

	reclaimEntered := make(chan struct{})
	releaseReclaim := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseReclaim) }) }
	t.Cleanup(release)
	deleteStarted := make(chan struct{}, 1)
	f.ops.onReclaim = func() {
		close(reclaimEntered)
		<-releaseReclaim
	}
	f.ops.onDelete = func() {
		select {
		case deleteStarted <- struct{}{}:
		default:
		}
	}
	ctx := testSessionScopeContext(7, "u-1")
	require.NoError(t, f.svc.DeleteAllSessions(ctx))

	select {
	case <-reclaimEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("reclaim was not called")
	}
	select {
	case <-deleteStarted:
		t.Fatal("directory delete started before reclaim returned")
	case <-time.After(200 * time.Millisecond):
	}
	release()

	require.Eventually(t, func() bool {
		return len(f.ops.workspaceDeletes()) == 1
	}, 2*time.Second, 10*time.Millisecond)
}

func TestDeleteAllSessionsLeavesRowsWhenTheListFails(t *testing.T) {
	f := newUserDeleteFixture(t, "user", nil)
	f.place(t, userDeleteSessionID)
	boom := errors.New("list sessions: database is closed")
	f.svc.sessionRepo = listFailSessionRepo{SessionRepository: f.svc.sessionRepo, err: boom}
	ctx := testSessionScopeContext(7, "u-1")

	err := f.svc.DeleteAllSessions(ctx)
	require.ErrorIs(t, err, boom)
	var row types.Session
	require.NoError(t, f.db.Where("id = ?", userDeleteSessionID).Take(&row).Error)
	require.Equal(t, userDeleteSessionID, row.ID)
	require.Nil(t, f.assignment(t, userDeleteSessionID).ReleasedAt, "a failed list must not retire")
}

func TestDeleteAllSessionsLeavesASessionCreatedAfterTheList(t *testing.T) {
	f := newUserDeleteFixture(t, "user", nil)
	f.place(t, userDeleteSessionID)
	const lateID = "s-late"
	f.svc.sessionRepo = &insertAfterListRepo{SessionRepository: f.svc.sessionRepo, db: f.db, lateID: lateID}
	ctx := testSessionScopeContext(7, "u-1")

	require.NoError(t, f.svc.DeleteAllSessions(ctx))

	_, err := f.svc.sessionRepo.GetByID(ctx, 7, userDeleteSessionID)
	require.Error(t, err, "the session that was listed is deleted")
	var late types.Session
	require.NoError(t, f.db.Where("id = ?", lateID).Take(&late).Error, "a session created after the list stays")
}

func TestDeleteAllSessionsFailsWhenTheReleaseCannotBePersisted(t *testing.T) {
	boom := errors.New("retire sessions: database is closed")
	f := newUserDeleteFixture(t, "user", boom)
	f.place(t, userDeleteSessionID)
	ctx := testSessionScopeContext(7, "u-1")

	err := f.svc.DeleteAllSessions(ctx)
	require.ErrorIs(t, err, boom)
	var row types.Session
	require.NoError(t, f.db.Where("id = ?", userDeleteSessionID).Take(&row).Error)
	require.Nil(t, f.assignment(t, userDeleteSessionID).ReleasedAt)
}

type listFailSessionRepo struct {
	interfaces.SessionRepository
	err error
}

func (r listFailSessionRepo) GetByTenantID(context.Context, uint64, string) ([]*types.Session, error) {
	return nil, r.err
}

// insertAfterListRepo returns the sessions that existed at list time, then
// inserts another row. Delete-all must not remove that later row.
type insertAfterListRepo struct {
	interfaces.SessionRepository
	db     *gorm.DB
	lateID string
}

func (r *insertAfterListRepo) GetByTenantID(
	ctx context.Context, tenantID uint64, userID string,
) ([]*types.Session, error) {
	listed, err := r.SessionRepository.GetByTenantID(ctx, tenantID, userID)
	if err != nil {
		return nil, err
	}
	err = r.db.Session(&gorm.Session{SkipHooks: true}).Create(&types.Session{
		ID: r.lateID, TenantID: tenantID, UserID: userID, Title: "late", OwnerAuthSource: types.AuthSourceWeb,
	}).Error
	if err != nil {
		return nil, err
	}
	return listed, nil
}

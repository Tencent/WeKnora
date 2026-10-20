package repository

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/Tencent/WeKnora/internal/database"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func newAllocationRepo(t *testing.T) (*sandboxAllocationRepository, *gorm.DB) {
	t.Helper()
	// SQLite allows one writer. Cap the pool at one connection and wait on a
	// busy lock so the first-writer test queues instead of failing with
	// "database table is locked". Real row-lock races run on Postgres in Task 20.
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_busy_timeout=5000", uuid.NewString())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(
		&types.Session{}, &types.SessionSandboxAssignment{}, &types.SandboxUserMachine{}, &types.Message{},
	))
	return NewSandboxAllocationRepository(db).(*sandboxAllocationRepository), db
}

func seedSession(t *testing.T, db *gorm.DB, id string, pin interfaces.PlacementPin) {
	t.Helper()
	s := &types.Session{
		ID: id, TenantID: 7, UserID: "u-1",
		SandboxConfigID: pin.ConfigID, SandboxConfigTenantID: pin.TenantID,
	}
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(s).Error)
}

func userAssignment(sessionID, digest string) *types.SessionSandboxAssignment {
	return &types.SessionSandboxAssignment{
		SessionID: sessionID, ConfigTenantID: 7, ConfigID: "cfg", MachineDigest: digest,
	}
}

func userMachine(digest string) *types.SandboxUserMachine {
	if digest == "" {
		return nil
	}
	return &types.SandboxUserMachine{
		MachineDigest: digest, OwnerTenantID: 7, OwnerID: "u-1",
		ConfigTenantID: 7, ConfigID: "cfg",
	}
}

// claim places a with the machine its digest names.
func claim(ctx context.Context, repo *sandboxAllocationRepository, a *types.SessionSandboxAssignment,
	pin interfaces.PlacementPin,
) (*types.SessionSandboxAssignment, error) {
	return repo.ClaimPlacement(ctx, a, userMachine(a.MachineDigest), pin)
}

// retireOne retires one session and returns its outcome.
func retireOne(ctx context.Context, repo *sandboxAllocationRepository, id string, at time.Time) (
	*types.SessionSandboxAssignment, bool, error,
) {
	out, err := repo.RetireSessions(ctx, []string{id}, at)
	if err != nil {
		return nil, false, err
	}
	return out[0].Assignment, out[0].MachineEmpty, nil
}

func readPin(t *testing.T, db *gorm.DB, id string) interfaces.PlacementPin {
	t.Helper()
	var s types.Session
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Where("id = ?", id).Take(&s).Error)
	return interfaces.PlacementPin{ConfigID: s.SandboxConfigID, TenantID: s.SandboxConfigTenantID}
}

func TestClaimPlacementWritesAssignmentAndPinTogether(t *testing.T) {
	repo, db := newAllocationRepo(t)
	ctx := context.Background()
	seedSession(t, db, "s-1", interfaces.PlacementPin{})

	got, err := claim(ctx, repo, userAssignment("s-1", "d-1"), interfaces.PlacementPin{})
	require.NoError(t, err)
	require.Equal(t, "d-1", got.MachineDigest)
	require.Equal(t, interfaces.PlacementPin{ConfigID: "cfg", TenantID: 7}, readPin(t, db, "s-1"))
	m, err := repo.GetUserMachine(ctx, "d-1")
	require.NoError(t, err)
	require.NotNil(t, m)
}

func TestClaimPlacementFirstWriterWinsAndPinFollowsTheWinner(t *testing.T) {
	repo, db := newAllocationRepo(t)
	ctx := context.Background()
	seedSession(t, db, "s-1", interfaces.PlacementPin{})
	var wg sync.WaitGroup
	results := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			a := userAssignment("s-1", "d-1")
			if i%2 == 1 {
				a.MachineDigest, a.ConfigID = "", "cfg-b"
			}
			w, err := claim(ctx, repo, a, interfaces.PlacementPin{})
			if errors.Is(err, interfaces.ErrPlacementConflict) {
				// The winner already pinned the row; a racer that re-reads adopts it.
				w, err = claim(ctx, repo, a, readPin(t, db, "s-1"))
			}
			require.NoError(t, err)
			results <- w.ConfigID
		}(i)
	}
	wg.Wait()
	close(results)
	winner := ""
	for c := range results {
		if winner == "" {
			winner = c
		}
		require.Equal(t, winner, c, "every racer ends on one persisted placement")
	}
	require.Equal(t, winner, readPin(t, db, "s-1").ConfigID, "the pin is the winner's projection")
}

func TestClaimPlacementRefusesAStalePinRead(t *testing.T) {
	repo, db := newAllocationRepo(t)
	seedSession(t, db, "s-1", interfaces.PlacementPin{ConfigID: "cfg-old", TenantID: 7})
	_, err := claim(context.Background(), repo, userAssignment("s-1", "d-1"), interfaces.PlacementPin{})
	require.ErrorIs(t, err, interfaces.ErrPlacementConflict)
}

func TestClaimPlacementOnADeletedSession(t *testing.T) {
	repo, _ := newAllocationRepo(t)
	_, err := claim(context.Background(), repo, userAssignment("missing", "d-1"), interfaces.PlacementPin{})
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestClaimPlacementComparesTheWholePin(t *testing.T) {
	repo, db := newAllocationRepo(t)
	ctx := context.Background()
	seedSession(t, db, "s-1", interfaces.PlacementPin{ConfigID: "cfg", TenantID: 9})
	_, err := claim(ctx, repo, userAssignment("s-1", "d-1"), interfaces.PlacementPin{ConfigID: "cfg", TenantID: 9})
	require.ErrorIs(t, err, interfaces.ErrPlacementConflict, "same config id in another tenant is another config")

	seedSession(t, db, "s-2", interfaces.PlacementPin{ConfigID: "cfg"})
	_, err = claim(ctx, repo, userAssignment("s-2", "d-1"), interfaces.PlacementPin{ConfigID: "cfg"})
	require.NoError(t, err)
	require.Equal(t, interfaces.PlacementPin{ConfigID: "cfg", TenantID: 7}, readPin(t, db, "s-2"),
		"a legacy pin without its tenant is completed from the assignment")
}

func TestRetireWithoutAnAssignmentBlocksALaterPlacement(t *testing.T) {
	repo, db := newAllocationRepo(t)
	ctx := context.Background()
	seedSession(t, db, "s-1", interfaces.PlacementPin{})
	out, err := repo.RetireSessions(ctx, []string{"s-1"}, time.Now().UTC())
	require.NoError(t, err)
	require.Nil(t, out[0].Assignment)
	tomb, err := repo.GetAssignment(ctx, "s-1")
	require.NoError(t, err)
	require.True(t, tomb.IsTombstone(), "an unplaced session is retired with a tombstone")

	again, err := repo.RetireSessions(ctx, []string{"s-1", "never-existed"}, time.Now().UTC())
	require.NoError(t, err, "retiring is idempotent")
	require.Nil(t, again[0].Assignment)
	missing, err := repo.GetAssignment(ctx, "never-existed")
	require.NoError(t, err)
	require.Nil(t, missing, "no tombstone for a session that does not exist")

	_, err = claim(ctx, repo, userAssignment("s-1", "d-1"), interfaces.PlacementPin{})
	require.ErrorIs(t, err, interfaces.ErrSessionRetired)
	live, _ := repo.CountLiveAssignments(ctx, "d-1")
	require.Zero(t, live)
}

func TestRetireAndPlacementInEitherOrderLeaveNothingLive(t *testing.T) {
	repo, db := newAllocationRepo(t)
	ctx := context.Background()

	placedFirst := uuid.NewString()
	seedSession(t, db, placedFirst, interfaces.PlacementPin{})
	_, err := claim(ctx, repo, userAssignment(placedFirst, "d-race"), interfaces.PlacementPin{})
	require.NoError(t, err)
	out, err := repo.RetireSessions(ctx, []string{placedFirst}, time.Now().UTC())
	require.NoError(t, err)
	require.NotNil(t, out[0].Assignment.CleanupDueAt, "a placement that won is released with its cleanup")

	retiredFirst := uuid.NewString()
	seedSession(t, db, retiredFirst, interfaces.PlacementPin{})
	_, err = repo.RetireSessions(ctx, []string{retiredFirst}, time.Now().UTC())
	require.NoError(t, err)
	_, err = claim(ctx, repo, userAssignment(retiredFirst, "d-race"), interfaces.PlacementPin{})
	require.ErrorIs(t, err, interfaces.ErrSessionRetired, "a placement that lost never lands")

	live, err := repo.CountLiveAssignments(ctx, "d-race")
	require.NoError(t, err)
	require.Zero(t, live)
}

func TestRetireSessionsDedupesAndKeepsOrder(t *testing.T) {
	repo, db := newAllocationRepo(t)
	ctx := context.Background()
	for _, id := range []string{"s-2", "s-1"} {
		seedSession(t, db, id, interfaces.PlacementPin{})
		_, err := claim(ctx, repo, userAssignment(id, "d-1"), interfaces.PlacementPin{})
		require.NoError(t, err)
	}
	out, err := repo.RetireSessions(ctx, []string{"s-2", "s-1", "s-2"}, time.Now().UTC())
	require.NoError(t, err)
	require.Len(t, out, 2, "duplicates are retired once")
	require.Equal(t, "s-2", out[0].SessionID, "results keep the caller's order")
	require.True(t, out[1].MachineEmpty)
}

func TestListEmptyUserMachinesPagesPastSkippedCandidates(t *testing.T) {
	repo, db := newAllocationRepo(t)
	ctx := context.Background()
	base := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Second)
	for i, d := range []string{"d-a", "d-b", "d-c"} {
		id := "s-" + d
		seedSession(t, db, id, interfaces.PlacementPin{})
		_, err := claim(ctx, repo, userAssignment(id, d), interfaces.PlacementPin{})
		require.NoError(t, err)
		_, _, err = retireOne(ctx, repo, id, base.Add(time.Duration(i)*time.Minute))
		require.NoError(t, err)
	}
	page1, err := repo.ListEmptyUserMachines(ctx, time.Now().UTC(), interfaces.EmptyMachineCursor{}, 2)
	require.NoError(t, err)
	require.Equal(t, []string{"d-a", "d-b"}, []string{page1[0].MachineDigest, page1[1].MachineDigest})
	last := page1[1]
	page2, err := repo.ListEmptyUserMachines(ctx, time.Now().UTC(),
		interfaces.EmptyMachineCursor{EmptySince: *last.EmptySince, Digest: last.MachineDigest}, 2)
	require.NoError(t, err)
	require.Len(t, page2, 1)
	require.Equal(t, "d-c", page2[0].MachineDigest)
}

func TestReleaseRecordsCleanupIntentAndEmptiesTheLastSession(t *testing.T) {
	repo, db := newAllocationRepo(t)
	ctx := context.Background()
	for _, id := range []string{"s-1", "s-2"} {
		seedSession(t, db, id, interfaces.PlacementPin{})
		_, err := claim(ctx, repo, userAssignment(id, "d-1"), interfaces.PlacementPin{})
		require.NoError(t, err)
	}
	now := time.Now().UTC().Truncate(time.Second)

	a, empty, err := retireOne(ctx, repo, "s-1", now)
	require.NoError(t, err)
	require.NotNil(t, a.ReleasedAt)
	require.False(t, empty)
	require.NotNil(t, a.CleanupDueAt, "the cleanup intent is written with the release")

	_, empty, err = retireOne(ctx, repo, "s-2", now)
	require.NoError(t, err)
	require.True(t, empty)
	m, _ := repo.GetUserMachine(ctx, "d-1")
	require.NotNil(t, m.EmptySince)

	require.NoError(t, repo.CompleteWorkspaceCleanup(ctx, "s-2"))
	_, _, err = retireOne(ctx, repo, "s-2", now.Add(time.Hour))
	require.NoError(t, err)
	row, _ := repo.GetAssignment(ctx, "s-2")
	require.Nil(t, row.CleanupDueAt, "a second release records nothing new")
	require.True(t, row.ReleasedAt.Equal(now), "nor moves the release time")
	require.NoError(t, repo.RetryWorkspaceCleanup(ctx, "s-2", now))
	row, _ = repo.GetAssignment(ctx, "s-2")
	require.Nil(t, row.CleanupDueAt, "a completed cleanup is not revived by a late retry")

	due, err := repo.ListDueWorkspaceCleanups(ctx, now.Add(time.Second), 10)
	require.NoError(t, err)
	require.Len(t, due, 1)
	require.Equal(t, "s-1", due[0].SessionID)
}

func TestReleaseOfASessionModeAssignmentHasNoCleanup(t *testing.T) {
	repo, db := newAllocationRepo(t)
	ctx := context.Background()
	seedSession(t, db, "s-1", interfaces.PlacementPin{})
	a := userAssignment("s-1", "")
	_, err := claim(ctx, repo, a, interfaces.PlacementPin{})
	require.NoError(t, err)
	released, empty, err := retireOne(ctx, repo, "s-1", time.Now().UTC())
	require.NoError(t, err)
	require.NotNil(t, released.ReleasedAt)
	require.Nil(t, released.CleanupDueAt)
	require.False(t, empty)
}

func TestDeleteUserMachineIfEmptySinceComparesTheClock(t *testing.T) {
	repo, db := newAllocationRepo(t)
	ctx := context.Background()
	seedSession(t, db, "s-1", interfaces.PlacementPin{})
	_, _ = claim(ctx, repo, userAssignment("s-1", "d-1"), interfaces.PlacementPin{})
	first := time.Now().UTC().Add(-10 * 24 * time.Hour).Truncate(time.Second)
	_, _, err := retireOne(ctx, repo, "s-1", first)
	require.NoError(t, err)

	// Someone joined and left after the reclaimer read the row: a new clock.
	seedSession(t, db, "s-2", interfaces.PlacementPin{})
	_, _ = claim(ctx, repo, userAssignment("s-2", "d-1"), interfaces.PlacementPin{})
	second := time.Now().UTC().Truncate(time.Second)
	_, _, _ = retireOne(ctx, repo, "s-2", second)

	deleted, err := repo.DeleteUserMachineIfEmptySince(ctx, "d-1", first)
	require.NoError(t, err)
	require.False(t, deleted, "the verified clock is gone; the new empty period must run its full window")

	deleted, err = repo.DeleteUserMachineIfEmptySince(ctx, "d-1", second)
	require.NoError(t, err)
	require.True(t, deleted)
}

func TestWorkspaceCleanupRetryAndComplete(t *testing.T) {
	repo, db := newAllocationRepo(t)
	ctx := context.Background()
	seedSession(t, db, "s-1", interfaces.PlacementPin{})
	_, _ = claim(ctx, repo, userAssignment("s-1", "d-1"), interfaces.PlacementPin{})
	_, _, err := retireOne(ctx, repo, "s-1", time.Now().UTC())
	require.NoError(t, err)

	require.NoError(t, repo.RetryWorkspaceCleanup(ctx, "s-1", time.Now().UTC().Add(time.Hour)))
	due, _ := repo.ListDueWorkspaceCleanups(ctx, time.Now().UTC(), 10)
	require.Empty(t, due)
	later, _ := repo.ListDueWorkspaceCleanups(ctx, time.Now().UTC().Add(2*time.Hour), 10)
	require.Len(t, later, 1)
	require.Equal(t, "d-1", later[0].MachineDigest, "the cleanup still knows its machine")
	require.NoError(t, repo.CompleteWorkspaceCleanup(ctx, "s-1"))
	later, _ = repo.ListDueWorkspaceCleanups(ctx, time.Now().UTC().Add(2*time.Hour), 10)
	require.Empty(t, later)
}

func TestHasSandboxCheckpoint(t *testing.T) {
	repo, db := newAllocationRepo(t)
	ctx := context.Background()
	ok, err := repo.HasSandboxCheckpoint(ctx, "s-1")
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, db.Create(&types.Message{
		ID: uuid.NewString(), SessionID: "s-1", Role: "assistant",
		SandboxCheckpoint: &types.SandboxCheckpoint{SandboxID: "sb", CommitSHA: "a"},
	}).Error)
	ok, err = repo.HasSandboxCheckpoint(ctx, "s-1")
	require.NoError(t, err)
	require.True(t, ok)
}

// A session already soft-deleted by an older replica must still be retired and
// its live assignment released. ClaimPlacement hides soft-deleted rows, so
// retirement is the path that reaches them.
func TestRetireSoftDeletedSession(t *testing.T) {
	repo, db := newAllocationRepo(t)
	ctx := context.Background()
	seedSession(t, db, "s-1", interfaces.PlacementPin{})
	_, err := claim(ctx, repo, userAssignment("s-1", "d-1"), interfaces.PlacementPin{})
	require.NoError(t, err)
	require.NoError(t, db.Where("id = ?", "s-1").Delete(&types.Session{}).Error)

	out, err := repo.RetireSessions(ctx, []string{"s-1"}, time.Now().UTC())
	require.NoError(t, err)
	require.NotNil(t, out[0].Assignment)
	require.NotNil(t, out[0].Assignment.ReleasedAt)
	require.NotNil(t, out[0].Assignment.CleanupDueAt)

	var row types.Session
	require.NoError(t, db.Unscoped().Where("id = ?", "s-1").Take(&row).Error)
	require.True(t, row.DeletedAt.Valid)

	_, err = claim(ctx, repo, userAssignment("s-1", "d-1"), interfaces.PlacementPin{})
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

// sqlCapture records the SQL GORM executes, in order.
type sqlCapture struct {
	logger.Interface
	mu   sync.Mutex
	sqls []string
}

func (l *sqlCapture) Trace(_ context.Context, _ time.Time, fc func() (string, int64), _ error) {
	sql, _ := fc()
	l.mu.Lock()
	l.sqls = append(l.sqls, sql)
	l.mu.Unlock()
}

func (l *sqlCapture) reset() {
	l.mu.Lock()
	l.sqls = nil
	l.mu.Unlock()
}

func (l *sqlCapture) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, len(l.sqls))
	copy(out, l.sqls)
	return out
}

func firstIndexContaining(sqls []string, needle string) int {
	for i, sql := range sqls {
		if strings.Contains(sql, needle) {
			return i
		}
	}
	return -1
}

// ClaimPlacement locks the session row before it writes the machine row.
func TestClaimPlacementTouchesSessionBeforeMachine(t *testing.T) {
	repo, db := newAllocationRepo(t)
	captured := &sqlCapture{Interface: logger.Discard}
	db.Logger = captured
	seedSession(t, db, "s-1", interfaces.PlacementPin{})
	captured.reset()
	_, err := claim(context.Background(), repo, userAssignment("s-1", "d-1"), interfaces.PlacementPin{})
	require.NoError(t, err)
	sqls := captured.snapshot()
	sessionIdx := firstIndexContaining(sqls, "FROM `sessions`")
	machineIdx := firstIndexContaining(sqls, "sandbox_user_machines")
	require.GreaterOrEqual(t, sessionIdx, 0)
	require.Greater(t, machineIdx, sessionIdx, "session row is locked before the machine row")
}

// The machine row is locked before the assignment is inserted, so a
// reclaimer counting live assignments can never miss a join in flight.
func TestClaimPlacementLocksTheMachineBeforeInsertingTheAssignment(t *testing.T) {
	repo, db := newAllocationRepo(t)
	ctx := context.Background()
	seedSession(t, db, "s-0", interfaces.PlacementPin{})
	_, err := claim(ctx, repo, userAssignment("s-0", "d-1"), interfaces.PlacementPin{})
	require.NoError(t, err)

	captured := &sqlCapture{Interface: logger.Discard}
	db.Logger = captured
	seedSession(t, db, "s-1", interfaces.PlacementPin{})
	captured.reset()
	_, err = claim(ctx, repo, userAssignment("s-1", "d-1"), interfaces.PlacementPin{})
	require.NoError(t, err)
	sqls := captured.snapshot()
	lock := -1
	for i, sql := range sqls {
		if strings.Contains(sql, "SELECT") && strings.Contains(sql, "FROM `sandbox_user_machines`") {
			lock = i
			break
		}
	}
	insert := firstIndexContaining(sqls, "INSERT INTO `session_sandbox_assignments`")
	require.GreaterOrEqual(t, lock, 0, "sql:\n%s", strings.Join(sqls, "\n"))
	require.Greater(t, insert, lock, "machine row is locked before the assignment insert")
}

// RetireSessions locks every session id in sorted order, then every machine
// digest in sorted order — the same sessions-then-machines order as ClaimPlacement.
func TestRetireLocksSessionsBeforeMachines(t *testing.T) {
	repo, db := newAllocationRepo(t)
	captured := &sqlCapture{Interface: logger.Discard}
	db.Logger = captured
	ctx := context.Background()
	for _, pair := range []struct{ id, digest string }{{"s-2", "d-b"}, {"s-1", "d-a"}} {
		seedSession(t, db, pair.id, interfaces.PlacementPin{})
		_, err := claim(ctx, repo, userAssignment(pair.id, pair.digest), interfaces.PlacementPin{})
		require.NoError(t, err)
	}
	captured.reset()
	_, err := repo.RetireSessions(ctx, []string{"s-2", "s-1"}, time.Now().UTC())
	require.NoError(t, err)
	sqls := captured.snapshot()

	var sessionLocks, machineLocks []string
	lastSessionLock := -1
	for i, sql := range sqls {
		if strings.Contains(sql, "FROM `sessions`") && strings.Contains(sql, "SELECT") {
			sessionLocks = append(sessionLocks, sql)
			lastSessionLock = i
		}
		if strings.Contains(sql, "FROM `sandbox_user_machines`") && strings.Contains(sql, "SELECT") {
			machineLocks = append(machineLocks, sql)
		}
	}
	require.GreaterOrEqual(t, len(sessionLocks), 2, "sql:\n%s", strings.Join(sqls, "\n"))
	require.GreaterOrEqual(t, len(machineLocks), 2, "sql:\n%s", strings.Join(sqls, "\n"))
	require.Contains(t, sessionLocks[0], `"s-1"`)
	require.Contains(t, sessionLocks[1], `"s-2"`)
	require.Contains(t, machineLocks[0], `"d-a"`)
	require.Contains(t, machineLocks[1], `"d-b"`)
	require.Greater(t, firstIndexContaining(sqls, "sandbox_user_machines"), lastSessionLock,
		"every session row is locked before any machine row")
}

// Postgres timestamptz equality is microsecond precision. Retirement truncates
// before writing so a clock read back compares equal to the value that was stored.
func TestRetireTruncatesTimestampsToMicroseconds(t *testing.T) {
	repo, db := newAllocationRepo(t)
	ctx := context.Background()
	seedSession(t, db, "s-1", interfaces.PlacementPin{})
	_, err := claim(ctx, repo, userAssignment("s-1", "d-1"), interfaces.PlacementPin{})
	require.NoError(t, err)

	at := time.Date(2026, 10, 13, 10, 11, 12, 123456789, time.UTC)
	want := at.Truncate(time.Microsecond)
	released, _, err := retireOne(ctx, repo, "s-1", at)
	require.NoError(t, err)
	require.True(t, released.CleanupDueAt.Equal(want))

	var stored types.SessionSandboxAssignment
	require.NoError(t, db.Where("session_id = ?", "s-1").Take(&stored).Error)
	require.NotNil(t, stored.ReleasedAt)
	require.True(t, stored.ReleasedAt.Equal(want), "released_at = %s", stored.ReleasedAt)

	deleted, err := repo.DeleteUserMachineIfEmptySince(ctx, "d-1", at)
	require.NoError(t, err)
	require.False(t, deleted, "nanoseconds below a microsecond must not match the stored clock")

	deleted, err = repo.DeleteUserMachineIfEmptySince(ctx, "d-1", want)
	require.NoError(t, err)
	require.True(t, deleted)
}

// The tests above build their schema with AutoMigrate from the entities. This
// one runs the repository on the schema the shipped SQLite migrations create,
// so an entity column the migrations do not have fails here.
func TestSandboxAllocationOnTheMigratedSQLiteSchema(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	require.NoError(t, err)
	t.Chdir(root)
	path := filepath.Join(t.TempDir(), "migrated.db")
	require.NoError(t, database.RunMigrationsWithOptions("sqlite3://unused",
		database.MigrationOptions{SQLiteDBPath: path}))
	db, err := gorm.Open(sqlite.Open(path+"?_busy_timeout=5000"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	repo := NewSandboxAllocationRepository(db).(*sandboxAllocationRepository)
	ctx := context.Background()

	for _, id := range []string{"s-1", "s-2", "s-3"} {
		require.NoError(t, db.Exec(
			"INSERT INTO sessions (id, tenant_id, user_id, owner_auth_source) VALUES (?, 7, 'u-1', 'web')", id).Error)
	}
	_, err = claim(ctx, repo, userAssignment("s-1", "d-1"), interfaces.PlacementPin{})
	require.NoError(t, err)
	session := userAssignment("s-2", "")
	_, err = claim(ctx, repo, session, interfaces.PlacementPin{})
	require.NoError(t, err)
	require.Equal(t, interfaces.PlacementPin{ConfigID: "cfg", TenantID: 7}, readPin(t, db, "s-1"))

	out, err := repo.RetireSessions(ctx, []string{"s-1", "s-2", "s-3"}, time.Now().UTC())
	require.NoError(t, err)
	require.True(t, out[0].MachineEmpty)
	require.NotNil(t, out[0].Assignment.CleanupDueAt)
	require.Nil(t, out[1].Assignment.CleanupDueAt)
	require.Nil(t, out[2].Assignment)
	_, err = claim(ctx, repo, userAssignment("s-3", "d-1"), interfaces.PlacementPin{})
	require.ErrorIs(t, err, interfaces.ErrSessionRetired)

	due, err := repo.ListDueWorkspaceCleanups(ctx, time.Now().UTC().Add(time.Second), 10)
	require.NoError(t, err)
	require.Len(t, due, 1)
	require.NoError(t, repo.RetryWorkspaceCleanup(ctx, "s-1", time.Now().UTC().Add(time.Hour)))
	require.NoError(t, repo.CompleteWorkspaceCleanup(ctx, "s-1"))

	m, err := repo.GetUserMachine(ctx, "d-1")
	require.NoError(t, err)
	require.NotNil(t, m.EmptySince)
	empty, err := repo.ListEmptyUserMachines(
		ctx, time.Now().UTC().Add(time.Second), interfaces.EmptyMachineCursor{}, 10,
	)
	require.NoError(t, err)
	require.Len(t, empty, 1)
	deleted, err := repo.DeleteUserMachineIfEmptySince(ctx, "d-1", *m.EmptySince)
	require.NoError(t, err)
	require.True(t, deleted)
}

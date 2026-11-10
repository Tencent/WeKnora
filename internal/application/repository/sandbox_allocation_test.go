package repository

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
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

// integrationEnv returns an integration dependency's address. Missing, the
// test is skipped — unless WEKNORA_REQUIRE_INTEGRATION=1, which turns the
// skip into a failure so an enablement run cannot pass by skipping.
func integrationEnv(t *testing.T, name string) string {
	t.Helper()
	v := strings.TrimSpace(os.Getenv(name))
	if v != "" {
		return v
	}
	if os.Getenv("WEKNORA_REQUIRE_INTEGRATION") == "1" {
		t.Fatalf("%s is required when WEKNORA_REQUIRE_INTEGRATION=1", name)
	}
	t.Skipf("set %s to run against a real dependency", name)
	return ""
}

// allocationBackends runs fn on SQLite and, when a disposable Postgres is
// configured, on Postgres. Session ids in fn must be unique: the Postgres
// database is shared and is not cleaned between runs.
func allocationBackends(t *testing.T, fn func(t *testing.T, repo *sandboxAllocationRepository, db *gorm.DB)) {
	t.Helper()
	t.Run("sqlite", func(t *testing.T) {
		repo, db := newAllocationRepo(t)
		fn(t, repo, db)
	})
	t.Run("postgres", func(t *testing.T) {
		dsn := integrationEnv(t, "WEKNORA_REPOSITORY_TEST_POSTGRES_DSN")
		db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		require.NoError(t, err)
		sqlDB, err := db.DB()
		require.NoError(t, err)
		t.Cleanup(func() { _ = sqlDB.Close() })
		require.NoError(t, db.AutoMigrate(
			&types.Session{}, &types.SessionSandboxAssignment{}, &types.SandboxUserMachine{}, &types.Message{},
		))
		fn(t, NewSandboxAllocationRepository(db).(*sandboxAllocationRepository), db)
	})
}

func seedSession(t *testing.T, db *gorm.DB, id string, pin interfaces.PlacementPin) {
	t.Helper()
	s := &types.Session{
		ID: id, TenantID: 7, UserID: "u-1",
		SandboxConfigID: pin.ConfigID, SandboxConfigTenantID: pin.TenantID,
	}
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(s).Error)
}

func TestDeleteSessionsRetiringChecksTenantAndOwner(t *testing.T) {
	allocationBackends(t, func(t *testing.T, repo *sandboxAllocationRepository, db *gorm.DB) {
		ctx := context.Background()
		own, otherOwner, otherTenant, legacy := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
		for _, row := range []*types.Session{
			{ID: own, TenantID: 7, UserID: "u-1"},
			{ID: otherOwner, TenantID: 7, UserID: "u-2"},
			{ID: otherTenant, TenantID: 8, UserID: "u-1"},
			{ID: legacy, TenantID: 7, UserID: ""},
		} {
			require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(row).Error)
		}
		retired, err := repo.DeleteSessionsRetiring(ctx, 7, map[string][]string{
			"u-1": {own, own, otherOwner, otherTenant, uuid.NewString()},
			"":    {legacy},
		}, time.Now())
		require.NoError(t, err)
		require.Len(t, retired, 2)
		for _, id := range []string{own, legacy} {
			var row types.Session
			require.ErrorIs(t, db.Where("id = ?", id).Take(&row).Error, gorm.ErrRecordNotFound)
			a, err := repo.GetAssignment(ctx, id)
			require.NoError(t, err)
			require.True(t, a.IsTombstone())
		}
		for _, id := range []string{otherOwner, otherTenant} {
			var row types.Session
			require.NoError(t, db.Where("id = ?", id).Take(&row).Error)
			a, err := repo.GetAssignment(ctx, id)
			require.NoError(t, err)
			require.Nil(t, a, "out-of-scope sessions must not even acquire retirement tombstones")
		}
	})
}

func TestDeleteSessionsRetiringRollsBackEveryOwnerOnDeleteFailure(t *testing.T) {
	allocationBackends(t, func(t *testing.T, repo *sandboxAllocationRepository, db *gorm.DB) {
		ctx := context.Background()
		placed, unplaced, digest := uuid.NewString(), uuid.NewString(), uuid.NewString()
		seedSession(t, db, placed, interfaces.PlacementPin{})
		require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&types.Session{
			ID: unplaced, TenantID: 7, UserID: "u-2",
		}).Error)
		_, err := claim(ctx, repo, userAssignment(placed, digest), interfaces.PlacementPin{})
		require.NoError(t, err)
		boom := errors.New("injected soft-delete failure")
		require.NoError(t, db.Callback().Delete().Before("gorm:delete").Register(
			"reject_session_delete", func(tx *gorm.DB) {
				if tx.Statement.Table == "sessions" {
					_ = tx.AddError(boom)
				}
			}))
		retired, err := repo.DeleteSessionsRetiring(ctx, 7, map[string][]string{
			"u-1": {placed}, "u-2": {unplaced},
		}, time.Now())
		require.ErrorIs(t, err, boom)
		require.Nil(t, retired)
		for _, id := range []string{placed, unplaced} {
			var row types.Session
			require.NoError(t, db.Where("id = ?", id).Take(&row).Error)
		}
		a, err := repo.GetAssignment(ctx, placed)
		require.NoError(t, err)
		require.Nil(t, a.ReleasedAt)
		require.Nil(t, a.CleanupDueAt)
		a, err = repo.GetAssignment(ctx, unplaced)
		require.NoError(t, err)
		require.Nil(t, a, "rollback must remove the new tombstone too")
		m, err := repo.GetUserMachine(ctx, digest)
		require.NoError(t, err)
		require.Nil(t, m.EmptySince)
	})
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
	allocationBackends(t, func(t *testing.T, repo *sandboxAllocationRepository, db *gorm.DB) {
		ctx := context.Background()
		id := uuid.NewString()
		digest := "d-" + uuid.NewString()
		seedSession(t, db, id, interfaces.PlacementPin{})
		var wg sync.WaitGroup
		results := make(chan string, 8)
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				a := userAssignment(id, digest)
				if i%2 == 1 {
					a.MachineDigest, a.ConfigID = "", "cfg-b"
				}
				w, err := claim(ctx, repo, a, interfaces.PlacementPin{})
				if errors.Is(err, interfaces.ErrPlacementConflict) {
					// The winner already pinned the row; a racer that re-reads adopts it.
					w, err = claim(ctx, repo, a, readPin(t, db, id))
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
		require.Equal(t, winner, readPin(t, db, id).ConfigID, "the pin is the winner's projection")
	})
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
	allocationBackends(t, func(t *testing.T, repo *sandboxAllocationRepository, db *gorm.DB) {
		ctx := context.Background()
		digest := "d-" + uuid.NewString()

		placedFirst := uuid.NewString()
		seedSession(t, db, placedFirst, interfaces.PlacementPin{})
		_, err := claim(ctx, repo, userAssignment(placedFirst, digest), interfaces.PlacementPin{})
		require.NoError(t, err)
		out, err := repo.RetireSessions(ctx, []string{placedFirst}, time.Now().UTC())
		require.NoError(t, err)
		require.NotNil(t, out[0].Assignment.CleanupDueAt, "a placement that won is released with its cleanup")

		retiredFirst := uuid.NewString()
		seedSession(t, db, retiredFirst, interfaces.PlacementPin{})
		_, err = repo.RetireSessions(ctx, []string{retiredFirst}, time.Now().UTC())
		require.NoError(t, err)
		_, err = claim(ctx, repo, userAssignment(retiredFirst, digest), interfaces.PlacementPin{})
		require.ErrorIs(t, err, interfaces.ErrSessionRetired, "a placement that lost never lands")

		live, err := repo.CountLiveAssignments(ctx, digest)
		require.NoError(t, err)
		require.Zero(t, live)
	})
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

func TestRetireOwnerSessionsEmptiesEveryMachineAndBlocksNewPlacements(t *testing.T) {
	repo, db := newAllocationRepo(t)
	ctx := context.Background()
	seedSession(t, db, "s-1", interfaces.PlacementPin{})
	seedSession(t, db, "s-2", interfaces.PlacementPin{})
	seedSession(t, db, "s-3", interfaces.PlacementPin{}) // not placed yet
	_, err := claim(ctx, repo, userAssignment("s-1", "d-1"), interfaces.PlacementPin{})
	require.NoError(t, err)
	other := userAssignment("s-2", "d-2")
	other.ConfigID = "cfg-b"
	_, err = claim(ctx, repo, other, interfaces.PlacementPin{})
	require.NoError(t, err)

	out, err := repo.RetireOwnerSessions(ctx, "u-1", time.Now().UTC())
	require.NoError(t, err)
	require.Len(t, out, 3)
	_, err = claim(ctx, repo, userAssignment("s-3", "d-1"), interfaces.PlacementPin{})
	require.ErrorIs(t, err, interfaces.ErrSessionRetired, "a request in flight cannot place after the account is gone")
	for _, d := range []string{"d-1", "d-2"} {
		m, _ := repo.GetUserMachine(ctx, d)
		require.NotNil(t, m.EmptySince, d)
		live, _ := repo.CountLiveAssignments(ctx, d)
		require.Zero(t, live, d)
	}
}

// A session row can leave the account while its live assignment stays on the
// account's machine. Retiring the account must release that assignment too,
// and a session of the account in another tenant is retired with the rest.
func TestRetireOwnerSessionsReleasesStrandedAssignmentsAndOtherTenants(t *testing.T) {
	repo, db := newAllocationRepo(t)
	ctx := context.Background()
	seedSession(t, db, "s-own", interfaces.PlacementPin{})
	_, err := claim(ctx, repo, userAssignment("s-own", "d-1"), interfaces.PlacementPin{})
	require.NoError(t, err)

	stranded := &types.Session{ID: "s-stranded", TenantID: 8, UserID: "u-2"}
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(stranded).Error)
	_, err = claim(ctx, repo, userAssignment("s-stranded", "d-1"), interfaces.PlacementPin{})
	require.NoError(t, err)

	otherTenant := &types.Session{ID: "s-other-tenant", TenantID: 9, UserID: "u-1"}
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(otherTenant).Error)

	out, err := repo.RetireOwnerSessions(ctx, "u-1", time.Now().UTC())
	require.NoError(t, err)
	got := make([]string, 0, len(out))
	for _, row := range out {
		got = append(got, row.SessionID)
	}
	require.ElementsMatch(t, []string{"s-own", "s-stranded", "s-other-tenant"}, got)

	_, err = claim(ctx, repo, userAssignment("s-other-tenant", "d-1"), interfaces.PlacementPin{})
	require.ErrorIs(t, err, interfaces.ErrSessionRetired)
	live, err := repo.CountLiveAssignments(ctx, "d-1")
	require.NoError(t, err)
	require.Zero(t, live)
	m, err := repo.GetUserMachine(ctx, "d-1")
	require.NoError(t, err)
	require.NotNil(t, m.EmptySince)
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
	allocationBackends(t, func(t *testing.T, repo *sandboxAllocationRepository, db *gorm.DB) {
		ctx := context.Background()
		firstID, secondID := uuid.NewString(), uuid.NewString()
		digest := "d-" + uuid.NewString()
		for _, id := range []string{firstID, secondID} {
			seedSession(t, db, id, interfaces.PlacementPin{})
			_, err := claim(ctx, repo, userAssignment(id, digest), interfaces.PlacementPin{})
			require.NoError(t, err)
		}
		now := time.Now().UTC().Truncate(time.Second)

		a, empty, err := retireOne(ctx, repo, firstID, now)
		require.NoError(t, err)
		require.NotNil(t, a.ReleasedAt)
		require.False(t, empty)
		require.NotNil(t, a.CleanupDueAt, "the cleanup intent is written with the release")

		_, empty, err = retireOne(ctx, repo, secondID, now)
		require.NoError(t, err)
		require.True(t, empty)
		m, _ := repo.GetUserMachine(ctx, digest)
		require.NotNil(t, m.EmptySince)

		require.NoError(t, repo.CompleteWorkspaceCleanup(ctx, secondID))
		_, _, err = retireOne(ctx, repo, secondID, now.Add(time.Hour))
		require.NoError(t, err)
		row, _ := repo.GetAssignment(ctx, secondID)
		require.Nil(t, row.CleanupDueAt, "a second release records nothing new")
		require.True(t, row.ReleasedAt.Equal(now), "nor moves the release time")
		require.NoError(t, repo.RetryWorkspaceCleanup(ctx, secondID, now))
		row, _ = repo.GetAssignment(ctx, secondID)
		require.Nil(t, row.CleanupDueAt, "a completed cleanup is not revived by a late retry")

		// Read this session back. A limited ListDueWorkspaceCleanups page is
		// filled by older due rows left on a shared Postgres.
		first, err := repo.GetAssignment(ctx, firstID)
		require.NoError(t, err)
		require.NotNil(t, first)
		require.NotNil(t, first.CleanupDueAt)
		require.False(t, first.CleanupDueAt.After(now.Add(time.Second)))
	})
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
	allocationBackends(t, func(t *testing.T, repo *sandboxAllocationRepository, db *gorm.DB) {
		ctx := context.Background()
		firstID, secondID := uuid.NewString(), uuid.NewString()
		digest := "d-" + uuid.NewString()
		seedSession(t, db, firstID, interfaces.PlacementPin{})
		_, _ = claim(ctx, repo, userAssignment(firstID, digest), interfaces.PlacementPin{})
		first := time.Now().UTC().Add(-10 * 24 * time.Hour).Truncate(time.Second)
		_, _, err := retireOne(ctx, repo, firstID, first)
		require.NoError(t, err)

		// Someone joined and left after the reclaimer read the row: a new clock.
		seedSession(t, db, secondID, interfaces.PlacementPin{})
		_, _ = claim(ctx, repo, userAssignment(secondID, digest), interfaces.PlacementPin{})
		second := time.Now().UTC().Truncate(time.Second)
		_, _, _ = retireOne(ctx, repo, secondID, second)

		deleted, err := repo.DeleteUserMachineIfEmptySince(ctx, digest, first)
		require.NoError(t, err)
		require.False(t, deleted, "the verified clock is gone; the new empty period must run its full window")

		deleted, err = repo.DeleteUserMachineIfEmptySince(ctx, digest, second)
		require.NoError(t, err)
		require.True(t, deleted)
	})
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

func TestPostgresRetireRacingFirstPlacementNeverLeavesALiveAssignment(t *testing.T) {
	dsn := integrationEnv(t, "WEKNORA_REPOSITORY_TEST_POSTGRES_DSN")
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&types.Session{}, &types.SessionSandboxAssignment{},
		&types.SandboxUserMachine{}))
	repo := NewSandboxAllocationRepository(db).(*sandboxAllocationRepository)
	ctx := context.Background()
	digest := "d-" + uuid.NewString()
	for i := 0; i < 50; i++ {
		id := uuid.NewString()
		seedSession(t, db, id, interfaces.PlacementPin{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, err := claim(ctx, repo, userAssignment(id, digest), interfaces.PlacementPin{})
			if err != nil {
				require.ErrorIs(t, err, interfaces.ErrSessionRetired)
			}
		}()
		go func() {
			defer wg.Done()
			_, err := repo.RetireSessions(ctx, []string{id}, time.Now().UTC())
			require.NoError(t, err)
		}()
		wg.Wait()
	}
	live, err := repo.CountLiveAssignments(ctx, digest)
	require.NoError(t, err)
	require.Zero(t, live, "whichever commits first, every session ends retired with nothing live")
}

func TestLockEmptyMachineRejectsAClearedClock(t *testing.T) {
	repo, db := newAllocationRepo(t)
	ctx := context.Background()
	since := time.Now().UTC().Truncate(time.Microsecond)
	row := &types.SandboxUserMachine{
		MachineDigest: "d-cleared", OwnerTenantID: 7, OwnerID: "u-1",
		ConfigTenantID: 7, ConfigID: "cfg-a", EmptySince: &since,
	}
	require.NoError(t, db.Create(row).Error)
	require.NoError(t, db.Exec(
		"UPDATE sandbox_user_machines SET empty_since = NULL WHERE machine_digest = ?", row.MachineDigest,
	).Error)

	lock, err := repo.LockEmptyMachine(ctx, row.MachineDigest)
	require.NoError(t, err)
	if lock != nil {
		lock.Abort()
	}
	require.Nil(t, lock)

	var kept types.SandboxUserMachine
	require.NoError(t, db.Where("machine_digest = ?", row.MachineDigest).Take(&kept).Error)
	require.Nil(t, kept.EmptySince)
}

func TestDeleteUserMachinesAndSandboxConfigRollsBackWhenTheConfigIsGone(t *testing.T) {
	_, db := newAllocationRepo(t)
	require.NoError(t, db.AutoMigrate(&types.TenantSandboxConfigEntity{}))
	ctx := context.Background()
	require.NoError(t, db.Create(&types.SandboxUserMachine{
		MachineDigest: "d-orphan", OwnerTenantID: 7, OwnerID: "u-1",
		ConfigTenantID: 7, ConfigID: "missing",
	}).Error)
	require.NoError(t, db.Create(&types.TenantSandboxConfigEntity{
		ID: "cfg-a", TenantID: 7, Name: "prod", SandboxType: "docker",
	}).Error)
	require.NoError(t, db.Create(&types.SandboxUserMachine{
		MachineDigest: "d-cfg", OwnerTenantID: 7, OwnerID: "u-1",
		ConfigTenantID: 7, ConfigID: "cfg-a",
	}).Error)

	err := DeleteUserMachinesAndSandboxConfig(ctx, db, 7, "missing")
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	var orphan types.SandboxUserMachine
	require.NoError(t, db.Where("machine_digest = ?", "d-orphan").Take(&orphan).Error)

	require.NoError(t, DeleteUserMachinesAndSandboxConfig(ctx, db, 7, "cfg-a"))
	var n int64
	require.NoError(t, db.Model(&types.SandboxUserMachine{}).Where("config_id = ?", "cfg-a").Count(&n).Error)
	require.Zero(t, n)
	var cfg types.TenantSandboxConfigEntity
	require.ErrorIs(t, db.Where("id = ?", "cfg-a").Take(&cfg).Error, gorm.ErrRecordNotFound)
}

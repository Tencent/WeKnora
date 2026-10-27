package repository

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newSessionRepositoryForForkTest(t *testing.T) (interfaces.SessionRepository, *gorm.DB) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&types.Session{}, &types.Message{}, &types.MessageArtifactRecord{}, &types.ForkSnapshotLease{},
	))

	return NewSessionRepository(db), db
}

func TestCreateForkedPersistsSessionAndCopiedMessages(t *testing.T) {
	repo, db := newSessionRepositoryForForkTest(t)
	ctx := context.Background()
	createdAt := time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)

	session := &types.Session{
		ID:                  "fork-1",
		TenantID:            1,
		UserID:              "u1",
		Title:               "原会话（分支）",
		SandboxConfigID:     "cfg-1",
		ParentSessionID:     "src",
		ForkedFromMessageID: "u-msg-2",
		ForkBootstrap: &types.ForkBootstrap{
			SnapshotID:      "snap-1",
			CommitSHA:       "sha1",
			SourceSandboxID: "sbx-1",
			CreatedAt:       createdAt,
		},
	}
	messages := []*types.Message{
		{ID: "copy-1", SessionID: "fork-1", Role: "user", CreatedAt: createdAt, KnowledgeID: ""},
		{ID: "copy-2", SessionID: "fork-1", Role: "assistant", CreatedAt: createdAt.Add(time.Second)},
	}

	require.NoError(t, repo.CreateForked(ctx, session, messages))

	got, err := repo.GetByID(ctx, 1, "fork-1")
	require.NoError(t, err)
	require.Equal(t, "fork-1", got.ID, "SkipHooks must keep the ID the service already assigned")
	require.Equal(t, "src", got.ParentSessionID)
	require.Equal(t, "u-msg-2", got.ForkedFromMessageID)
	require.Equal(t, "cfg-1", got.SandboxConfigID)
	require.NotNil(t, got.ForkBootstrap)
	require.Equal(t, "snap-1", got.ForkBootstrap.SnapshotID)
	require.Equal(t, "sha1", got.ForkBootstrap.CommitSHA)
	require.False(t, got.ForkBootstrap.Consumed())

	var stored []*types.Message
	require.NoError(t, db.Where("session_id = ?", "fork-1").Order("created_at ASC, id ASC").Find(&stored).Error)
	require.Equal(t, []string{"copy-1", "copy-2"}, messageIDs(stored))
}

func TestCreateForkedWithAssignmentPersistsPlacementAndRollsBackARepeat(t *testing.T) {
	repo, db := newSessionRepositoryForForkTest(t)
	require.NoError(t, db.AutoMigrate(&types.SessionSandboxAssignment{}, &types.SandboxUserMachine{}))
	ctx := context.Background()
	digest := strings.Repeat("ab", 32)
	const sessionID = "fork-user-1"
	session := &types.Session{
		ID: sessionID, TenantID: 1, UserID: "u1", Title: "branched",
		SandboxConfigID: "cfg-1", ParentSessionID: "src",
	}
	messages := []*types.Message{
		{ID: "copy-u", SessionID: sessionID, Role: "user", CreatedAt: time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)},
	}
	assignment := &types.SessionSandboxAssignment{
		SessionID: sessionID, ConfigTenantID: 1, ConfigID: "cfg-1", MachineDigest: digest,
	}
	machine := &types.SandboxUserMachine{
		MachineDigest: digest, OwnerTenantID: 1, OwnerID: "u1",
		ConfigTenantID: 1, ConfigID: "cfg-1",
	}

	require.NoError(t,
		repo.(*sessionRepository).CreateForkedWithAssignment(ctx, session, messages, assignment, machine))

	var row types.SessionSandboxAssignment
	require.NoError(t, db.Where("session_id = ?", sessionID).Take(&row).Error)
	require.Equal(t, digest, row.MachineDigest)
	require.Equal(t, "cfg-1", row.ConfigID)
	require.Nil(t, row.ReleasedAt)

	var stored types.SandboxUserMachine
	require.NoError(t, db.Where("machine_digest = ?", digest).Take(&stored).Error)
	require.Nil(t, stored.EmptySince)
	require.Equal(t, "u1", stored.OwnerID)

	var sessions int64
	require.NoError(t, db.Model(&types.Session{}).Where("id = ?", sessionID).Count(&sessions).Error)
	require.Equal(t, int64(1), sessions)

	again := &types.Session{ID: sessionID, TenantID: 1, UserID: "u1", Title: "again"}
	err := repo.(*sessionRepository).CreateForkedWithAssignment(ctx, again, []*types.Message{
		{ID: "copy-extra", SessionID: sessionID, Role: "user"},
	}, assignment, machine)
	require.Error(t, err)
	require.NoError(t, db.Model(&types.Session{}).Where("id = ?", sessionID).Count(&sessions).Error)
	require.Equal(t, int64(1), sessions, "the failed repeat rolls back and does not write a second session row")
	var extras int64
	require.NoError(t, db.Model(&types.Message{}).Where("id = ?", "copy-extra").Count(&extras).Error)
	require.Zero(t, extras)
}

func TestCreateForkedWithAssignmentLocksWebUserBeforeSessionAndMachine(t *testing.T) {
	repo, db := newSessionRepositoryForForkTest(t)
	require.NoError(t, db.AutoMigrate(&types.SessionSandboxAssignment{}, &types.SandboxUserMachine{}))
	migrateUsers(t, db)
	ctx := context.Background()
	owner := "11111111-1111-4111-8111-111111111111"
	require.NoError(t, db.Create(&types.User{
		ID: owner, Username: "fork-owner", Email: "fork-owner@example.com", PasswordHash: "x",
	}).Error)
	digest := strings.Repeat("cd", 32)
	const sessionID = "fork-locked-1"
	sawLock := false
	sessionVisible := false
	machineVisible := false
	afterWebUserLock = func(tx *gorm.DB) {
		sawLock = true
		var n int64
		require.NoError(t, tx.Model(&types.Session{}).Where("id = ?", sessionID).Count(&n).Error)
		sessionVisible = n > 0
		require.NoError(t, tx.Model(&types.SandboxUserMachine{}).Where("machine_digest = ?", digest).Count(&n).Error)
		machineVisible = n > 0
	}
	t.Cleanup(func() { afterWebUserLock = nil })

	err := repo.(*sessionRepository).CreateForkedWithAssignment(ctx, &types.Session{
		ID: sessionID, TenantID: 1, UserID: owner, Title: "locked",
	}, nil, &types.SessionSandboxAssignment{
		SessionID: sessionID, ConfigTenantID: 1, ConfigID: "cfg-1", MachineDigest: digest,
	}, &types.SandboxUserMachine{
		MachineDigest: digest, OwnerTenantID: 1, OwnerID: owner,
		ConfigTenantID: 1, ConfigID: "cfg-1",
	})
	afterWebUserLock = nil
	require.NoError(t, err)
	require.True(t, sawLock, "a canonical web owner is locked in the fork transaction")
	require.False(t, sessionVisible, "the user row is locked before the session insert")
	require.False(t, machineVisible, "the user row is locked before the machine row")
}

func TestCreateForkedAllowsEmptyHistory(t *testing.T) {
	repo, _ := newSessionRepositoryForForkTest(t)
	ctx := context.Background()

	session := &types.Session{ID: "fork-empty", TenantID: 1, UserID: "u1", Title: "first"}
	require.NoError(t, repo.CreateForked(ctx, session, nil))

	got, err := repo.GetByID(ctx, 1, "fork-empty")
	require.NoError(t, err)
	require.Equal(t, "fork-empty", got.ID)
}

func TestUpdateForkBootstrapSetsAndClears(t *testing.T) {
	repo, _ := newSessionRepositoryForForkTest(t)
	ctx := context.Background()
	session := &types.Session{ID: "s1", TenantID: 1, UserID: "u1", Title: "s"}
	require.NoError(t, repo.CreateForked(ctx, session, nil))

	bootstrap := &types.ForkBootstrap{
		SnapshotID:      "snap-1",
		CommitSHA:       "sha1",
		SourceSandboxID: "sbx-1",
		CreatedAt:       time.Date(2026, 9, 10, 8, 0, 0, 0, time.UTC),
	}
	require.NoError(t, repo.UpdateForkBootstrap(ctx, "s1", bootstrap))

	got, err := repo.GetByID(ctx, 1, "s1")
	require.NoError(t, err)
	require.NotNil(t, got.ForkBootstrap)
	require.Equal(t, "snap-1", got.ForkBootstrap.SnapshotID)

	require.NoError(t, repo.UpdateForkBootstrap(ctx, "s1", nil))

	got, err = repo.GetByID(ctx, 1, "s1")
	require.NoError(t, err)
	require.Nil(t, got.ForkBootstrap)
}

func TestListUnconsumedForksFiltersConsumedAndTooNew(t *testing.T) {
	repo, _ := newSessionRepositoryForForkTest(t)
	ctx := context.Background()
	cutoff := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	consumedAt := cutoff.Add(-time.Hour)

	stale := &types.Session{
		ID: "stale", TenantID: 1, UserID: "u1", Title: "stale",
		ForkBootstrap: &types.ForkBootstrap{
			SnapshotID: "snap-stale", CommitSHA: "sha", SourceSandboxID: "sbx",
			CreatedAt: cutoff.Add(-time.Hour),
		},
	}
	fresh := &types.Session{
		ID: "fresh", TenantID: 1, UserID: "u1", Title: "fresh",
		ForkBootstrap: &types.ForkBootstrap{
			SnapshotID: "snap-fresh", CommitSHA: "sha", SourceSandboxID: "sbx",
			CreatedAt: cutoff.Add(time.Minute),
		},
	}
	consumed := &types.Session{
		ID: "consumed", TenantID: 1, UserID: "u1", Title: "consumed",
		ForkBootstrap: &types.ForkBootstrap{
			SnapshotID: "snap-consumed", CommitSHA: "sha", SourceSandboxID: "sbx",
			CreatedAt:  cutoff.Add(-2 * time.Hour),
			ConsumedAt: &consumedAt,
		},
	}
	plain := &types.Session{ID: "plain", TenantID: 1, UserID: "u1", Title: "plain"}

	require.NoError(t, repo.CreateForked(ctx, stale, nil))
	require.NoError(t, repo.CreateForked(ctx, fresh, nil))
	require.NoError(t, repo.CreateForked(ctx, consumed, nil))
	require.NoError(t, repo.CreateForked(ctx, plain, nil))

	got, err := repo.ListUnconsumedForks(ctx, cutoff)
	require.NoError(t, err)

	ids := make([]string, 0, len(got))
	for _, s := range got {
		ids = append(ids, s.ID)
	}
	require.ElementsMatch(t, []string{"stale", "consumed"}, ids)
}

func TestListUnconsumedForksIncludesSoftDeletedUnopenedFork(t *testing.T) {
	repo, _ := newSessionRepositoryForForkTest(t)
	ctx := context.Background()
	cutoff := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	stale := &types.Session{
		ID: "deleted-fork", TenantID: 1, UserID: "u1", Title: "gone",
		ForkBootstrap: &types.ForkBootstrap{
			SnapshotID: "snap-deleted", CommitSHA: "sha", SourceSandboxID: "sbx",
			CreatedAt: cutoff.Add(-time.Hour),
		},
	}
	require.NoError(t, repo.CreateForked(ctx, stale, nil))
	n, err := repo.Delete(ctx, 1, "u1", "deleted-fork")
	require.NoError(t, err)
	require.Equal(t, int64(1), n)

	got, err := repo.ListUnconsumedForks(ctx, cutoff)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "deleted-fork", got[0].ID)
	require.Equal(t, "snap-deleted", got[0].ForkBootstrap.SnapshotID)
}

func TestHasOtherUnconsumedForkSnapshotIgnoresSelfAndConsumed(t *testing.T) {
	repo, _ := newSessionRepositoryForForkTest(t)
	ctx := context.Background()
	consumedAt := time.Now().UTC()

	self := &types.Session{
		ID: "self", TenantID: 1, UserID: "u1", Title: "self",
		ForkBootstrap: &types.ForkBootstrap{
			SnapshotID: "snap-shared", CommitSHA: "sha", SourceSandboxID: "sbx",
			CreatedAt: time.Now().UTC(),
		},
	}
	sibling := &types.Session{
		ID: "sibling", TenantID: 1, UserID: "u1", Title: "sibling",
		ForkBootstrap: &types.ForkBootstrap{
			SnapshotID: "snap-shared", CommitSHA: "sha-early", SourceSandboxID: "sbx",
			CreatedAt: time.Now().UTC(),
		},
	}
	otherSnap := &types.Session{
		ID: "other-snap", TenantID: 1, UserID: "u1", Title: "other",
		ForkBootstrap: &types.ForkBootstrap{
			SnapshotID: "snap-other", CommitSHA: "sha", SourceSandboxID: "sbx",
			CreatedAt: time.Now().UTC(),
		},
	}
	consumed := &types.Session{
		ID: "consumed", TenantID: 1, UserID: "u1", Title: "consumed",
		ForkBootstrap: &types.ForkBootstrap{
			SnapshotID: "snap-shared", CommitSHA: "sha", SourceSandboxID: "sbx",
			CreatedAt:  time.Now().UTC().Add(-time.Hour),
			ConsumedAt: &consumedAt,
		},
	}

	require.NoError(t, repo.CreateForked(ctx, self, nil))
	require.NoError(t, repo.CreateForked(ctx, sibling, nil))
	require.NoError(t, repo.CreateForked(ctx, otherSnap, nil))
	require.NoError(t, repo.CreateForked(ctx, consumed, nil))

	got, err := repo.HasOtherUnconsumedForkSnapshot(ctx, "snap-shared", "self")
	require.NoError(t, err)
	require.True(t, got)

	got, err = repo.HasOtherUnconsumedForkSnapshot(ctx, "snap-shared", "sibling")
	require.NoError(t, err)
	require.True(t, got, "self is still unconsumed")

	got, err = repo.HasOtherUnconsumedForkSnapshot(ctx, "snap-other", "other-snap")
	require.NoError(t, err)
	require.False(t, got, "only this session names snap-other")

	got, err = repo.HasOtherUnconsumedForkSnapshot(ctx, "snap-shared", "missing")
	require.NoError(t, err)
	require.True(t, got)

	got, err = repo.HasOtherUnconsumedForkSnapshot(ctx, "", "self")
	require.NoError(t, err)
	require.False(t, got)

	holders, err := repo.UnconsumedForkSnapshotHolders(ctx, "snap-shared")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"self", "sibling"}, holders)
}

func TestHasOtherUnconsumedForkSnapshotIgnoresSoftDeletedSibling(t *testing.T) {
	repo, _ := newSessionRepositoryForForkTest(t)
	ctx := context.Background()
	self := &types.Session{
		ID: "self", TenantID: 1, UserID: "u1", Title: "self",
		ForkBootstrap: &types.ForkBootstrap{
			SnapshotID: "snap-shared", CommitSHA: "sha", SourceSandboxID: "sbx",
			CreatedAt: time.Now().UTC(),
		},
	}
	sibling := &types.Session{
		ID: "sibling", TenantID: 1, UserID: "u1", Title: "sibling",
		ForkBootstrap: &types.ForkBootstrap{
			SnapshotID: "snap-shared", CommitSHA: "sha", SourceSandboxID: "sbx",
			CreatedAt: time.Now().UTC(),
		},
	}
	require.NoError(t, repo.CreateForked(ctx, self, nil))
	require.NoError(t, repo.CreateForked(ctx, sibling, nil))
	n, err := repo.Delete(ctx, 1, "u1", "sibling")
	require.NoError(t, err)
	require.Equal(t, int64(1), n)

	got, err := repo.HasOtherUnconsumedForkSnapshot(ctx, "snap-shared", "self")
	require.NoError(t, err)
	require.False(t, got, "a soft-deleted sibling no longer needs the snapshot")
}

func TestForkSnapshotLeaseRoundTripAndStaleListing(t *testing.T) {
	repo, _ := newSessionRepositoryForForkTest(t)
	ctx := context.Background()
	stale := time.Now().UTC().Add(-20 * time.Minute)
	fresh := time.Now().UTC()

	require.NoError(t, repo.CreateForkSnapshotLease(ctx, &types.ForkSnapshotLease{
		SnapshotID:      "snap-stale",
		TenantID:        9,
		SandboxConfigID: "cfg-1",
		CreatedAt:       stale,
	}))
	require.NoError(t, repo.CreateForkSnapshotLease(ctx, &types.ForkSnapshotLease{
		SnapshotID: "snap-fresh",
		CreatedAt:  fresh,
	}))

	got, err := repo.ListStaleForkSnapshotLeases(ctx, time.Now().UTC().Add(-15*time.Minute))
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "snap-stale", got[0].SnapshotID)
	require.Equal(t, uint64(9), got[0].TenantID)
	require.Equal(t, "cfg-1", got[0].SandboxConfigID)

	require.NoError(t, repo.DeleteForkSnapshotLease(ctx, "snap-stale"))
	got, err = repo.ListStaleForkSnapshotLeases(ctx, time.Now().UTC().Add(-15*time.Minute))
	require.NoError(t, err)
	require.Empty(t, got)
}

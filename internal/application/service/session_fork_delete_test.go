package service

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type deleteForkMessageRepo struct {
	interfaces.MessageRepository
}

func (deleteForkMessageRepo) GetKnowledgeIDsBySessionID(context.Context, string) ([]string, error) {
	return nil, nil
}

type deleteForkWebSearchState struct {
	interfaces.WebSearchStateService
}

func (deleteForkWebSearchState) DeleteWebSearchTempKBState(context.Context, string) error {
	return nil
}

func newSessionServiceForForkDeleteTest(t *testing.T) (*sessionService, *gorm.DB, *fakeForkSessionSnapshotDeleter) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.Session{}, &types.ForkSnapshotLease{}))
	snapshots := &fakeForkSessionSnapshotDeleter{}
	svc := &sessionService{
		sessionRepo:        repository.NewSessionRepository(db),
		messageRepo:        deleteForkMessageRepo{},
		webSearchStateRepo: deleteForkWebSearchState{},
		forkSnapshots:      snapshots,
	}
	return svc, db, snapshots
}

func TestDeleteSessionReleasesUnopenedForkSnapshot(t *testing.T) {
	svc, db, snapshots := newSessionServiceForForkDeleteTest(t)
	ctx := testSessionScopeContext(1, "u1")
	row := &types.Session{
		ID: "fork-1", TenantID: 1, UserID: "u1", Title: "branch", SandboxConfigID: "cfg-1",
		ForkBootstrap: &types.ForkBootstrap{
			SnapshotID: "snap-1", CommitSHA: "sha", SourceSandboxID: "sbx",
			CreatedAt: time.Now().UTC(),
		},
	}
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(row).Error)

	require.NoError(t, svc.DeleteSession(ctx, "fork-1"))

	require.Equal(t, []string{"snap-1"}, snapshots.forkDeleted)
	var n int64
	require.NoError(t, db.Model(&types.Session{}).Count(&n).Error)
	require.Zero(t, n)
}

func TestDeleteSessionKeepsSnapshotStillNeededBySibling(t *testing.T) {
	svc, db, snapshots := newSessionServiceForForkDeleteTest(t)
	ctx := testSessionScopeContext(1, "u1")
	bootstrap := &types.ForkBootstrap{
		SnapshotID: "snap-shared", CommitSHA: "sha", SourceSandboxID: "sbx",
		CreatedAt: time.Now().UTC(),
	}
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&types.Session{
		ID: "fork-1", TenantID: 1, UserID: "u1", Title: "branch",
		ForkBootstrap: bootstrap,
	}).Error)
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&types.Session{
		ID: "fork-2", TenantID: 1, UserID: "u1", Title: "sibling",
		ForkBootstrap: bootstrap,
	}).Error)

	require.NoError(t, svc.DeleteSession(ctx, "fork-1"))

	require.Empty(t, snapshots.forkDeleted)
}

func TestBatchDeleteSessionsReleasesUnopenedForkSnapshotsAfterAllRowsAreGone(t *testing.T) {
	svc, db, snapshots := newSessionServiceForForkDeleteTest(t)
	ctx := testSessionScopeContext(1, "u1")
	bootstrap := &types.ForkBootstrap{
		SnapshotID: "snap-shared", CommitSHA: "sha", SourceSandboxID: "sbx",
		CreatedAt: time.Now().UTC(),
	}
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&types.Session{
		ID: "fork-1", TenantID: 1, UserID: "u1", Title: "a", ForkBootstrap: bootstrap,
	}).Error)
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&types.Session{
		ID: "fork-2", TenantID: 1, UserID: "u1", Title: "b", ForkBootstrap: bootstrap,
	}).Error)

	require.NoError(t, svc.BatchDeleteSessions(ctx, []string{"fork-1", "fork-2"}))

	require.Equal(t, []string{"snap-shared"}, snapshots.forkDeleted,
		"shared snapshot must be deleted once, after both rows are gone")
}

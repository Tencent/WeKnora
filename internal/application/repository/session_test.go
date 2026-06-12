package repository

import (
	"context"
	"testing"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newSessionRepositoryForTest(t *testing.T) (interfaces.SessionRepository, *gorm.DB) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.Session{}))

	return NewSessionRepository(db), db
}

func createSessionForTest(t *testing.T, db *gorm.DB, tenantID uint64, userID string) *types.Session {
	t.Helper()

	session := &types.Session{
		TenantID: tenantID,
		UserID:   userID,
		Title:    userID + " session",
	}
	if userID == "" {
		session.Title = "legacy tenant session"
	}
	require.NoError(t, db.Create(session).Error)

	return session
}

func countActiveSessionsForTest(t *testing.T, db *gorm.DB, id string) int64 {
	t.Helper()

	var count int64
	require.NoError(t, db.Model(&types.Session{}).Where("id = ?", id).Count(&count).Error)
	return count
}

func sessionIDsForTest(sessions []*types.Session) []string {
	ids := make([]string, 0, len(sessions))
	for _, session := range sessions {
		ids = append(ids, session.ID)
	}
	return ids
}

func TestSessionRepositoryGetAndListHonorUserScope(t *testing.T) {
	repo, db := newSessionRepositoryForTest(t)
	ctx := context.Background()
	aliceSession := createSessionForTest(t, db, 1, "alice")
	bobSession := createSessionForTest(t, db, 1, "bob")
	legacySession := createSessionForTest(t, db, 1, "")
	_ = createSessionForTest(t, db, 2, "bob")

	_, err := repo.Get(ctx, 1, "bob", aliceSession.ID)
	require.ErrorIs(t, err, apperrors.ErrSessionNotFound)

	got, err := repo.Get(ctx, 1, "bob", bobSession.ID)
	require.NoError(t, err)
	require.Equal(t, bobSession.ID, got.ID)

	got, err = repo.Get(ctx, 1, "bob", legacySession.ID)
	require.NoError(t, err)
	require.Equal(t, legacySession.ID, got.ID)

	sessions, err := repo.GetByTenantID(ctx, 1, "bob")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{bobSession.ID, legacySession.ID}, sessionIDsForTest(sessions))

	paged, total, err := repo.GetPagedByTenantID(ctx, 1, "bob", &types.Pagination{Page: 1, PageSize: 10})
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	require.ElementsMatch(t, []string{bobSession.ID, legacySession.ID}, sessionIDsForTest(paged))
}

func TestSessionRepositoryUpdateHonorsUserScope(t *testing.T) {
	repo, db := newSessionRepositoryForTest(t)
	ctx := context.Background()
	aliceSession := createSessionForTest(t, db, 1, "alice")

	rows, err := repo.Update(ctx, &types.Session{
		ID:       aliceSession.ID,
		TenantID: aliceSession.TenantID,
		Title:    "bob update attempt",
	}, "bob")
	require.NoError(t, err)
	require.Zero(t, rows)

	var unchanged types.Session
	require.NoError(t, db.First(&unchanged, "id = ?", aliceSession.ID).Error)
	require.Equal(t, aliceSession.Title, unchanged.Title)

	rows, err = repo.Update(ctx, &types.Session{
		ID:       aliceSession.ID,
		TenantID: aliceSession.TenantID,
		Title:    "alice updated session",
	}, "alice")
	require.NoError(t, err)
	require.EqualValues(t, 1, rows)

	var changed types.Session
	require.NoError(t, db.First(&changed, "id = ?", aliceSession.ID).Error)
	require.Equal(t, "alice updated session", changed.Title)
}

func TestSessionRepositoryDeleteHonorsUserScope(t *testing.T) {
	repo, db := newSessionRepositoryForTest(t)
	ctx := context.Background()
	aliceSession := createSessionForTest(t, db, 1, "alice")
	bobSession := createSessionForTest(t, db, 1, "bob")

	rows, err := repo.Delete(ctx, 1, "bob", aliceSession.ID)
	require.NoError(t, err)
	require.Zero(t, rows)
	require.EqualValues(t, 1, countActiveSessionsForTest(t, db, aliceSession.ID))

	rows, err = repo.Delete(ctx, 1, "bob", bobSession.ID)
	require.NoError(t, err)
	require.EqualValues(t, 1, rows)
	require.Zero(t, countActiveSessionsForTest(t, db, bobSession.ID))
}

func TestSessionRepositoryBatchDeleteHonorsUserScope(t *testing.T) {
	repo, db := newSessionRepositoryForTest(t)
	ctx := context.Background()
	aliceSession := createSessionForTest(t, db, 1, "alice")
	bobSession := createSessionForTest(t, db, 1, "bob")
	legacySession := createSessionForTest(t, db, 1, "")

	rows, err := repo.BatchDelete(ctx, 1, "bob", []string{aliceSession.ID, bobSession.ID, legacySession.ID})
	require.NoError(t, err)
	require.EqualValues(t, 2, rows)
	require.EqualValues(t, 1, countActiveSessionsForTest(t, db, aliceSession.ID))
	require.Zero(t, countActiveSessionsForTest(t, db, bobSession.ID))
	require.Zero(t, countActiveSessionsForTest(t, db, legacySession.ID))
}

func TestSessionRepositoryDeleteAllHonorsUserScope(t *testing.T) {
	repo, db := newSessionRepositoryForTest(t)
	ctx := context.Background()
	aliceSession := createSessionForTest(t, db, 1, "alice")
	bobSession := createSessionForTest(t, db, 1, "bob")
	legacySession := createSessionForTest(t, db, 1, "")
	otherTenantSession := createSessionForTest(t, db, 2, "bob")

	rows, err := repo.DeleteAllByTenantID(ctx, 1, "bob")
	require.NoError(t, err)
	require.EqualValues(t, 2, rows)
	require.EqualValues(t, 1, countActiveSessionsForTest(t, db, aliceSession.ID))
	require.Zero(t, countActiveSessionsForTest(t, db, bobSession.ID))
	require.Zero(t, countActiveSessionsForTest(t, db, legacySession.ID))
	require.EqualValues(t, 1, countActiveSessionsForTest(t, db, otherTenantSession.ID))
}

// im_channel_sessions row for QueryPaged source-filter tests. Mirrors the columns
// the LEFT JOIN projects; kept local to avoid importing internal/im.
type testIMChannelSession struct {
	ID          string `gorm:"primaryKey"`
	SessionID   string
	Platform    string
	ChatID      string
	ThreadID    string
	UserID      string
	AgentID     string
	IMChannelID string `gorm:"column:im_channel_id"`
	TenantID    uint64
	DeletedAt   gorm.DeletedAt
}

func (testIMChannelSession) TableName() string { return "im_channel_sessions" }

func listItemIDsForTest(items []*types.SessionListItem) []string {
	ids := make([]string, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	return ids
}

// A /clear (or session recycling) soft-deletes the IM mapping and starts a fresh
// session. The old session must stay under its IM platform, not leak into "web"
// (which would happen if the source-filter join excluded soft-deleted mappings).
func TestSessionRepositoryQueryPagedKeepsClearedIMSessionsOutOfWeb(t *testing.T) {
	repo, db := newSessionRepositoryForTest(t)
	require.NoError(t, db.AutoMigrate(&testIMChannelSession{}))
	ctx := context.Background()

	web := createSessionForTest(t, db, 1, "alice")     // never bound to IM -> web
	active := createSessionForTest(t, db, 1, "alice")  // active IM mapping
	cleared := createSessionForTest(t, db, 1, "alice") // IM mapping soft-deleted by /clear

	require.NoError(t, db.Create(&testIMChannelSession{
		ID: "m-active", SessionID: active.ID, Platform: "wecom", TenantID: 1,
	}).Error)
	clearedMapping := &testIMChannelSession{
		ID: "m-cleared", SessionID: cleared.ID, Platform: "wecom", TenantID: 1,
	}
	require.NoError(t, db.Create(clearedMapping).Error)
	require.NoError(t, db.Delete(clearedMapping).Error) // soft-delete, as /clear does

	webItems, _, err := repo.QueryPaged(ctx, &types.SessionListQuery{
		TenantID: 1, UserID: "alice", Source: "web", Page: 1, PageSize: 50,
	})
	require.NoError(t, err)
	require.Equal(t, []string{web.ID}, listItemIDsForTest(webItems),
		"web must exclude sessions that ever had an IM mapping, including cleared ones")

	wecomItems, _, err := repo.QueryPaged(ctx, &types.SessionListQuery{
		TenantID: 1, UserID: "alice", Source: "wecom", Page: 1, PageSize: 50,
	})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{active.ID, cleared.ID}, listItemIDsForTest(wecomItems),
		"wecom must include both the active and the cleared IM session")
}

func TestSessionRepositoryQueryPagedSplitsWebAndEmbedSessions(t *testing.T) {
	repo, db := newSessionRepositoryForTest(t)
	require.NoError(t, db.AutoMigrate(&testIMChannelSession{}))
	ctx := context.Background()

	web := createSessionForTest(t, db, 1, "alice")
	embed := createSessionForTest(t, db, 1, "alice")
	require.NoError(t, db.Model(&types.Session{}).Where("id = ?", embed.ID).
		Update("description", types.EmbedSessionMarkerPrefix+"ch-1").Error)

	webItems, _, err := repo.QueryPaged(ctx, &types.SessionListQuery{
		TenantID: 1, UserID: "alice", Source: "web", Page: 1, PageSize: 50,
	})
	require.NoError(t, err)
	require.Equal(t, []string{web.ID}, listItemIDsForTest(webItems))

	embedItems, _, err := repo.QueryPaged(ctx, &types.SessionListQuery{
		TenantID: 1, UserID: "alice", Source: "embed:ch-1", Page: 1, PageSize: 50,
	})
	require.NoError(t, err)
	require.Equal(t, []string{embed.ID}, listItemIDsForTest(embedItems))
}

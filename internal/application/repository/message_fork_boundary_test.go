package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newMessageRepositoryForForkTest(t *testing.T) (*messageRepository, *gorm.DB) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.Message{}, &types.MessageArtifactRecord{}))

	return &messageRepository{db: db}, db
}

func seedMessage(t *testing.T, db *gorm.DB, id, sessionID, role string, at time.Time) {
	t.Helper()
	// Message.BeforeCreate always assigns a fresh UUID, which would discard
	// the IDs these tests use as both expected results and the composite
	// cursor. Skip hooks so the seeded identity is the one the queries see.
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&types.Message{
		ID:        id,
		SessionID: sessionID,
		Role:      role,
		CreatedAt: at,
	}).Error)
}

// Same-millisecond messages must come back in a deterministic order, otherwise
// a fork boundary drawn at one of them is not reproducible.
func TestGetMessagesBySessionBreaksTimestampTiesById(t *testing.T) {
	repo, db := newMessageRepositoryForForkTest(t)
	ctx := context.Background()
	at := time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)

	seedMessage(t, db, "m-c", "s1", "assistant", at)
	seedMessage(t, db, "m-a", "s1", "user", at)
	seedMessage(t, db, "m-b", "s1", "assistant", at)

	got, err := repo.GetMessagesBySession(ctx, "s1", 1, 10)
	require.NoError(t, err)
	require.Equal(t, []string{"m-a", "m-b", "m-c"}, messageIDs(got))
}

func TestListMessagesBySessionUpToExcludesBoundaryAndLater(t *testing.T) {
	repo, db := newMessageRepositoryForForkTest(t)
	ctx := context.Background()
	base := time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)

	seedMessage(t, db, "m1", "s1", "user", base)
	seedMessage(t, db, "m2", "s1", "assistant", base.Add(time.Second))
	seedMessage(t, db, "m3", "s1", "user", base.Add(2*time.Second))
	seedMessage(t, db, "m4", "s1", "assistant", base.Add(3*time.Second))
	seedMessage(t, db, "other", "s2", "user", base)

	got, err := repo.ListMessagesBySessionUpTo(ctx, "s1", base.Add(2*time.Second), "m3")
	require.NoError(t, err)
	require.Equal(t, []string{"m1", "m2"}, messageIDs(got))
}

// The boundary is a composite (created_at, id) cursor, so a message sharing the
// boundary's timestamp is included only when its ID sorts before the boundary's.
func TestListMessagesBySessionUpToUsesCompositeCursor(t *testing.T) {
	repo, db := newMessageRepositoryForForkTest(t)
	ctx := context.Background()
	at := time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)

	seedMessage(t, db, "m-a", "s1", "user", at)
	seedMessage(t, db, "m-b", "s1", "assistant", at)
	seedMessage(t, db, "m-c", "s1", "user", at)

	got, err := repo.ListMessagesBySessionUpTo(ctx, "s1", at, "m-c")
	require.NoError(t, err)
	require.Equal(t, []string{"m-a", "m-b"}, messageIDs(got))
}

func TestListMessagesBySessionUpToSkipsSoftDeleted(t *testing.T) {
	repo, db := newMessageRepositoryForForkTest(t)
	ctx := context.Background()
	base := time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)

	seedMessage(t, db, "m1", "s1", "user", base)
	seedMessage(t, db, "m2", "s1", "assistant", base.Add(time.Second))
	seedMessage(t, db, "boundary", "s1", "user", base.Add(2*time.Second))
	require.NoError(t, db.Delete(&types.Message{}, "id = ?", "m2").Error)

	got, err := repo.ListMessagesBySessionUpTo(ctx, "s1", base.Add(2*time.Second), "boundary")
	require.NoError(t, err)
	require.Equal(t, []string{"m1"}, messageIDs(got))
}

func TestRecordRestoredArtifactMtimeOnlyTouchesThisSession(t *testing.T) {
	repo, db := newMessageRepositoryForForkTest(t)
	ctx := context.Background()
	at := time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)
	oldMod := at
	newMod := at.Add(time.Minute)
	hash := "abc"
	art := types.MessageArtifacts{{
		SourcePath: "/workspace/output/report.pptx", FileName: "report.pptx",
		ModTime: oldMod, FileSize: 4, ContentHash: hash,
	}}
	seeded := []*types.Message{
		{ID: "parent-msg", SessionID: "parent", Role: "assistant", CreatedAt: at, Artifacts: art},
		{ID: "fork-msg", SessionID: "fork-1", Role: "assistant", CreatedAt: at, Artifacts: art},
	}
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(seeded).Error)
	require.NoError(t, insertMessageArtifacts(db, seeded))

	require.NoError(t, repo.RecordRestoredArtifactMtime(ctx, "fork-1", "/workspace/output/report.pptx", newMod, hash))

	parent, err := repo.GetMessage(ctx, "parent", "parent-msg")
	require.NoError(t, err)
	child, err := repo.GetMessage(ctx, "fork-1", "fork-msg")
	require.NoError(t, err)
	require.True(t, parent.Artifacts[0].ModTime.Equal(oldMod), "parent artifact mtime must stay put")
	require.True(t, child.Artifacts[0].ModTime.Equal(newMod), "fork artifact mtime should follow the restored sandbox")
}

func messageIDs(messages []*types.Message) []string {
	ids := make([]string, 0, len(messages))
	for _, m := range messages {
		ids = append(ids, m.ID)
	}
	return ids
}

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestGetLatestContextCheckpointReturnsTheNewestInTheSession(t *testing.T) {
	repo, db := newMessageRepositoryForForkTest(t)
	ctx := context.Background()
	base := time.Date(2026, 9, 18, 9, 0, 0, 0, time.UTC)

	none, err := repo.GetLatestContextCheckpoint(ctx, "s1")
	require.NoError(t, err)
	require.Nil(t, none)

	seedMessage(t, db, "a1", "s1", "assistant", base)
	seedMessage(t, db, "a2", "s1", "assistant", base.Add(time.Minute))
	seedMessage(t, db, "a3", "s1", "assistant", base.Add(2*time.Minute))
	seedMessage(t, db, "other", "s2", "assistant", base.Add(time.Hour))

	require.NoError(t, repo.UpdateMessageContextCheckpoint(ctx, "s1", "a1",
		&types.ContextCheckpoint{Summary: "through one"}))
	require.NoError(t, repo.UpdateMessageContextCheckpoint(ctx, "s1", "a2",
		&types.ContextCheckpoint{Summary: "through two"}))
	require.NoError(t, repo.UpdateMessageContextCheckpoint(ctx, "s2", "other",
		&types.ContextCheckpoint{Summary: "another session"}))

	got, err := repo.GetLatestContextCheckpoint(ctx, "s1")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "a2", got.ID)
	require.Equal(t, "through two", got.ContextCheckpoint.Summary)
	require.True(t, got.CreatedAt.Equal(base.Add(time.Minute)))

	// A deleted turn takes its checkpoint with it.
	require.NoError(t, repo.DeleteMessage(ctx, "s1", "a2"))
	got, err = repo.GetLatestContextCheckpoint(ctx, "s1")
	require.NoError(t, err)
	require.Equal(t, "a1", got.ID)
}

// The session guard keeps a stray turn ID from writing into another session,
// and the role guard keeps it off user rows.
func TestUpdateMessageContextCheckpointIsScopedToTheSessionsAssistantRows(t *testing.T) {
	repo, db := newMessageRepositoryForForkTest(t)
	ctx := context.Background()
	at := time.Date(2026, 9, 18, 9, 0, 0, 0, time.UTC)

	seedMessage(t, db, "u1", "s1", "user", at)
	seedMessage(t, db, "a1", "s1", "assistant", at)

	require.NoError(t, repo.UpdateMessageContextCheckpoint(ctx, "s2", "a1",
		&types.ContextCheckpoint{Summary: "wrong session"}))
	require.NoError(t, repo.UpdateMessageContextCheckpoint(ctx, "s1", "u1",
		&types.ContextCheckpoint{Summary: "user row"}))

	got, err := repo.GetLatestContextCheckpoint(ctx, "s1")
	require.NoError(t, err)
	require.Nil(t, got)
}

// Messages are also saved whole (UpdateMessage) by paths that loaded them
// before a checkpoint was written. Such a save must not clear it.
func TestUpdateMessageKeepsAnExistingContextCheckpoint(t *testing.T) {
	repo, db := newMessageRepositoryForForkTest(t)
	ctx := context.Background()
	at := time.Date(2026, 9, 18, 9, 0, 0, 0, time.UTC)

	seedMessage(t, db, "a1", "s1", "assistant", at)
	stale, err := repo.GetMessage(ctx, "s1", "a1")
	require.NoError(t, err)

	require.NoError(t, repo.UpdateMessageContextCheckpoint(ctx, "s1", "a1",
		&types.ContextCheckpoint{Summary: "kept"}))
	stale.Content = "edited"
	require.NoError(t, repo.UpdateMessage(ctx, stale))

	got, err := repo.GetLatestContextCheckpoint(ctx, "s1")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "kept", got.ContextCheckpoint.Summary)
}

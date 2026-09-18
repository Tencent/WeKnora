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
// and the role guard keeps it off user rows. Either way nothing was saved, and
// the caller hears so instead of logging a checkpoint that does not exist.
func TestUpdateMessageContextCheckpointIsScopedToTheSessionsAssistantRows(t *testing.T) {
	repo, db := newMessageRepositoryForForkTest(t)
	ctx := context.Background()
	at := time.Date(2026, 9, 18, 9, 0, 0, 0, time.UTC)

	seedMessage(t, db, "u1", "s1", "user", at)
	seedMessage(t, db, "a1", "s1", "assistant", at)

	require.Error(t, repo.UpdateMessageContextCheckpoint(ctx, "s2", "a1",
		&types.ContextCheckpoint{Summary: "wrong session"}), "a write that matches no row is not a save")
	require.Error(t, repo.UpdateMessageContextCheckpoint(ctx, "s1", "u1",
		&types.ContextCheckpoint{Summary: "user row"}))
	require.Error(t, repo.UpdateMessageContextCheckpoint(ctx, "s1", "gone",
		&types.ContextCheckpoint{Summary: "deleted turn"}))

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

// Agent history pages a session backwards. Messages sharing a timestamp are
// ordered by ID, so a page boundary among them neither skips nor repeats one.
func TestListMessagesBySessionBeforeCursorPagesBackwardsWithoutGaps(t *testing.T) {
	repo, db := newMessageRepositoryForForkTest(t)
	ctx := context.Background()
	base := time.Date(2026, 9, 18, 9, 0, 0, 0, time.UTC)

	seedMessage(t, db, "m1", "s1", "user", base)
	seedMessage(t, db, "m2", "s1", "assistant", base.Add(time.Second))
	seedMessage(t, db, "m3-a", "s1", "user", base.Add(2*time.Second))
	seedMessage(t, db, "m3-b", "s1", "assistant", base.Add(2*time.Second))
	seedMessage(t, db, "m4", "s1", "user", base.Add(3*time.Second))
	seedMessage(t, db, "other", "s2", "user", base.Add(time.Hour))
	// Sorts inside the cursor's tie range; only the session filter keeps it out.
	seedMessage(t, db, "a-other", "s2", "user", base.Add(2*time.Second))

	first, err := repo.ListMessagesBySessionBeforeCursor(ctx, "s1", time.Time{}, "", 2)
	require.NoError(t, err)
	require.Equal(t, []string{"m4", "m3-b"}, messageIDs(first))

	oldest := first[len(first)-1]
	second, err := repo.ListMessagesBySessionBeforeCursor(ctx, "s1", oldest.CreatedAt, oldest.ID, 2)
	require.NoError(t, err)
	require.Equal(t, []string{"m3-a", "m2"}, messageIDs(second))

	oldest = second[len(second)-1]
	last, err := repo.ListMessagesBySessionBeforeCursor(ctx, "s1", oldest.CreatedAt, oldest.ID, 2)
	require.NoError(t, err)
	require.Equal(t, []string{"m1"}, messageIDs(last))
}

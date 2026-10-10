package repository

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestDeleteMessageInvalidatesDependentCheckpoints(t *testing.T) {
	repo, db := newMessageRepositoryForForkTest(t)
	ctx := t.Context()
	at := time.Now().Add(-time.Hour)
	seedMessage(t, db, "before", "s", "assistant", at)
	seedMessage(t, db, "deleted", "s", "user", at.Add(time.Minute))
	seedMessage(t, db, "after", "s", "assistant", at.Add(2*time.Minute))
	seedMessage(t, db, "other", "other", "assistant", at.Add(3*time.Minute))
	for _, pair := range [][2]string{{"s", "before"}, {"s", "after"}, {"other", "other"}} {
		require.NoError(t, repo.UpdateMessageContextCheckpoint(ctx, pair[0], pair[1],
			&types.ContextCheckpoint{Summary: pair[1]}))
	}
	require.NoError(t, repo.DeleteMessage(ctx, "s", "deleted"))
	checkpoint, err := repo.GetLatestContextCheckpoint(ctx, "s")
	require.NoError(t, err)
	require.NotNil(t, checkpoint)
	require.Equal(t, "before", checkpoint.ID)
	other, err := repo.GetLatestContextCheckpoint(ctx, "other")
	require.NoError(t, err)
	require.NotNil(t, other)
	require.Equal(t, "other", other.ID)
}

func TestCheckpointRejectsSummaryStartedBeforeDeletion(t *testing.T) {
	repo, db := newMessageRepositoryForForkTest(t)
	ctx := t.Context()
	at := time.Now().Add(-time.Hour)
	seedMessage(t, db, "deleted", "s", "user", at)
	seedMessage(t, db, "target", "s", "assistant", at.Add(time.Minute))
	require.NoError(t, repo.DeleteMessage(ctx, "s", "deleted"))
	require.ErrorIs(t, repo.UpdateMessageContextCheckpoint(ctx, "s", "target",
		&types.ContextCheckpoint{Summary: "stale"}), types.ErrStaleContextCheckpoint)
	checkpoint, err := repo.GetLatestContextCheckpoint(ctx, "s")
	require.NoError(t, err)
	require.Nil(t, checkpoint)
	count, err := repo.CountDeletedMessagesBySession(ctx, "s")
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
	require.NoError(t, repo.UpdateMessageContextCheckpoint(ctx, "s", "target", &types.ContextCheckpoint{
		Summary: "rebuilt from surviving history", SourceDeletedCount: count,
	}))
	checkpoint, err = repo.GetLatestContextCheckpoint(ctx, "s")
	require.NoError(t, err)
	require.Equal(t, "rebuilt from surviving history", checkpoint.ContextCheckpoint.Summary)
}

func TestDeleteSteeredMessageInvalidatesItsEarlierAssistantCheckpoint(t *testing.T) {
	for _, rewind := range []bool{false, true} {
		t.Run(map[bool]string{false: "single", true: "rewind"}[rewind], func(t *testing.T) {
			repo, db := newMessageRepositoryForForkTest(t)
			ctx := t.Context()
			at := time.Now().Add(-time.Hour)
			seedMessage(t, db, "user", "s", "user", at)
			seedMessage(t, db, "assistant", "s", "assistant", at.Add(time.Second))
			seedMessage(t, db, "steer", "s", "user", at.Add(time.Minute))
			require.NoError(t, db.Model(&types.Message{}).Where("session_id = ?", "s").
				Update("request_id", "turn").Error)
			require.NoError(t, repo.UpdateMessageContextCheckpoint(ctx, "s", "assistant",
				&types.ContextCheckpoint{Summary: "includes steer"}))
			if rewind {
				_, err := repo.DeleteMessagesFrom(ctx, "s", at.Add(time.Minute), "steer", true)
				require.NoError(t, err)
			} else {
				require.NoError(t, repo.DeleteMessage(ctx, "s", "steer"))
			}
			checkpoint, err := repo.GetLatestContextCheckpoint(ctx, "s")
			require.NoError(t, err)
			require.Nil(t, checkpoint)
		})
	}
}

func TestCheckpointDeletionUsesCompositeBoundaryAndSessionScope(t *testing.T) {
	repo, db := newMessageRepositoryForForkTest(t)
	ctx := t.Context()
	at := time.Now().Add(-time.Hour)
	seedMessage(t, db, "a", "s", "assistant", at)
	seedMessage(t, db, "b", "s", "user", at)
	seedMessage(t, db, "c", "s", "assistant", at)
	for _, id := range []string{"a", "c"} {
		require.NoError(t, repo.UpdateMessageContextCheckpoint(ctx, "s", id, &types.ContextCheckpoint{Summary: id}))
	}
	require.NoError(t, repo.DeleteMessage(ctx, "wrong-session", "b"))
	require.NoError(t, repo.DeleteMessage(ctx, "s", "missing"))
	checkpoint, err := repo.GetLatestContextCheckpoint(ctx, "s")
	require.NoError(t, err)
	require.Equal(t, "c", checkpoint.ID)
	require.NoError(t, repo.DeleteMessage(ctx, "s", "b"))
	checkpoint, err = repo.GetLatestContextCheckpoint(ctx, "s")
	require.NoError(t, err)
	require.Equal(t, "a", checkpoint.ID)
}

func TestDeleteMessageRollsBackWhenCheckpointInvalidationFails(t *testing.T) {
	repo, db := newMessageRepositoryForForkTest(t)
	ctx := t.Context()
	at := time.Now().Add(-time.Hour)
	seedMessage(t, db, "deleted", "s", "user", at)
	seedMessage(t, db, "target", "s", "assistant", at.Add(time.Minute))
	require.NoError(t, db.Exec(`CREATE TRIGGER fail_checkpoint BEFORE UPDATE OF context_checkpoint ON messages
 BEGIN SELECT RAISE(ABORT, 'injected checkpoint failure'); END`).Error)
	require.Error(t, repo.DeleteMessage(ctx, "s", "deleted"))
	message, err := repo.GetMessage(ctx, "s", "deleted")
	require.NoError(t, err)
	require.Equal(t, "deleted", message.ID)
}

func TestMessageUpdateCannotRestoreInvalidatedCheckpoint(t *testing.T) {
	repo, db := newMessageRepositoryForForkTest(t)
	ctx := t.Context()
	at := time.Now().Add(-time.Hour)
	seedMessage(t, db, "deleted", "s", "user", at)
	seedMessage(t, db, "target", "s", "assistant", at.Add(time.Minute))
	require.NoError(t, repo.UpdateMessageContextCheckpoint(ctx, "s", "target",
		&types.ContextCheckpoint{Summary: "stale"}))
	stale, err := repo.GetMessage(ctx, "s", "target")
	require.NoError(t, err)
	require.NoError(t, repo.DeleteMessage(ctx, "s", "deleted"))
	stale.Content = "updated answer"
	require.NoError(t, repo.UpdateMessage(ctx, stale))
	checkpoint, err := repo.GetLatestContextCheckpoint(ctx, "s")
	require.NoError(t, err)
	require.Nil(t, checkpoint)
}

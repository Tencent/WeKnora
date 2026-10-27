package repository

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func seedSteeredCheckpoint(t *testing.T, repo *messageRepository, db *gorm.DB) time.Time {
	t.Helper()
	at := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	seedMessage(t, db, "user", "s", "user", at)
	seedMessage(t, db, "assistant", "s", "assistant", at.Add(time.Second))
	seedMessage(t, db, "steer", "s", "user", at.Add(time.Minute))
	require.NoError(t, db.Model(&types.Message{}).Where("session_id = ?", "s").
		Update("request_id", "turn").Error)
	require.NoError(t, repo.UpdateMessageContextCheckpoint(t.Context(), "s", "assistant",
		&types.ContextCheckpoint{Summary: "includes steer"}))
	return at.Add(time.Minute)
}

func TestRewindSteeredMessageInvalidatesEarlierAssistantCheckpoint(t *testing.T) {
	repo, db := newMessageRepositoryForForkTest(t)
	ctx := t.Context()
	cut := seedSteeredCheckpoint(t, repo, db)
	seedMessage(t, db, "before", "s", "assistant", cut.Add(-2*time.Minute))
	seedMessage(t, db, "other", "other", "assistant", cut)
	for _, pair := range [][2]string{{"s", "before"}, {"other", "other"}} {
		require.NoError(t, repo.UpdateMessageContextCheckpoint(ctx, pair[0], pair[1],
			&types.ContextCheckpoint{Summary: pair[1]}))
	}
	// A reused request ID in another session must not move this boundary.
	require.NoError(t, db.Model(&types.Message{}).Where("id = ?", "other").
		Updates(map[string]any{"request_id": "turn", "created_at": cut.Add(-3 * time.Minute)}).Error)
	deleted, err := repo.DeleteMessagesFrom(ctx, "s", cut, "steer", true)
	require.NoError(t, err)
	require.Len(t, deleted, 1)
	require.Equal(t, "steer", deleted[0].ID)
	assistant, err := repo.GetMessage(ctx, "s", "assistant")
	require.NoError(t, err)
	require.Nil(t, assistant.ContextCheckpoint)
	checkpoint, err := repo.GetLatestContextCheckpoint(ctx, "s")
	require.NoError(t, err)
	require.NotNil(t, checkpoint)
	require.Equal(t, "before", checkpoint.ID)
	other, err := repo.GetLatestContextCheckpoint(ctx, "other")
	require.NoError(t, err)
	require.NotNil(t, other)
	require.Equal(t, "other", other.ID)
}

func TestRewindCheckpointUsesCompositeBoundary(t *testing.T) {
	for _, inclusive := range []bool{false, true} {
		name := map[bool]string{false: "assistant_exclusive", true: "user_inclusive"}[inclusive]
		t.Run(name, func(t *testing.T) { checkRewindCompositeBoundary(t, inclusive) })
	}
}

func checkRewindCompositeBoundary(t *testing.T, inclusive bool) {
	t.Helper()
	repo, db := newMessageRepositoryForForkTest(t)
	at := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	for _, id := range []string{"a", "b", "c", "d"} {
		role := "assistant"
		if id == "d" {
			role = "user"
		}
		seedMessage(t, db, id, "s", role, at)
	}
	// c survives a cut at d, but its checkpoint covers that steered turn.
	require.NoError(t, db.Model(&types.Message{}).Where("id IN ?", []string{"c", "d"}).
		Update("request_id", "steered-turn").Error)
	for _, id := range []string{"a", "b", "c"} {
		require.NoError(t, repo.UpdateMessageContextCheckpoint(t.Context(), "s", id,
			&types.ContextCheckpoint{Summary: id}))
	}
	boundary, count := "b", 2
	if inclusive {
		boundary, count = "d", 1
	}
	deleted, err := repo.DeleteMessagesFrom(t.Context(), "s", at, boundary, inclusive)
	require.NoError(t, err)
	require.Len(t, deleted, count)
	checkpoint, err := repo.GetLatestContextCheckpoint(t.Context(), "s")
	require.NoError(t, err)
	require.NotNil(t, checkpoint)
	require.Equal(t, "b", checkpoint.ID)
}

func TestRewindSkipsAssistantWithoutCheckpoint(t *testing.T) {
	repo, db := newMessageRepositoryForForkTest(t)
	cut := seedSteeredCheckpoint(t, repo, db)
	seedMessage(t, db, "no-checkpoint", "s", "assistant", cut.Add(-time.Second))
	require.NoError(t, db.Exec(`CREATE TRIGGER reject_empty_checkpoint_update
 BEFORE UPDATE OF context_checkpoint ON messages WHEN OLD.context_checkpoint IS NULL
 BEGIN SELECT RAISE(ABORT, 'unnecessary checkpoint update'); END`).Error)
	_, err := repo.DeleteMessagesFrom(t.Context(), "s", cut, "steer", true)
	require.NoError(t, err)
	assistant, err := repo.GetMessage(t.Context(), "s", "assistant")
	require.NoError(t, err)
	require.Nil(t, assistant.ContextCheckpoint)
}

func TestRewindRollsBackWhenCheckpointInvalidationFails(t *testing.T) {
	repo, db := newMessageRepositoryForForkTest(t)
	cut := seedSteeredCheckpoint(t, repo, db)
	require.NoError(t, db.Exec(`CREATE TRIGGER fail_checkpoint BEFORE UPDATE OF context_checkpoint ON messages
 BEGIN SELECT RAISE(ABORT, 'injected checkpoint failure'); END`).Error)
	_, err := repo.DeleteMessagesFrom(t.Context(), "s", cut, "steer", true)
	require.Error(t, err)
	steer, err := repo.GetMessage(t.Context(), "s", "steer")
	require.NoError(t, err)
	require.Equal(t, "steer", steer.ID)
	assistant, err := repo.GetMessage(t.Context(), "s", "assistant")
	require.NoError(t, err)
	require.NotNil(t, assistant.ContextCheckpoint)
}

func TestMessageUpdateCannotRestoreRewoundCheckpoint(t *testing.T) {
	repo, db := newMessageRepositoryForForkTest(t)
	ctx := t.Context()
	cut := seedSteeredCheckpoint(t, repo, db)
	stale, err := repo.GetMessage(ctx, "s", "assistant")
	require.NoError(t, err)
	require.NotNil(t, stale.ContextCheckpoint)
	_, err = repo.DeleteMessagesFrom(ctx, "s", cut, "steer", true)
	require.NoError(t, err)
	stale.Content = "updated answer"
	require.NoError(t, repo.UpdateMessage(ctx, stale))
	updated, err := repo.GetMessage(ctx, "s", "assistant")
	require.NoError(t, err)
	require.Equal(t, "updated answer", updated.Content)
	require.Nil(t, updated.ContextCheckpoint)
}

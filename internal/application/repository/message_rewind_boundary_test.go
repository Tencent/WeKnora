package repository

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Rewinding at a user message drops that question too, so the boundary itself
// is deleted and the caller can prefill it back into the composer.
func TestDeleteMessagesFromInclusiveDropsBoundaryAndLater(t *testing.T) {
	repo, db := newMessageRepositoryForForkTest(t)
	ctx := context.Background()
	base := time.Date(2026, 9, 17, 9, 0, 0, 0, time.UTC)

	seedMessage(t, db, "m1", "s1", "user", base)
	seedMessage(t, db, "m2", "s1", "assistant", base.Add(time.Second))
	seedMessage(t, db, "m3", "s1", "user", base.Add(2*time.Second))
	seedMessage(t, db, "m4", "s1", "assistant", base.Add(3*time.Second))

	deleted, err := repo.DeleteMessagesFrom(ctx, "s1", base.Add(2*time.Second), "m3", true)
	require.NoError(t, err)
	require.Equal(t, []string{"m3", "m4"}, messageIDs(deleted))

	require.Equal(t, []string{"m1", "m2"}, remainingMessageIDs(t, db, "s1"))
}

// Rewinding at an assistant answer keeps that answer: the conversation resumes
// right after it.
func TestDeleteMessagesFromExclusiveKeepsBoundary(t *testing.T) {
	repo, db := newMessageRepositoryForForkTest(t)
	ctx := context.Background()
	base := time.Date(2026, 9, 17, 9, 0, 0, 0, time.UTC)

	seedMessage(t, db, "m1", "s1", "user", base)
	seedMessage(t, db, "m2", "s1", "assistant", base.Add(time.Second))
	seedMessage(t, db, "m3", "s1", "user", base.Add(2*time.Second))

	deleted, err := repo.DeleteMessagesFrom(ctx, "s1", base.Add(time.Second), "m2", false)
	require.NoError(t, err)
	require.Equal(t, []string{"m3"}, messageIDs(deleted))

	require.Equal(t, []string{"m1", "m2"}, remainingMessageIDs(t, db, "s1"))
}

// Same-millisecond peers are ordered by ID, so the cut is reproducible.
func TestDeleteMessagesFromUsesCompositeCursor(t *testing.T) {
	repo, db := newMessageRepositoryForForkTest(t)
	ctx := context.Background()
	at := time.Date(2026, 9, 17, 9, 0, 0, 0, time.UTC)

	seedMessage(t, db, "m-a", "s1", "user", at)
	seedMessage(t, db, "m-b", "s1", "assistant", at)
	seedMessage(t, db, "m-c", "s1", "user", at)

	deleted, err := repo.DeleteMessagesFrom(ctx, "s1", at, "m-b", true)
	require.NoError(t, err)
	require.Equal(t, []string{"m-b", "m-c"}, messageIDs(deleted))
	require.Equal(t, []string{"m-a"}, remainingMessageIDs(t, db, "s1"))
}

func TestDeleteMessagesFromLeavesOtherSessionsAlone(t *testing.T) {
	repo, db := newMessageRepositoryForForkTest(t)
	ctx := context.Background()
	base := time.Date(2026, 9, 17, 9, 0, 0, 0, time.UTC)

	seedMessage(t, db, "mine", "s1", "user", base)
	seedMessage(t, db, "theirs", "s2", "user", base.Add(time.Second))

	deleted, err := repo.DeleteMessagesFrom(ctx, "s1", base, "mine", true)
	require.NoError(t, err)
	require.Equal(t, []string{"mine"}, messageIDs(deleted))
	require.Equal(t, []string{"theirs"}, remainingMessageIDs(t, db, "s2"))
}

func TestDeleteMessagesFromReportsNothingWhenAlreadyAtTheEnd(t *testing.T) {
	repo, db := newMessageRepositoryForForkTest(t)
	ctx := context.Background()
	base := time.Date(2026, 9, 17, 9, 0, 0, 0, time.UTC)

	seedMessage(t, db, "m1", "s1", "user", base)
	seedMessage(t, db, "m2", "s1", "assistant", base.Add(time.Second))

	deleted, err := repo.DeleteMessagesFrom(ctx, "s1", base.Add(time.Second), "m2", false)
	require.NoError(t, err)
	require.Empty(t, deleted)
	require.Equal(t, []string{"m1", "m2"}, remainingMessageIDs(t, db, "s1"))
}

func remainingMessageIDs(t *testing.T, db *gorm.DB, sessionID string) []string {
	t.Helper()
	var rows []*types.Message
	require.NoError(t, db.Where("session_id = ?", sessionID).
		Order("created_at ASC, id ASC").Find(&rows).Error)
	return messageIDs(rows)
}

// The cheap checkpoint lookup must agree with the full-history read rewind
// used to do, or the two paths would disagree about which SHA survives a cut.
func TestListAssistantCheckpointsUpToMatchesFullHistoryPrefix(t *testing.T) {
	repo, db := newMessageRepositoryForForkTest(t)
	ctx := context.Background()
	base := time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)

	seedMessage(t, db, "m1", "s1", "user", base)
	seedCheckpointMessage(t, db, "m2", "s1", base.Add(time.Second), "sb1", strings.Repeat("a", 40))
	seedMessage(t, db, "m3", "s1", "user", base.Add(2*time.Second))
	seedCheckpointMessage(t, db, "m4", "s1", base.Add(3*time.Second), "sb1", strings.Repeat("b", 40))
	seedMessage(t, db, "boundary", "s1", "user", base.Add(4*time.Second))
	seedCheckpointMessage(t, db, "later", "s1", base.Add(5*time.Second), "sb1", strings.Repeat("c", 40))
	seedCheckpointMessage(t, db, "other", "s2", base, "sb2", strings.Repeat("d", 40))

	got, err := repo.ListAssistantCheckpointsUpTo(ctx, "s1", base.Add(4*time.Second), "boundary")
	require.NoError(t, err)
	require.Equal(t, []string{"m2", "m4"}, messageIDs(got))
	require.NotNil(t, got[1].SandboxCheckpoint)
	require.Equal(t, strings.Repeat("b", 40), got[1].SandboxCheckpoint.CommitSHA)
	require.Equal(t, "sb1", got[1].SandboxCheckpoint.SandboxID)

	full, err := repo.ListMessagesBySessionUpTo(ctx, "s1", base.Add(4*time.Second), "boundary")
	require.NoError(t, err)
	require.Equal(t, []string{"m1", "m2", "m3", "m4"}, messageIDs(full))
}

func TestListAssistantCheckpointsUpToSkipsSoftDeleted(t *testing.T) {
	repo, db := newMessageRepositoryForForkTest(t)
	ctx := context.Background()
	base := time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)

	seedCheckpointMessage(t, db, "m1", "s1", base, "sb1", strings.Repeat("a", 40))
	seedCheckpointMessage(t, db, "m2", "s1", base.Add(time.Second), "sb1", strings.Repeat("b", 40))
	seedMessage(t, db, "boundary", "s1", "user", base.Add(2*time.Second))
	require.NoError(t, db.Delete(&types.Message{}, "id = ?", "m2").Error)

	got, err := repo.ListAssistantCheckpointsUpTo(ctx, "s1", base.Add(2*time.Second), "boundary")
	require.NoError(t, err)
	require.Equal(t, []string{"m1"}, messageIDs(got))
}

func seedCheckpointMessage(
	t *testing.T, db *gorm.DB, id, sessionID string, at time.Time, sandboxID, sha string,
) {
	t.Helper()
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&types.Message{
		ID:        id,
		SessionID: sessionID,
		Role:      "assistant",
		Content:   "answer body that the checkpoint query must not need to read",
		CreatedAt: at,
		SandboxCheckpoint: &types.SandboxCheckpoint{
			SandboxID: sandboxID,
			CommitSHA: sha,
		},
	}).Error)
}

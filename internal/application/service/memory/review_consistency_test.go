package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func TestMemoryConsistencyPoisonConversationHasBoundedRetries(t *testing.T) {
	s, tr, messages, models, queue := newExtractionHarness(t)
	ctx := enabledCtx(t, tr, 1, "alice")
	at := time.Now().Add(-24 * time.Hour)
	// Separate sessions also prove a poison input cannot pin the other queue entries.
	badConversation := []*types.Message{
		userMessage("bad", "poison-marker", at),
		userMessage("bad", "poison-marker-后一句", at.Add(time.Minute)),
	}
	messages.set("bad", badConversation)
	messages.set("good", []*types.Message{
		userMessage("good", "valid-marker", at.Add(time.Hour)),
		userMessage("good", "valid-marker-后一句", at.Add(time.Hour+time.Minute)),
	})
	models.response = accountResponse("一个会话", "用户在这个会话里说了两句话。")
	models.responseFor = map[string]string{"poison-marker": "not json"}
	s.ScheduleExtraction(ctx, "bad", "m", "model")
	s.ScheduleExtraction(ctx, "good", "m", "model")
	drainExtractions(t, s, queue)
	poisonCalls := 0
	for _, prompt := range models.promptsSeen() {
		if containsTranscript(prompt, "poison-marker") {
			poisonCalls++
		}
	}
	require.Equal(t, 3, poisonCalls)
	require.Contains(t, models.seenTranscripts(), "valid-marker")
	pending, err := s.repo.HasPendingExtraction(ctx, scopeFor(t, ctx))
	require.NoError(t, err)
	require.False(t, pending)

	// Failure metadata is checked through a repository claim after a fresh turn.
	_, _, err = s.repo.EnqueuePendingSession(ctx, scopeFor(t, ctx), "bad", time.Minute)
	require.NoError(t, err)
	batch, err := s.repo.ClaimPendingSessions(ctx, scopeFor(t, ctx), "", "inspect", time.Minute)
	require.NoError(t, err)
	require.Len(t, batch.Sessions, 1)
	progress := batch.Sessions[0]
	require.Equal(t, 3, progress.FailureCount)
	require.NotNil(t, progress.FailedAt)
	require.Equal(t, "invalid_model_output", progress.FailureCode)
	require.True(t, progress.Cursor.At.Equal(progress.FailedTo.At))
	require.Equal(t, progress.Cursor.ID, progress.FailedTo.ID)
	require.NoError(t, s.repo.FinishExtraction(ctx, scopeFor(t, ctx), "inspect"))
	// A conversation given up on is not abandoned: later turns in it still run.
	messages.set("bad", append(badConversation,
		userMessage("bad", "later-valid-marker", at.Add(2*time.Hour))))
	models.responseFor = nil
	s.ScheduleExtraction(ctx, "bad", "later", "model")
	before := len(models.promptsSeen())
	drainExtractions(t, s, queue)
	require.Greater(t, len(models.promptsSeen()), before)
	require.Contains(t, transcriptBlock(models.promptsSeen()[before]), "later-valid-marker")

	stored, err := s.repo.EpisodeBySession(ctx, scopeFor(t, ctx), "bad")
	require.NoError(t, err)
	require.NotNil(t, stored, "the conversation has to become writable again once the model behaves")
}

func containsTranscript(prompt, marker string) bool {
	// Inspect only extractable input, excluding context and prompt examples.
	return strings.Contains(transcriptBlock(prompt), marker)
}

func TestMemoryConsistencyProgressDoesNotGrowSubjectJSON(t *testing.T) {
	s, db, tr := newMemoryHarness(t)
	ctx := enabledCtx(t, tr, 1, "alice")
	scope := scopeFor(t, ctx)
	subject, err := s.repo.EnsureSubject(ctx, scope)
	require.NoError(t, err)
	legacyBoundary := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	require.NoError(t, db.Model(subject).Updates(map[string]interface{}{
		"extract_cursor":   legacyBoundary,
		"pending_sessions": types.MemoryPendingSessions{"legacy"},
	}).Error)
	for i := 0; i < 100; i++ {
		_, _, err := s.repo.EnqueuePendingSession(ctx, scope, fmt.Sprintf("session-%03d", i), time.Minute)
		require.NoError(t, err)
	}
	for i := 0; ; i++ {
		lease := fmt.Sprintf("lease-%d", i)
		batch, err := s.repo.ClaimPendingSessions(ctx, scope, "", lease, time.Minute)
		require.NoError(t, err)
		if batch == nil {
			break
		}
		require.LessOrEqual(t, len(batch.Sessions), types.MaxMemoryPendingSessions)
		for _, session := range batch.Sessions {
			require.True(t, session.Cursor.At.Equal(legacyBoundary), "upgrade must not replay pre-cursor history")
			require.NoError(t, s.repo.CheckpointExtraction(ctx, scope, lease, session,
				types.MemoryMessageCursor{At: legacyBoundary.Add(time.Minute), ID: "done"}, true))
		}
		require.NoError(t, s.repo.FinishExtraction(ctx, scope, lease))
	}
	subject, err = s.repo.GetSubject(ctx, scope)
	require.NoError(t, err)
	raw, err := json.Marshal(subject.ExtractionState)
	require.NoError(t, err)
	require.Less(t, len(raw), 100)
	require.Empty(t, subject.PendingSessions)
	require.True(t, subject.ExtractCursor.Equal(legacyBoundary), "legacy baseline must never advance")
	var count int64
	require.NoError(t, db.Model(&types.MemoryExtractionSession{}).Count(&count).Error)
	require.EqualValues(t, 101, count)

	// A completed row remembers its cursor; a genuinely new row uses the frozen baseline.
	_, _, err = s.repo.EnqueuePendingSession(ctx, scope, "legacy", time.Minute)
	require.NoError(t, err)
	batch, err := s.repo.ClaimPendingSessions(ctx, scope, "", "again", time.Minute)
	require.NoError(t, err)
	require.Equal(t, "done", batch.Sessions[0].Cursor.ID)
}

type brokenEpisodeStore struct{ interfaces.MemoryRepository }

func (brokenEpisodeStore) SaveEpisode(
	context.Context, interfaces.MemoryScope, *types.MemoryEpisode,
) error {
	return fmt.Errorf("database unavailable")
}

// A database that is briefly unavailable must surface as a failed task, which
// asynq retries. Swallowing it would consume the conversation: the run would
// report success and no account of it would ever be written.
func TestMemoryConsistencyStorageDatabaseErrorsRemainRetryable(t *testing.T) {
	s, tr, messages, models, queue := newExtractionHarness(t)
	ctx := enabledCtx(t, tr, 1, "alice")
	s.repo = brokenEpisodeStore{s.repo}
	messages.set("s", settledConversation("s", "我是工程师", "在做后端"))
	models.response = accountResponse("职业", "用户是做后端的工程师。")

	s.ScheduleExtraction(ctx, "s", "m", "model")
	task := queue.pop()
	require.NotNil(t, task)
	require.ErrorContains(t, s.Handle(context.Background(), task), "database unavailable")
}

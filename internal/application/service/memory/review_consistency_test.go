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

func TestMemoryConsistencyInvalidDecisionsDoNotBlockValidOnes(t *testing.T) {
	s, _, tr := newMemoryHarness(t)
	ctx := enabledCtx(t, tr, 1, "alice")
	scope := scopeFor(t, ctx)
	old, err := s.Remember(ctx, types.MemoryItem{Kind: types.MemoryKindFact, Topic: "职业", Content: "我是工程师"})
	require.NoError(t, err)
	require.NoError(t, s.repo.DeleteItem(ctx, scope, old.ID)) // Snapshot became stale during the model call.
	zero, invalid := 0, 99
	require.NoError(t, s.applyDecisions(ctx, scope, s.workspaceConfig(ctx, 1),
		transcriptSegment{}, []*types.MemoryItem{old}, []extractionDecision{
			{Action: "update", Target: &zero, Content: "我是经理"},
			{Action: "add", Content: "  "},
			{Action: "update", Target: &invalid, Content: "错误索引"},
			{Action: "update", Topic: "不存在", Content: "不应该变成新增"},
			{Action: "add", Kind: types.MemoryKindFact, Topic: "编辑器", Content: "使用 Neovim"},
		}))
	items, total, err := s.ListItems(ctx, types.MemoryStatusActive, 10, 0)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Equal(t, "使用 Neovim", items[0].Content)
}

func TestMemoryConsistencyDeleteInvalidatesPendingTarget(t *testing.T) {
	s, _, tr := newMemoryHarness(t)
	ctx := enabledCtx(t, tr, 1, "alice")
	old, err := s.Remember(ctx, types.MemoryItem{Topic: "职业", Content: "我是工程师"})
	require.NoError(t, err)
	proposal, err := s.Remember(ctx, types.MemoryItem{Topic: "职业", Content: "可能是经理", Inferred: true})
	require.NoError(t, err)
	require.NoError(t, s.DeleteItem(ctx, old.ID))
	_, total, err := s.ListItems(ctx, types.MemoryStatusPending, 10, 0)
	require.NoError(t, err)
	require.Zero(t, total)
	_, err = s.ConfirmItem(ctx, proposal.ID)
	require.ErrorIs(t, err, types.ErrMemoryConflict)
}

func TestMemoryConsistencyPoisonSegmentHasBoundedRetries(t *testing.T) {
	s, tr, messages, models, queue := newExtractionHarness(t)
	ctx := enabledCtx(t, tr, 1, "alice")
	at := time.Now().Add(-24 * time.Hour)
	// Separate sessions also prove a poison input cannot pin the other queue entries.
	badMessage := userMessage("bad", "poison-marker", at)
	messages.set("bad", []*types.Message{badMessage})
	messages.set("good", []*types.Message{userMessage("good", "valid-marker", at.Add(time.Hour))})
	models.response = `{"memories":[]}`
	models.responseFor = map[string]string{"poison-marker": "not json"}
	s.ScheduleExtraction(ctx, "bad", "m", "model")
	s.ScheduleExtraction(ctx, "good", "m", "model")
	drainExtractions(t, s, queue)
	poisonCalls := 0
	for _, prompt := range models.prompts {
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
	// Later messages in the same conversation still run, without re-reading the poison input.
	messages.set("bad", []*types.Message{
		badMessage,
		userMessage("bad", "later-valid-marker", at.Add(2*time.Hour)),
	})
	models.responseFor = nil
	s.ScheduleExtraction(ctx, "bad", "later", "model")
	before := len(models.prompts)
	drainExtractions(t, s, queue)
	require.Greater(t, len(models.prompts), before)
	require.Contains(t, transcriptBlock(models.prompts[before]), "later-valid-marker")
	require.NotContains(t, transcriptBlock(models.prompts[before]), "poison-marker")
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

type brokenDecisionLookup struct{ interfaces.MemoryRepository }

func (brokenDecisionLookup) FindActiveByKey(
	context.Context, interfaces.MemoryScope, string,
) (*types.MemoryItem, error) {
	return nil, fmt.Errorf("database unavailable")
}

func TestMemoryConsistencyDecisionDatabaseErrorsRemainRetryable(t *testing.T) {
	s, _, tr := newMemoryHarness(t)
	ctx := enabledCtx(t, tr, 1, "alice")
	s.repo = brokenDecisionLookup{s.repo}
	err := s.applyDecisions(ctx, scopeFor(t, ctx), s.workspaceConfig(ctx, 1),
		transcriptSegment{}, nil, []extractionDecision{{Action: "update", Topic: "职业", Content: "经理"}})
	require.ErrorContains(t, err, "database unavailable")
}

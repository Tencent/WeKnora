package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type partialRebuildRepoStub struct {
	interfaces.KnowledgeRepository
	knowledgeID string
	updates     map[string]interface{}
	knowledge   *types.Knowledge
}

func (r *partialRebuildRepoStub) UpdateKnowledge(_ context.Context, knowledge *types.Knowledge) error {
	copied := *knowledge
	r.knowledge = &copied
	return nil
}

func (r *partialRebuildRepoStub) UpdateKnowledgeColumns(
	_ context.Context,
	knowledgeID string,
	values map[string]interface{},
) error {
	r.knowledgeID = knowledgeID
	r.updates = values
	return nil
}

type partialRebuildTrackerStub struct {
	noopSpanTracker
	knowledgeID string
	attempt     int
	status      string
	errorCode   string
}

func (t *partialRebuildTrackerStub) FinalizeAttempt(
	_ context.Context,
	knowledgeID string,
	attempt int,
	status string,
	_ types.JSONMap,
	errorCode string,
	_ string,
) {
	t.knowledgeID = knowledgeID
	t.attempt = attempt
	t.status = status
	t.errorCode = errorCode
}

func TestRollbackPartialRebuildStateRestoresDocumentAndFailsAttempt(t *testing.T) {
	repo := &partialRebuildRepoStub{}
	tracker := &partialRebuildTrackerStub{}
	svc := &knowledgeService{repo: repo, spanTracker: tracker}
	updatedAt := time.Date(2026, 7, 20, 10, 30, 0, 0, time.UTC)

	svc.rollbackPartialRebuildState(
		context.Background(), "knowledge-1", 4,
		types.ParseStatusCompleted, "enabled", 2, "previous error", updatedAt,
	)

	require.Equal(t, "knowledge-1", repo.knowledgeID)
	require.Equal(t, map[string]interface{}{
		"parse_status":           types.ParseStatusCompleted,
		"enable_status":          "enabled",
		"pending_subtasks_count": 2,
		"error_message":          "previous error",
		"updated_at":             updatedAt,
	}, repo.updates)
	require.Equal(t, "knowledge-1", tracker.knowledgeID)
	require.Equal(t, 4, tracker.attempt)
	require.Equal(t, types.SpanStatusFailed, tracker.status)
	require.Equal(t, "REBUILD_ENQUEUE_FAILED", tracker.errorCode)
}

func TestFailPartialRebuildKeepsExistingDocumentEnabled(t *testing.T) {
	repo := &partialRebuildRepoStub{}
	tracker := &partialRebuildTrackerStub{}
	svc := &knowledgeService{repo: repo, spanTracker: tracker}
	knowledge := &types.Knowledge{
		ID:           "knowledge-2",
		ParseStatus:  types.ParseStatusProcessing,
		EnableStatus: "disabled",
	}

	svc.failPartialRebuild(context.Background(), knowledge, 5, errors.New("embedding unavailable"))

	require.NotNil(t, repo.knowledge)
	require.Equal(t, types.ParseStatusFailed, repo.knowledge.ParseStatus)
	require.Equal(t, "enabled", repo.knowledge.EnableStatus)
	require.Equal(t, "embedding unavailable", repo.knowledge.ErrorMessage)
	require.Equal(t, "knowledge-2", tracker.knowledgeID)
	require.Equal(t, 5, tracker.attempt)
	require.Equal(t, types.SpanStatusFailed, tracker.status)
	require.Equal(t, "PARTIAL_REBUILD_FAILED", tracker.errorCode)
}

func TestPostProcessUpdatesSummaryStatusOnlyWhenSelected(t *testing.T) {
	require.True(t, postProcessUpdatesSummaryStatus(nil))
	require.True(t, postProcessUpdatesSummaryStatus([]string{types.RebuildStageSummary}))
	require.True(t, postProcessUpdatesSummaryStatus([]string{
		types.RebuildStageJournalRank,
		types.RebuildStageSummary,
	}))
	require.False(t, postProcessUpdatesSummaryStatus([]string{types.RebuildStageJournalRank}))
}

func TestRuleBasedContentType(t *testing.T) {
	tests := []struct {
		knowledge *types.Knowledge
		want      types.KnowledgeContentType
		matched   bool
	}{
		{knowledge: &types.Knowledge{Type: "url"}, want: types.KnowledgeContentTypeWebpage, matched: true},
		{knowledge: &types.Knowledge{Type: "manual"}, want: types.KnowledgeContentTypeManual, matched: true},
		{knowledge: &types.Knowledge{Type: "file", FileType: "epub"}, want: types.KnowledgeContentTypeBook, matched: true},
		{knowledge: &types.Knowledge{Type: "file", FileType: "pptx"}, want: types.KnowledgeContentTypePresentation, matched: true},
		{knowledge: &types.Knowledge{Type: "file", FileType: "xlsx"}, want: types.KnowledgeContentTypeSpreadsheet, matched: true},
		{knowledge: &types.Knowledge{Type: "file", FileType: "pdf"}, matched: false},
	}
	for _, tt := range tests {
		got, matched := ruleBasedContentType(tt.knowledge)
		require.Equal(t, tt.want, got)
		require.Equal(t, tt.matched, matched)
	}
}

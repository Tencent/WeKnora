package handler

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestKnowledgeProgressWeights(t *testing.T) {
	require.Equal(t, 8, progressDocumentTotal(false))
	require.Equal(t, 16, progressDocumentTotal(true))

	rows := []types.KnowledgeProcessingSpan{
		{Name: types.StageDocReader, Status: types.SpanStatusDone},
		{Name: types.StageChunking, Status: types.SpanStatusDone},
		{Name: types.StageEmbedding, Status: types.SpanStatusDone},
		{Name: types.StageMultimodal, Status: types.SpanStatusSkipped},
		{Name: "postprocess.question", Status: types.SpanStatusDone},
	}
	require.Equal(t, 6, progressWeightFromSpans(rows, false))

	wikiRows := append(rows,
		types.KnowledgeProcessingSpan{Name: "postprocess.summary", Status: types.SpanStatusDone},
		types.KnowledgeProcessingSpan{Name: "postprocess.wiki.extract", Status: types.SpanStatusDone},
		types.KnowledgeProcessingSpan{Name: "postprocess.wiki.summary", Status: types.SpanStatusDone},
		types.KnowledgeProcessingSpan{Name: "postprocess.wiki.classify", Status: types.SpanStatusDone},
		types.KnowledgeProcessingSpan{Name: "postprocess.wiki.page[home]", Status: types.SpanStatusDone},
	)
	require.Equal(t, 16, progressWeightFromSpans(wikiRows, true))
}

func TestKnowledgeProgressCounts(t *testing.T) {
	counts := progressCountsFromKnowledges([]*types.Knowledge{
		{ParseStatus: types.ParseStatusCompleted},
		{ParseStatus: types.ParseStatusDeleting},
		{ParseStatus: types.ParseStatusCancelled},
		{ParseStatus: types.ParseStatusProcessing},
		{ParseStatus: types.ParseStatusFailed},
	})
	require.Equal(t, knowledgeProgressCounts{
		Completed: 1, Deleted: 1, Stopped: 1, Incomplete: 2,
	}, counts)
}

func TestProgressKnowledgeTerminalTreatsSoftDeletedDocumentAsTerminal(t *testing.T) {
	deleted := &types.Knowledge{
		ParseStatus: types.ParseStatusProcessing,
		DeletedAt:   gorm.DeletedAt{Valid: true},
	}
	require.True(t, progressKnowledgeTerminal(deleted))
}

func TestBatchParseCancellable(t *testing.T) {
	for _, test := range []struct {
		name      string
		knowledge *types.Knowledge
		want      bool
	}{
		{"pending", &types.Knowledge{ParseStatus: types.ParseStatusPending}, true},
		{"processing", &types.Knowledge{ParseStatus: types.ParseStatusProcessing}, true},
		{"finalizing", &types.Knowledge{ParseStatus: types.ParseStatusFinalizing}, true},
		{"completed", &types.Knowledge{ParseStatus: types.ParseStatusCompleted}, false},
		{"cancelled", &types.Knowledge{ParseStatus: types.ParseStatusCancelled}, false},
		{"soft deleted", &types.Knowledge{ParseStatus: types.ParseStatusProcessing, DeletedAt: gorm.DeletedAt{Valid: true}}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, batchParseCancellable(test.knowledge))
		})
	}
}

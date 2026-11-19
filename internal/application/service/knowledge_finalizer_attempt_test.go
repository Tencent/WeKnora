package service

import (
	"context"
	"encoding/json"
	"testing"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

type supersedingGraphChat struct {
	graphReplyChat
	duringCall func()
}

func (c supersedingGraphChat) Chat(
	ctx context.Context, messages []chat.Message, opts *chat.ChatOptions,
) (*types.ChatResponse, error) {
	c.duringCall()
	return c.graphReplyChat.Chat(ctx, messages, opts)
}

func TestChunkExtractCompletionDoesNotDrainNewParseAttempt(t *testing.T) {
	ctx := context.Background()
	svc, db := newKnowledgeSharedAccessService(t, nil)
	require.NoError(t, db.Exec(spanTrackerTestDDL).Error)
	seedKnowledge(t, db, &types.Knowledge{
		ID: "doc", TenantID: 1, KnowledgeBaseID: "kb", Type: "file",
		ParseStatus: types.ParseStatusFinalizing, PendingSubtasksCount: 1,
	})
	tracker := NewSpanTracker(apprepo.NewKnowledgeSpanRepository(db), db)
	_, first, err := tracker.OpenAttempt(ctx, "doc", "")
	require.NoError(t, err)
	require.Equal(t, 1, first)
	model := supersedingGraphChat{
		graphReplyChat: graphReplyChat{resp: &types.ChatResponse{
			Content: "No entities to extract.", FinishReason: "stop",
		}},
		duringCall: func() {
			_, next, err := tracker.OpenAttempt(ctx, "doc", "")
			require.NoError(t, err)
			require.Equal(t, 2, next)
			require.NoError(t, db.Model(&types.Knowledge{}).Where("id = ?", "doc").Updates(map[string]interface{}{
				"parse_status": types.ParseStatusProcessing, "pending_subtasks_count": 0,
			}).Error)
			seeded, err := svc.repo.SetFinalizing(ctx, "doc", 2)
			require.NoError(t, err)
			require.True(t, seeded)
		},
	}
	extractor := &ChunkExtractService{
		template:      &types.PromptTemplateStructured{Description: "extract"},
		modelService:  &stubModelService{chatModel: model},
		knowledgeRepo: svc.repo, spanTracker: tracker,
		knowledgeBaseRepo: graphKBRepo{kb: &types.KnowledgeBase{
			ID: "kb", ExtractConfig: &types.ExtractConfig{Enabled: true},
		}},
		chunkRepo: graphChunkRepo{chunk: &types.Chunk{
			ID: "c1", KnowledgeID: "doc", KnowledgeBaseID: "kb", Content: "text",
		}},
	}
	payload, err := json.Marshal(types.ExtractChunkPayload{
		TenantID: 1, ChunkID: "c1", KnowledgeID: "doc", ModelID: "m1", Attempt: first,
	})
	require.NoError(t, err)
	require.NoError(t, extractor.Handle(ctx, asynq.NewTask(types.TypeChunkExtract, payload)))
	current, err := svc.repo.GetKnowledgeByID(ctx, 1, "doc")
	require.NoError(t, err)
	require.Equal(t, 2, current.PendingSubtasksCount,
		"the old handler completed after the new attempt seeded its slots")
	require.Equal(t, types.ParseStatusFinalizing, current.ParseStatus)
}

type supersedingSummaryQueue struct{ duringCall func() }

func (q supersedingSummaryQueue) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	if task.Type() == types.TypeSummaryGeneration {
		q.duringCall()
	}
	return &asynq.TaskInfo{ID: "queued", Type: task.Type()}, nil
}

func TestPostProcessShortfallUsesResolvedParseAttempt(t *testing.T) {
	t.Setenv("NEO4J_ENABLE", "false")
	ctx := context.Background()
	service, db := newKnowledgeSharedAccessService(t, nil)
	require.NoError(t, db.Exec(spanTrackerTestDDL).Error)
	seedKnowledge(t, db, &types.Knowledge{
		ID: "doc", TenantID: 7, KnowledgeBaseID: "kb", Type: "file", ParseStatus: types.ParseStatusProcessing,
	})
	tracker := NewSpanTracker(apprepo.NewKnowledgeSpanRepository(db), db)
	_, _, err := tracker.OpenAttempt(ctx, "doc", "")
	require.NoError(t, err)
	queued := false
	queue := supersedingSummaryQueue{duringCall: func() {
		queued = true
		_, attempt, err := tracker.OpenAttempt(ctx, "doc", "")
		require.NoError(t, err)
		require.Equal(t, 2, attempt)
		require.NoError(t, db.Model(&types.Knowledge{}).Where("id = ?", "doc").Updates(map[string]interface{}{
			"parse_status": types.ParseStatusProcessing, "pending_subtasks_count": 0,
		}).Error)
		seeded, err := service.repo.SetFinalizing(ctx, "doc", 2)
		require.NoError(t, err)
		require.True(t, seeded)
	}}
	post := &KnowledgePostProcessService{
		knowledgeRepo: service.repo, spanTracker: tracker, taskEnqueuer: queue,
		kbService: &wikiEnqueueFailureKBService{kb: &types.KnowledgeBase{
			ID: "kb", TenantID: 7, IndexingStrategy: types.IndexingStrategy{GraphEnabled: true},
			ExtractConfig: &types.ExtractConfig{Enabled: true},
		}},
		chunkRepo: &wikiEnqueueFailureChunkRepo{chunks: []*types.Chunk{
			{
				ID: "c1", TenantID: 7, KnowledgeID: "doc", KnowledgeBaseID: "kb",
				ChunkType: types.ChunkTypeText, Content: "Alice pays Bob.",
			},
			{
				ID: "c2", TenantID: 7, KnowledgeID: "doc", KnowledgeBaseID: "kb",
				ChunkType: types.ChunkTypeText, Content: "Payment is due.",
			},
		}},
	}
	// Legacy payload: Handle resolves its owner to attempt one before fan-out.
	payload, err := json.Marshal(types.KnowledgePostProcessPayload{
		TenantID: 7, KnowledgeID: "doc", KnowledgeBaseID: "kb",
	})
	require.NoError(t, err)
	require.NoError(t, post.Handle(ctx, asynq.NewTask(types.TypeKnowledgePostProcess, payload)))
	require.True(t, queued)
	current, err := service.repo.GetKnowledgeByID(ctx, 7, "doc")
	require.NoError(t, err)
	require.Equal(t, 2, current.PendingSubtasksCount, "un-enqueued slots still belong to the resolved old attempt")
	require.Equal(t, types.ParseStatusFinalizing, current.ParseStatus)
}

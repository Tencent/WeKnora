package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type postProcessKnowledgeRepo struct {
	interfaces.KnowledgeRepository
	knowledge    *types.Knowledge
	failedReason string
}

func (r *postProcessKnowledgeRepo) GetKnowledgeByIDOnly(_ context.Context, id string) (*types.Knowledge, error) {
	if r.knowledge == nil || r.knowledge.ID != id {
		return nil, nil
	}
	return r.knowledge, nil
}

func (r *postProcessKnowledgeRepo) SetFinalizingIfAttempt(_ context.Context, tenantID uint64, id string, attempt int64, _ int) (bool, error) {
	return r.knowledge != nil &&
		r.knowledge.TenantID == tenantID &&
		r.knowledge.ID == id &&
		r.knowledge.CurrentProcessAttempt == attempt, nil
}

func (r *postProcessKnowledgeRepo) UpdateKnowledgeColumnsIfAttempt(
	_ context.Context,
	tenantID uint64,
	id string,
	attempt int64,
	_ []string,
	_ map[string]interface{},
) (bool, error) {
	return r.knowledge != nil &&
		r.knowledge.TenantID == tenantID &&
		r.knowledge.ID == id &&
		r.knowledge.CurrentProcessAttempt == attempt, nil
}

func (r *postProcessKnowledgeRepo) MarkKnowledgeFailedIfAttempt(
	_ context.Context,
	tenantID uint64,
	id string,
	attempt int64,
	reason string,
) (bool, error) {
	if r.knowledge != nil &&
		r.knowledge.TenantID == tenantID &&
		r.knowledge.ID == id &&
		r.knowledge.CurrentProcessAttempt == attempt {
		r.failedReason = reason
		return true, nil
	}
	return false, nil
}

type postProcessKBService struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func (s postProcessKBService) GetKnowledgeBaseByIDOnly(_ context.Context, id string) (*types.KnowledgeBase, error) {
	if s.kb == nil || s.kb.ID != id {
		return nil, nil
	}
	return s.kb, nil
}

type postProcessChunkService struct {
	interfaces.ChunkService
	chunks []*types.Chunk
}

func (s postProcessChunkService) ListChunksByKnowledgeID(_ context.Context, _ string) ([]*types.Chunk, error) {
	return s.chunks, nil
}

func TestKnowledgePostProcessPassesAttemptToWikiIngest(t *testing.T) {
	payload := types.KnowledgePostProcessPayload{
		TenantID:        1,
		KnowledgeID:     "kid-1",
		KnowledgeBaseID: "kb-1",
		Attempt:         5,
	}
	payloadBytes, err := json.Marshal(payload)
	require.NoError(t, err)

	knowledgeRepo := &postProcessKnowledgeRepo{
		knowledge: &types.Knowledge{
			ID:                    "kid-1",
			TenantID:              1,
			KnowledgeBaseID:       "kb-1",
			ParseStatus:           types.ParseStatusProcessing,
			CurrentProcessAttempt: 5,
		},
	}
	pendingRepo := &recordingWikiPendingRepo{}
	enqueuer := &fakeTaskEnqueuer{}
	handler := NewKnowledgePostProcessService(
		knowledgeRepo,
		postProcessKBService{kb: &types.KnowledgeBase{
			ID:       "kb-1",
			TenantID: 1,
			IndexingStrategy: types.IndexingStrategy{
				WikiEnabled: true,
			},
		}},
		postProcessChunkService{chunks: []*types.Chunk{{
			ID:              "chunk-1",
			TenantID:        1,
			KnowledgeID:     "kid-1",
			KnowledgeBaseID: "kb-1",
			ChunkType:       types.ChunkTypeText,
			Content:         "text content",
		}}},
		enqueuer,
		pendingRepo,
		nil,
		nil,
		nil,
	)

	err = handler.Handle(context.Background(), asynq.NewTask(types.TypeKnowledgePostProcess, payloadBytes))
	require.NoError(t, err)
	assert.Empty(t, knowledgeRepo.failedReason)
	require.Len(t, pendingRepo.enqueued, 1)

	var pending WikiPendingOp
	require.NoError(t, json.Unmarshal(pendingRepo.enqueued[0].Payload, &pending))
	assert.Equal(t, WikiOpIngest, pending.Op)
	assert.Equal(t, "kid-1", pending.KnowledgeID)
	assert.Equal(t, 5, pending.Attempt)
}

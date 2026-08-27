package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

type embeddingFailureKnowledgeRepo struct {
	interfaces.KnowledgeRepository
	knowledge *types.Knowledge
	updated   types.Knowledge
}

func (r *embeddingFailureKnowledgeRepo) GetKnowledgeByID(
	context.Context, uint64, string,
) (*types.Knowledge, error) {
	return r.knowledge, nil
}

func (r *embeddingFailureKnowledgeRepo) UpdateKnowledge(
	_ context.Context, knowledge *types.Knowledge,
) error {
	r.updated = *knowledge
	return nil
}

type embeddingFailureTenantRepo struct {
	interfaces.TenantRepository
}

func (embeddingFailureTenantRepo) GetTenantByID(
	_ context.Context, tenantID uint64,
) (*types.Tenant, error) {
	return &types.Tenant{ID: tenantID}, nil
}

type embeddingFailureKBService struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func (s embeddingFailureKBService) GetKnowledgeBaseByID(
	context.Context, string,
) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

type embeddingFailureModelService struct {
	interfaces.ModelService
	err error
}

func (s embeddingFailureModelService) GetEmbeddingModel(
	context.Context, string,
) (embedding.Embedder, error) {
	return nil, s.err
}

type recordingFailureSpanTracker struct {
	noopSpanTracker
	status       string
	errorCode    string
	errorMessage string
}

func (t *recordingFailureSpanTracker) OpenAttempt(
	_ context.Context, knowledgeID, _ string,
) (*Span, int, error) {
	return &Span{KnowledgeID: knowledgeID, Attempt: 1}, 1, nil
}

func (t *recordingFailureSpanTracker) FinalizeAttempt(
	_ context.Context,
	_ string,
	_ int,
	status string,
	_ types.JSONMap,
	errorCode string,
	errorMessage string,
) {
	t.status = status
	t.errorCode = errorCode
	t.errorMessage = errorMessage
}

func TestProcessManualUpdateEmbeddingModelFailureIsTerminalAndRetryable(t *testing.T) {
	modelErr := errors.New("embedding credentials unavailable")
	knowledge := &types.Knowledge{
		ID:              "knowledge-1",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		ParseStatus:     types.ParseStatusPending,
	}
	kb := &types.KnowledgeBase{
		ID:               "kb-1",
		TenantID:         1,
		EmbeddingModelID: "embedding-1",
		IndexingStrategy: types.IndexingStrategy{VectorEnabled: true},
	}
	repo := &embeddingFailureKnowledgeRepo{knowledge: knowledge}
	tracker := &recordingFailureSpanTracker{}
	svc := &knowledgeService{
		repo:         repo,
		tenantRepo:   embeddingFailureTenantRepo{},
		kbService:    embeddingFailureKBService{kb: kb},
		modelService: embeddingFailureModelService{err: modelErr},
		spanTracker:  tracker,
	}
	payload, err := json.Marshal(types.ManualProcessPayload{
		TenantID:        1,
		KnowledgeID:     knowledge.ID,
		KnowledgeBaseID: kb.ID,
		Content:         "video transcript chunk",
	})
	require.NoError(t, err)

	err = svc.ProcessManualUpdate(context.Background(), asynq.NewTask(types.TypeManualProcess, payload))

	require.ErrorIs(t, err, modelErr)
	require.Equal(t, types.ParseStatusFailed, repo.updated.ParseStatus)
	require.Contains(t, repo.updated.ErrorMessage, "embedding credentials unavailable")
	require.Equal(t, types.SpanStatusFailed, tracker.status)
	require.Equal(t, werrors.ErrCodeEmbeddingProviderFail, tracker.errorCode)
	require.Contains(t, tracker.errorMessage, "embedding credentials unavailable")
}

package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

// embeddingModelFailureRepo is a KnowledgeRepository fake that keeps the
// persisted row in sync so pipeline re-reads (isKnowledgeAborted) observe
// the same status a real repository would.
type embeddingModelFailureRepo struct {
	interfaces.KnowledgeRepository
	knowledge   *types.Knowledge
	updateCalls int
}

func (r *embeddingModelFailureRepo) GetKnowledgeByID(
	context.Context, uint64, string,
) (*types.Knowledge, error) {
	return r.knowledge, nil
}

func (r *embeddingModelFailureRepo) UpdateKnowledge(_ context.Context, k *types.Knowledge) error {
	r.updateCalls++
	r.knowledge = k
	return nil
}

func (r *embeddingModelFailureRepo) UpdateKnowledgeColumn(
	context.Context, string, string, interface{},
) error {
	return nil
}

// embeddingModelFailureModelService makes GetEmbeddingModel fail the way a
// missing or misconfigured embedding provider does.
type embeddingModelFailureModelService struct {
	interfaces.ModelService
}

func (embeddingModelFailureModelService) GetEmbeddingModel(
	context.Context, string,
) (embedding.Embedder, error) {
	return nil, errors.New("embedding model not found")
}

type embeddingModelFailureKBService struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func (s embeddingModelFailureKBService) GetKnowledgeBaseByID(
	context.Context, string,
) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

type embeddingModelFailureTenantRepo struct {
	interfaces.TenantRepository
	tenant *types.Tenant
}

func (r embeddingModelFailureTenantRepo) GetTenantByID(
	context.Context, uint64,
) (*types.Tenant, error) {
	return r.tenant, nil
}

func newEmbeddingModelFailureService() (
	*knowledgeService, *embeddingModelFailureRepo, *types.KnowledgeBase, *types.Knowledge,
) {
	knowledge := &types.Knowledge{
		ID:              "knowledge-1",
		TenantID:        7,
		KnowledgeBaseID: "kb-1",
		Type:            "passage",
		ParseStatus:     types.ParseStatusPending,
		EnableStatus:    "disabled",
	}
	repo := &embeddingModelFailureRepo{knowledge: knowledge}
	kb := &types.KnowledgeBase{
		ID:               "kb-1",
		TenantID:         7,
		EmbeddingModelID: "embed-1",
		IndexingStrategy: types.DefaultIndexingStrategy(),
	}
	svc := &knowledgeService{
		repo:         repo,
		kbService:    embeddingModelFailureKBService{kb: kb},
		tenantRepo:   embeddingModelFailureTenantRepo{tenant: &types.Tenant{ID: 7}},
		modelService: embeddingModelFailureModelService{},
	}
	return svc, repo, kb, knowledge
}

func embeddingModelFailureTask(t *testing.T) *asynq.Task {
	t.Helper()
	payloadBytes, err := json.Marshal(types.DocumentProcessPayload{
		TenantID:        7,
		KnowledgeID:     "knowledge-1",
		KnowledgeBaseID: "kb-1",
		Passages:        []string{"hello world"},
	})
	require.NoError(t, err)
	return asynq.NewTask(types.TypeDocumentProcess, payloadBytes)
}

// TestProcessDocumentEmbeddingModelFailureReturnsErrorForRetry pins the
// contract that a failed GetEmbeddingModel bubbles an error out of
// ProcessDocument so asynq retries. Before the fix ProcessDocument
// returned nil (task "succeeded"), leaving the knowledge row stuck in
// "processing" forever with no error message and no retry.
func TestProcessDocumentEmbeddingModelFailureReturnsErrorForRetry(t *testing.T) {
	svc, repo, _, _ := newEmbeddingModelFailureService()

	err := svc.ProcessDocument(context.Background(), embeddingModelFailureTask(t))

	require.Error(t, err)
	require.ErrorContains(t, err, "resolve embedding model failed")
	require.Equal(t, types.ParseStatusProcessing, repo.knowledge.ParseStatus,
		"non-final attempt must stay processing so asynq can retry")
}

// TestProcessDocumentEmbeddingModelFailureMarksFailedOnLastRetry pins the
// terminal behavior: once the retry budget is exhausted the row must be
// marked failed with a visible error message instead of staying stuck in
// "processing".
func TestProcessDocumentEmbeddingModelFailureMarksFailedOnLastRetry(t *testing.T) {
	svc, repo, _, _ := newEmbeddingModelFailureService()
	ctx := types.WithTaskRetryMetadata(context.Background(), 3, 3)

	err := svc.ProcessDocument(ctx, embeddingModelFailureTask(t))

	require.Error(t, err)
	require.Equal(t, types.ParseStatusFailed, repo.knowledge.ParseStatus)
	require.Contains(t, repo.knowledge.ErrorMessage, "resolve embedding model failed")
	require.GreaterOrEqual(t, repo.updateCalls, 2, "processing and failed states must both be persisted")
}

// TestMarkKnowledgeFailedSkipsWhenAborted pins the guard on
// markKnowledgeFailed: a failure landing after the user cancelled the row
// must not overwrite the newer "cancelled" status.
func TestMarkKnowledgeFailedSkipsWhenAborted(t *testing.T) {
	svc, repo, _, _ := newEmbeddingModelFailureService()
	repo.knowledge.ParseStatus = types.ParseStatusCancelled

	svc.markKnowledgeFailed(context.Background(), repo.knowledge, "boom")

	require.Equal(t, types.ParseStatusCancelled, repo.knowledge.ParseStatus)
	require.Empty(t, repo.knowledge.ErrorMessage)
	require.Zero(t, repo.updateCalls)
}

// TestProcessDocumentFromPassageEmbeddingModelFailureMarksFailed pins the
// sync (request-path) contract: there is no asynq retry loop, so the row
// must be marked failed immediately instead of staying stuck in
// "processing".
func TestProcessDocumentFromPassageEmbeddingModelFailureMarksFailed(t *testing.T) {
	svc, repo, kb, knowledge := newEmbeddingModelFailureService()

	svc.processDocumentFromPassage(context.Background(), kb, knowledge, []string{"hello world"})

	require.Equal(t, types.ParseStatusFailed, repo.knowledge.ParseStatus)
	require.Contains(t, repo.knowledge.ErrorMessage, "resolve embedding model failed")
}

// TestTriggerManualProcessingEmbeddingModelFailureMarksFailed pins the async
// goroutine path (used by moveKnowledgeReparse): there is no error channel,
// so the goroutine must mark the row failed itself.
func TestTriggerManualProcessingEmbeddingModelFailureMarksFailed(t *testing.T) {
	svc, repo, kb, knowledge := newEmbeddingModelFailureService()

	err := svc.triggerManualProcessing(context.Background(), kb, knowledge, "# content", false)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return repo.knowledge.ParseStatus == types.ParseStatusFailed
	}, 2*time.Second, 5*time.Millisecond)
	require.Contains(t, repo.knowledge.ErrorMessage, "resolve embedding model failed")
}

func manualUpdateFailureTask(t *testing.T) *asynq.Task {
	t.Helper()
	payloadBytes, err := json.Marshal(types.ManualProcessPayload{
		TenantID:        7,
		KnowledgeID:     "knowledge-1",
		KnowledgeBaseID: "kb-1",
		Content:         "# content",
	})
	require.NoError(t, err)
	return asynq.NewTask(types.TypeManualProcess, payloadBytes)
}

// TestProcessManualUpdateEmbeddingModelFailureReturnsErrorForRetry pins the
// manual-reprocess contract: the error is returned so asynq retries, and the
// non-final attempt leaves the row in "processing".
func TestProcessManualUpdateEmbeddingModelFailureReturnsErrorForRetry(t *testing.T) {
	svc, repo, _, _ := newEmbeddingModelFailureService()

	err := svc.ProcessManualUpdate(context.Background(), manualUpdateFailureTask(t))

	require.Error(t, err)
	require.ErrorContains(t, err, "resolve embedding model failed")
	require.Equal(t, types.ParseStatusProcessing, repo.knowledge.ParseStatus,
		"non-final attempt must stay processing so asynq can retry")
}

// TestProcessManualUpdateEmbeddingModelFailureMarksFailedOnLastRetry pins the
// terminal behavior for manual reprocessing.
func TestProcessManualUpdateEmbeddingModelFailureMarksFailedOnLastRetry(t *testing.T) {
	svc, repo, _, _ := newEmbeddingModelFailureService()
	ctx := types.WithTaskRetryMetadata(context.Background(), 3, 3)

	err := svc.ProcessManualUpdate(ctx, manualUpdateFailureTask(t))

	require.Error(t, err)
	require.Equal(t, types.ParseStatusFailed, repo.knowledge.ParseStatus)
	require.Contains(t, repo.knowledge.ErrorMessage, "resolve embedding model failed")
}

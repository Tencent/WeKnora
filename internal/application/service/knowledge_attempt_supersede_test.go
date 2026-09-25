package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// attemptTracker is a SpanTracker whose LatestAttempt a test controls, so a
// stale task can be made to see a newer (reparse) attempt. latest[i] is
// returned for the i-th call and the last entry for every later one, which
// lets a test flip to "superseded" partway through a pipeline.
type attemptTracker struct {
	noopSpanTracker
	mu       sync.Mutex
	latest   []int
	calls    int
	opened   int
	nextOpen int
	events   *[]string
}

func newAttemptTracker(latest ...int) *attemptTracker {
	return &attemptTracker{latest: latest}
}

func (t *attemptTracker) LatestAttempt(context.Context, string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.latest) == 0 {
		return 0
	}
	i := t.calls
	t.calls++
	if i >= len(t.latest) {
		i = len(t.latest) - 1
	}
	return t.latest[i]
}

func (t *attemptTracker) OpenAttempt(context.Context, string, string) (*Span, int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.opened++
	if t.events != nil {
		*t.events = append(*t.events, "open-attempt")
	}
	t.nextOpen++
	return &Span{}, t.nextOpen, nil
}

func newDocumentProcessTask(t *testing.T, attempt int) *asynq.Task {
	t.Helper()
	payload, err := json.Marshal(types.DocumentProcessPayload{
		TenantID:        1,
		KnowledgeID:     "k-1",
		KnowledgeBaseID: "kb-1",
		FilePath:        "file.md",
		FileName:        "file.md",
		Attempt:         attempt,
	})
	require.NoError(t, err)
	return asynq.NewTask(types.TypeDocumentProcess, payload)
}

func processingKnowledge() *types.Knowledge {
	return &types.Knowledge{
		ID:              "k-1",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		ParseStatus:     types.ParseStatusProcessing,
		FilePath:        "file.md",
	}
}

// A ProcessDocument task queued before a reparse must not touch the row the
// reparse now owns: writing "processing" back and wiping the new chunks is
// what stranded rows mid-reparse.
func TestProcessDocumentSkipsSupersededAttemptWithoutWritingRow(t *testing.T) {
	repo := &embedFailureKnowledgeRepo{knowledge: processingKnowledge()}
	svc := &knowledgeService{
		repo:        repo,
		tenantRepo:  &summaryRefreshTenantRepo{tenant: &types.Tenant{ID: 1}},
		spanTracker: newAttemptTracker(3),
	}

	err := svc.ProcessDocument(context.Background(), newDocumentProcessTask(t, 2))

	require.NoError(t, err)
	assert.Empty(t, repo.updates, "a superseded task must not write the row")
}

// Acking a task whose tenant or row could not be read left the row pending
// with no task behind it; those failures must be retried.
func TestProcessDocumentRetriesUnreadableTenantOrKnowledge(t *testing.T) {
	readErr := errors.New("connection reset")
	tests := []struct {
		name    string
		tenant  *summaryRefreshTenantRepo
		repo    *abortCheckRepo
		wantErr error
	}{
		{
			name:    "tenant lookup fails",
			tenant:  &summaryRefreshTenantRepo{err: readErr},
			repo:    &abortCheckRepo{knowledge: processingKnowledge()},
			wantErr: readErr,
		},
		{
			name:    "knowledge read fails",
			tenant:  &summaryRefreshTenantRepo{tenant: &types.Tenant{ID: 1}},
			repo:    &abortCheckRepo{err: readErr},
			wantErr: readErr,
		},
		{
			name:   "knowledge is gone",
			tenant: &summaryRefreshTenantRepo{tenant: &types.Tenant{ID: 1}},
			repo:   &abortCheckRepo{err: apprepo.ErrKnowledgeNotFound},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			svc := &knowledgeService{repo: test.repo, tenantRepo: test.tenant}

			err := svc.ProcessDocument(context.Background(), newDocumentProcessTask(t, 1))

			if test.wantErr == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, test.wantErr)
		})
	}
}

func TestProcessManualUpdateRetriesUnreadableTenantOrKnowledge(t *testing.T) {
	readErr := errors.New("connection reset")
	payload, err := json.Marshal(types.ManualProcessPayload{
		TenantID: 1, KnowledgeID: "k-1", KnowledgeBaseID: "kb-1", Content: "body",
	})
	require.NoError(t, err)
	task := asynq.NewTask(types.TypeManualProcess, payload)
	tests := []struct {
		name    string
		tenant  *summaryRefreshTenantRepo
		repo    *abortCheckRepo
		wantErr error
	}{
		{
			name:    "tenant lookup fails",
			tenant:  &summaryRefreshTenantRepo{err: readErr},
			repo:    &abortCheckRepo{knowledge: processingKnowledge()},
			wantErr: readErr,
		},
		{
			name:    "knowledge read fails",
			tenant:  &summaryRefreshTenantRepo{tenant: &types.Tenant{ID: 1}},
			repo:    &abortCheckRepo{err: readErr},
			wantErr: readErr,
		},
		{
			name:   "knowledge is gone",
			tenant: &summaryRefreshTenantRepo{tenant: &types.Tenant{ID: 1}},
			repo:   &abortCheckRepo{err: apprepo.ErrKnowledgeNotFound},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			svc := &knowledgeService{repo: test.repo, tenantRepo: test.tenant}

			err := svc.ProcessManualUpdate(context.Background(), task)

			if test.wantErr == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, test.wantErr)
		})
	}
}

// processChunks under an attempt a reparse has superseded must leave the new
// attempt's chunks alone. modelService is nil on purpose: going past the
// guard would panic resolving the embedder.
func TestProcessChunksSkipsSupersededAttemptBeforeTouchingChunks(t *testing.T) {
	knowledge := processingKnowledge()
	repo := &embedFailureKnowledgeRepo{knowledge: knowledge}
	chunks := &deleteCountingChunkRepo{}
	svc := &knowledgeService{repo: repo, chunkRepo: chunks, spanTracker: newAttemptTracker(2)}
	kb := &types.KnowledgeBase{
		ID: "kb-1", TenantID: 1, EmbeddingModelID: "embedding-1",
		IndexingStrategy: types.IndexingStrategy{VectorEnabled: true},
	}

	err := svc.processChunks(withAttempt(context.Background(), 1), kb, knowledge,
		[]types.ParsedChunk{{Content: "body"}})

	require.NoError(t, err)
	assert.Zero(t, chunks.deletes)
	assert.Empty(t, repo.updates)
}

type chunkWriteCountingRepo struct {
	parentChildChunkService
	deletes int
}

func (r *chunkWriteCountingRepo) DeleteChunksByKnowledgeID(context.Context, uint64, string) error {
	r.deletes++
	return nil
}

func newIndexingHarness(
	knowledge *types.Knowledge, task interfaces.TaskEnqueuer, tracker SpanTracker,
) (*knowledgeService, *embedFailureKnowledgeRepo, *types.KnowledgeBase, context.Context) {
	repo := &embedFailureKnowledgeRepo{knowledge: knowledge}
	tenant := &types.Tenant{
		ID: 1,
		RetrieverEngines: types.RetrieverEngines{Engines: []types.RetrieverEngineParams{{
			RetrieverType:       types.VectorRetrieverType,
			RetrieverEngineType: types.PostgresRetrieverEngineType,
		}}},
	}
	svc := &knowledgeService{
		repo:           repo,
		chunkRepo:      &chunkWriteCountingRepo{},
		modelService:   parentChildModelService{embedder: parentChildEmbedder{}},
		retrieveEngine: parentChildRetrieveRegistry{engine: &parentChildRetrieveEngine{}},
		graphEngine:    parentChildGraphRepo{},
		tenantRepo:     parentChildTenantRepo{},
		task:           task,
		spanTracker:    tracker,
	}
	kb := &types.KnowledgeBase{
		ID: "kb-1", TenantID: 1, EmbeddingModelID: "embedding-1",
		IndexingStrategy: types.IndexingStrategy{VectorEnabled: true},
	}
	ctx := context.WithValue(context.Background(), types.TenantInfoContextKey, tenant)
	return svc, repo, kb, ctx
}

// A reparse that lands while this run is embedding must not see its row put
// back to this run's final status, nor get a post-process for the old run.
func TestProcessChunksSkipsCompletionWhenSupersededWhileIndexing(t *testing.T) {
	enqueuer := &slotReleaseEnqueuer{}
	// Current at the entry guard, superseded by the completion guard.
	svc, repo, kb, ctx := newIndexingHarness(processingKnowledge(), enqueuer, newAttemptTracker(1, 2))

	err := svc.processChunks(withAttempt(ctx, 1), kb, repo.knowledge,
		[]types.ParsedChunk{{Content: "body", Seq: 0, Start: 0, End: 4, ParentIndex: -1}})

	require.NoError(t, err)
	assert.Equal(t, 1, svc.chunkRepo.(*chunkWriteCountingRepo).deletes, "the run passed the entry guard")
	assert.Empty(t, repo.updates, "the superseded run must not save its final state")
	assert.Zero(t, enqueuer.postProcess)
}

type failingEnqueuer struct {
	err   error
	calls int
}

func (e *failingEnqueuer) Enqueue(*asynq.Task, ...asynq.Option) (*asynq.TaskInfo, error) {
	e.calls++
	return nil, e.err
}

// Once the chunks are indexed the post-process enqueue is the only thing
// that moves the row out of "processing", so its failure must fail the task
// (asynq retries the idempotent parse) instead of being logged and acked.
func TestProcessChunksReturnsErrorWhenPostProcessCannotBeEnqueued(t *testing.T) {
	queueErr := errors.New("redis unavailable")
	enqueuer := &failingEnqueuer{err: queueErr}
	svc, repo, kb, ctx := newIndexingHarness(processingKnowledge(), enqueuer, nil)

	err := svc.processChunks(ctx, kb, repo.knowledge,
		[]types.ParsedChunk{{Content: "body", Seq: 0, Start: 0, End: 4, ParentIndex: -1}})

	require.ErrorIs(t, err, queueErr)
	assert.Equal(t, postProcessEnqueueAttempts, enqueuer.calls, "the enqueue is retried in place first")
}

// Post-process of a run a reparse superseded must not enter finalizing: it
// would seed a counter for subtasks that all drop themselves as superseded.
func TestKnowledgePostProcessSkipsSupersededAttempt(t *testing.T) {
	const knowledgeID = "knowledge-superseded-post-process"
	pendingRepo := &wikiEnqueueFailurePendingRepo{}
	queue := &wikiEnqueueFailureTaskQueue{}
	svc, repo := newWikiEnqueueTestService(knowledgeID, pendingRepo, queue)
	svc.spanTracker = newAttemptTracker(2)
	payload, err := json.Marshal(types.KnowledgePostProcessPayload{
		TenantID: 7, KnowledgeID: knowledgeID, KnowledgeBaseID: "kb-wiki", Attempt: 1,
	})
	require.NoError(t, err)

	err = svc.Handle(context.Background(), asynq.NewTask(types.TypeKnowledgePostProcess, payload))

	require.NoError(t, err)
	assert.Equal(t, types.ParseStatusProcessing, repo.knowledge.ParseStatus)
	assert.Zero(t, repo.expectedSubtasks)
	assert.Zero(t, pendingRepo.seedCalls)
	assert.Empty(t, queue.taskTypes)
}

// The current attempt still runs post-process normally.
func TestKnowledgePostProcessRunsCurrentAttempt(t *testing.T) {
	const knowledgeID = "knowledge-current-post-process"
	pendingRepo := &wikiEnqueueFailurePendingRepo{}
	queue := &wikiEnqueueFailureTaskQueue{}
	svc, repo := newWikiEnqueueTestService(knowledgeID, pendingRepo, queue)
	svc.spanTracker = newAttemptTracker(1)
	payload, err := json.Marshal(types.KnowledgePostProcessPayload{
		TenantID: 7, KnowledgeID: knowledgeID, KnowledgeBaseID: "kb-wiki", Attempt: 1,
	})
	require.NoError(t, err)

	err = svc.Handle(context.Background(), asynq.NewTask(types.TypeKnowledgePostProcess, payload))

	require.NoError(t, err)
	assert.Equal(t, types.ParseStatusFinalizing, repo.knowledge.ParseStatus)
	require.NotNil(t, pendingRepo.seededOp)
	var op WikiPendingOp
	require.NoError(t, json.Unmarshal(pendingRepo.seededOp.Payload, &op))
	assert.Equal(t, 1, op.Attempt, "the wiki op records the attempt that owns its slot")
}

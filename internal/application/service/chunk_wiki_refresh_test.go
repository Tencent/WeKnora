package service

import (
	"context"
	"encoding/json"
	"testing"

	serviceretriever "github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"go.uber.org/dig"
)

type chunkWikiRefreshRepo struct {
	interfaces.ChunkRepository
	chunk    *types.Chunk
	revision *types.ChunkRevision
}

func (r *chunkWikiRefreshRepo) GetChunkByID(
	_ context.Context, _ uint64, _ string,
) (*types.Chunk, error) {
	copyOfChunk := *r.chunk
	return &copyOfChunk, nil
}

func (r *chunkWikiRefreshRepo) SaveChunkRevision(
	_ context.Context, chunk *types.Chunk, _ *types.ChunkRevision, _ int,
) error {
	copyOfChunk := *chunk
	r.chunk = &copyOfChunk
	return nil
}

func (r *chunkWikiRefreshRepo) UpdateChunk(_ context.Context, chunk *types.Chunk) error {
	copyOfChunk := *chunk
	r.chunk = &copyOfChunk
	return nil
}

func (r *chunkWikiRefreshRepo) ListChunkByParentID(
	_ context.Context, _ uint64, _ string,
) ([]*types.Chunk, error) {
	return nil, nil
}

func (r *chunkWikiRefreshRepo) GetChunkRevision(
	_ context.Context, _ uint64, _ string, _ int,
) (*types.ChunkRevision, error) {
	copyOfRevision := *r.revision
	return &copyOfRevision, nil
}

type chunkWikiRefreshKnowledgeRepo struct {
	interfaces.KnowledgeRepository
	knowledge *types.Knowledge
}

func (r *chunkWikiRefreshKnowledgeRepo) GetKnowledgeByID(
	_ context.Context, _ uint64, _ string,
) (*types.Knowledge, error) {
	copyOfKnowledge := *r.knowledge
	return &copyOfKnowledge, nil
}

type chunkWikiRefreshKBRepo struct {
	interfaces.KnowledgeBaseRepository
	kb *types.KnowledgeBase
}

func (r *chunkWikiRefreshKBRepo) GetKnowledgeBaseByID(
	_ context.Context, _ string,
) (*types.KnowledgeBase, error) {
	copyOfKB := *r.kb
	return &copyOfKB, nil
}

type chunkWikiRefreshPendingRepo struct {
	interfaces.TaskPendingOpsRepository
	ops []*types.TaskPendingOp
}

func (r *chunkWikiRefreshPendingRepo) Enqueue(_ context.Context, op *types.TaskPendingOp) error {
	copyOfOp := *op
	copyOfOp.Payload = append([]byte(nil), op.Payload...)
	r.ops = append(r.ops, &copyOfOp)
	return nil
}

type chunkWikiRefreshTaskQueue struct {
	tasks []*asynq.Task
}

func (q *chunkWikiRefreshTaskQueue) Enqueue(
	task *asynq.Task, _ ...asynq.Option,
) (*asynq.TaskInfo, error) {
	q.tasks = append(q.tasks, task)
	return nil, nil
}

type chunkWikiRefreshAttemptTracker struct {
	SpanTracker
	attempt int
}

func (t chunkWikiRefreshAttemptTracker) LatestAttempt(context.Context, string) int {
	return t.attempt
}

type chunkWikiRefreshModelService struct{ interfaces.ModelService }
type chunkWikiRefreshRetrieveRegistry struct {
	interfaces.RetrieveEngineRegistry
}

type chunkWikiRefreshOwnership struct{}

func (chunkWikiRefreshOwnership) StoreOwnedBy(context.Context, string, uint64) (bool, error) {
	return true, nil
}

type chunkWikiRefreshHarness struct {
	service *chunkService
	repo    *chunkWikiRefreshRepo
	pending *chunkWikiRefreshPendingRepo
	queue   *chunkWikiRefreshTaskQueue
	ctx     context.Context
}

func newChunkWikiRefreshHarness(wikiEnabled bool, attempt int) chunkWikiRefreshHarness {
	chunk := &types.Chunk{
		ID: "chunk-1", TenantID: 7, KnowledgeBaseID: "kb-1", KnowledgeID: "knowledge-1",
		ChunkType: types.ChunkTypeText, Content: "current content", SourceContent: "current content",
		ContentRevision: 2, IsEnabled: true, IndexStatus: "ready",
	}
	repo := &chunkWikiRefreshRepo{
		chunk: chunk,
		revision: &types.ChunkRevision{
			ChunkID: chunk.ID, Revision: 1, Content: "historical content", IsEnabled: true,
		},
	}
	pending := &chunkWikiRefreshPendingRepo{}
	queue := &chunkWikiRefreshTaskQueue{}
	kbRepo := &chunkWikiRefreshKBRepo{kb: &types.KnowledgeBase{
		ID: chunk.KnowledgeBaseID,
		IndexingStrategy: types.IndexingStrategy{
			WikiEnabled: wikiEnabled,
		},
	}}
	knowledgeRepo := &chunkWikiRefreshKnowledgeRepo{knowledge: &types.Knowledge{
		ID: chunk.KnowledgeID, TenantID: chunk.TenantID, KnowledgeBaseID: chunk.KnowledgeBaseID,
		SummaryStatus: types.SummaryStatusNone,
	}}
	return chunkWikiRefreshHarness{
		service: &chunkService{
			chunkRepository: repo,
			knowledgeRepo:   knowledgeRepo,
			kbRepository:    kbRepo,
			task:            queue,
			pendingRepo:     pending,
			spanTracker:     chunkWikiRefreshAttemptTracker{attempt: attempt},
		},
		repo:    repo,
		pending: pending,
		queue:   queue,
		ctx: context.WithValue(
			context.Background(), types.TenantIDContextKey, chunk.TenantID,
		),
	}
}

func assertChunkWikiRefreshQueued(t *testing.T, h chunkWikiRefreshHarness, wantAttempt int) {
	t.Helper()
	if len(h.pending.ops) != 1 {
		t.Fatalf("pending wiki ops = %d, want 1", len(h.pending.ops))
	}
	pending := h.pending.ops[0]
	if pending.TaskType != wikiTaskType || pending.Scope != wikiTaskScope ||
		pending.ScopeID != "kb-1" || pending.DedupKey != "knowledge-1" || pending.Op != WikiOpIngest {
		t.Fatalf("unexpected pending wiki op: %+v", pending)
	}
	var op WikiPendingOp
	if err := json.Unmarshal(pending.Payload, &op); err != nil {
		t.Fatalf("decode pending wiki op: %v", err)
	}
	if op.KnowledgeID != "knowledge-1" || op.Attempt != wantAttempt {
		t.Fatalf("pending wiki payload = %+v, want knowledge-1 attempt %d", op, wantAttempt)
	}
	if len(h.queue.tasks) != 1 || h.queue.tasks[0].Type() != types.TypeWikiIngest {
		t.Fatalf("wiki trigger tasks = %+v, want one %s task", h.queue.tasks, types.TypeWikiIngest)
	}
	var trigger WikiIngestPayload
	if err := json.Unmarshal(h.queue.tasks[0].Payload(), &trigger); err != nil {
		t.Fatalf("decode wiki trigger: %v", err)
	}
	if trigger.TenantID != 7 || trigger.KnowledgeBaseID != "kb-1" {
		t.Fatalf("unexpected wiki trigger: %+v", trigger)
	}
}

func TestUpdateDocumentChunkEnqueuesWikiRefreshWithCurrentAttempt(t *testing.T) {
	h := newChunkWikiRefreshHarness(true, 9)
	content := "manually edited content"
	if _, err := h.service.UpdateDocumentChunk(h.ctx, "chunk-1", &content, nil, nil); err != nil {
		t.Fatalf("UpdateDocumentChunk() error = %v", err)
	}
	assertChunkWikiRefreshQueued(t, h, 9)
}

func TestDisableDocumentChunkEnqueuesWikiRefresh(t *testing.T) {
	h := newChunkWikiRefreshHarness(true, 4)
	disabled := false
	if _, err := h.service.UpdateDocumentChunk(h.ctx, "chunk-1", nil, &disabled, nil); err != nil {
		t.Fatalf("UpdateDocumentChunk(disable) error = %v", err)
	}
	assertChunkWikiRefreshQueued(t, h, 4)
}

func TestRevertDocumentChunkEnqueuesWikiRefresh(t *testing.T) {
	h := newChunkWikiRefreshHarness(true, 6)
	if _, err := h.service.RevertDocumentChunk(h.ctx, "chunk-1", 1, nil); err != nil {
		t.Fatalf("RevertDocumentChunk() error = %v", err)
	}
	if h.repo.chunk.Content != "historical content" {
		t.Fatalf("reverted content = %q", h.repo.chunk.Content)
	}
	assertChunkWikiRefreshQueued(t, h, 6)
}

func TestChunkWikiRefreshSkipsNoOpAndDisabledKnowledgeBase(t *testing.T) {
	t.Run("no-op", func(t *testing.T) {
		h := newChunkWikiRefreshHarness(true, 3)
		content := h.repo.chunk.Content
		if _, err := h.service.UpdateDocumentChunk(h.ctx, "chunk-1", &content, nil, nil); err != nil {
			t.Fatalf("UpdateDocumentChunk(no-op) error = %v", err)
		}
		if len(h.pending.ops) != 0 || len(h.queue.tasks) != 0 {
			t.Fatalf("no-op unexpectedly queued wiki refresh")
		}
	})

	t.Run("wiki-disabled", func(t *testing.T) {
		h := newChunkWikiRefreshHarness(false, 3)
		content := "new content"
		if _, err := h.service.UpdateDocumentChunk(h.ctx, "chunk-1", &content, nil, nil); err != nil {
			t.Fatalf("UpdateDocumentChunk() error = %v", err)
		}
		if len(h.pending.ops) != 0 || len(h.queue.tasks) != 0 {
			t.Fatalf("wiki-disabled KB unexpectedly queued wiki refresh")
		}
	})
}

func TestNewChunkServiceDIResolvesWikiPendingRepository(t *testing.T) {
	container := dig.New()
	providers := []any{
		func() interfaces.ChunkRepository { return &chunkWikiRefreshRepo{} },
		func() interfaces.KnowledgeRepository { return &chunkWikiRefreshKnowledgeRepo{} },
		func() interfaces.KnowledgeBaseRepository { return &chunkWikiRefreshKBRepo{} },
		func() interfaces.ModelService { return chunkWikiRefreshModelService{} },
		func() interfaces.RetrieveEngineRegistry { return chunkWikiRefreshRetrieveRegistry{} },
		func() serviceretriever.TenantStoreOwnership { return chunkWikiRefreshOwnership{} },
		func() interfaces.TaskEnqueuer { return &chunkWikiRefreshTaskQueue{} },
		func() interfaces.TaskPendingOpsRepository { return &chunkWikiRefreshPendingRepo{} },
		func() SpanTracker { return chunkWikiRefreshAttemptTracker{} },
		NewChunkService,
	}
	for _, provider := range providers {
		if err := container.Provide(provider); err != nil {
			t.Fatalf("provide chunk service dependency: %v", err)
		}
	}
	if err := container.Invoke(func(service interfaces.ChunkService) {
		resolved, ok := service.(*chunkService)
		if !ok || resolved.pendingRepo == nil {
			t.Fatalf("resolved chunk service is missing wiki pending repository: %#v", service)
		}
	}); err != nil {
		t.Fatalf("resolve NewChunkService through dig: %v", err)
	}
}

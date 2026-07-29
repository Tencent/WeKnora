package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Moving a document out of a wiki-enabled KB must reconcile that KB's wiki
// state the same way a delete does. wiki_pages hold source_refs back to the
// knowledge and are what both the folder tree and the wiki graph are rendered
// from, so skipping the cleanup leaves the source KB showing pages for a
// document it no longer owns.

type moveWikiKnowledgeRepo struct {
	interfaces.KnowledgeRepository
	knowledge *types.Knowledge
}

func (r *moveWikiKnowledgeRepo) GetKnowledgeByID(
	_ context.Context, _ uint64, _ string,
) (*types.Knowledge, error) {
	clone := *r.knowledge
	return &clone, nil
}

func (r *moveWikiKnowledgeRepo) UpdateKnowledge(_ context.Context, k *types.Knowledge) error {
	clone := *k
	r.knowledge = &clone
	return nil
}

func (r *moveWikiKnowledgeRepo) DeleteKnowledgeTagRelations(_ context.Context, _ string) error {
	return nil
}

type moveWikiPageRepo struct {
	interfaces.WikiPageRepository
	listedKBs []string
}

func (r *moveWikiPageRepo) ListBySourceRef(
	_ context.Context, kbID, _ string,
) ([]*types.WikiPage, error) {
	r.listedKBs = append(r.listedKBs, kbID)
	return nil, nil
}

type moveWikiPendingRepo struct {
	interfaces.TaskPendingOpsRepository
	ops          []*types.TaskPendingOp
	failEnqueues map[string]int
}

func (r *moveWikiPendingRepo) Enqueue(_ context.Context, op *types.TaskPendingOp) error {
	key := op.ScopeID + "/" + op.Op
	if r.failEnqueues[key] > 0 {
		r.failEnqueues[key]--
		return errors.New("pending queue unavailable")
	}
	r.ops = append(r.ops, op)
	return nil
}

func (r *moveWikiPendingRepo) DeleteByDedupKey(_ context.Context, _, _, _, _, _ string) error {
	return nil
}

type moveWikiChunkRepo struct {
	interfaces.ChunkRepository
	movedToKB string
	moveErr   error
}

func (r *moveWikiChunkRepo) ListChunksByKnowledgeID(
	_ context.Context, _ uint64, _ string,
) ([]*types.Chunk, error) {
	return nil, nil
}

func (r *moveWikiChunkRepo) MoveChunksByKnowledgeID(
	_ context.Context, _ uint64, _ string, targetKBID string,
) error {
	if r.moveErr != nil {
		return r.moveErr
	}
	r.movedToKB = targetKBID
	return nil
}

type moveWikiKBService struct {
	interfaces.KnowledgeBaseService
	kbs map[string]*types.KnowledgeBase
}

func (s *moveWikiKBService) GetKnowledgeBaseByID(
	_ context.Context, id string,
) (*types.KnowledgeBase, error) {
	kb, ok := s.kbs[id]
	if !ok {
		return nil, errors.New("knowledge base not found")
	}
	return kb, nil
}

type moveWikiTenantRepo struct {
	interfaces.TenantRepository
}

func (r *moveWikiTenantRepo) GetTenantByID(_ context.Context, id uint64) (*types.Tenant, error) {
	return &types.Tenant{ID: id}, nil
}

func wikiEnabledKB(id string) *types.KnowledgeBase {
	return &types.KnowledgeBase{
		ID:               id,
		IndexingStrategy: types.IndexingStrategy{WikiEnabled: true},
	}
}

// opsFor returns the pending ops enqueued against a given KB scope.
func opsFor(ops []*types.TaskPendingOp, kbID string) []*types.TaskPendingOp {
	var out []*types.TaskPendingOp
	for _, op := range ops {
		if op.ScopeID == kbID {
			out = append(out, op)
		}
	}
	return out
}

func newMoveWikiService(t *testing.T) (
	*knowledgeService, *moveWikiPageRepo, *moveWikiPendingRepo, *moveWikiChunkRepo,
) {
	t.Helper()
	wikiRepo := &moveWikiPageRepo{}
	pendingRepo := &moveWikiPendingRepo{}
	chunkRepo := &moveWikiChunkRepo{}
	svc := &knowledgeService{
		repo: &moveWikiKnowledgeRepo{knowledge: &types.Knowledge{
			ID:              "kn-1",
			TenantID:        1,
			Title:           "Doc",
			KnowledgeBaseID: "kb-src",
			ParseStatus:     types.ParseStatusCompleted,
		}},
		wikiRepo:        wikiRepo,
		taskPendingRepo: pendingRepo,
		task:            &wikiGuardTaskQueue{},
		chunkRepo:       chunkRepo,
	}
	return svc, wikiRepo, pendingRepo, chunkRepo
}

func moveWikiCtx() context.Context {
	return context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
}

func TestMoveOneKnowledgeRejectsUnknownModeBeforeWikiCleanup(t *testing.T) {
	svc, wikiRepo, pendingRepo, _ := newMoveWikiService(t)

	err := svc.moveOneKnowledge(moveWikiCtx(), "kn-1",
		wikiEnabledKB("kb-src"), wikiEnabledKB("kb-dst"), "bogus")

	require.Error(t, err)
	assert.Empty(t, wikiRepo.listedKBs)
	assert.Empty(t, pendingRepo.ops)
	assert.Equal(t, types.ParseStatusCompleted,
		svc.repo.(*moveWikiKnowledgeRepo).knowledge.ParseStatus)
}

func TestMoveOneKnowledgeReuseVectorsIngestsIntoTargetKB(t *testing.T) {
	// reuse_vectors keeps the existing chunks and never re-enters the parse
	// pipeline, so nothing else would tell the target KB to build wiki pages.
	svc, wikiRepo, pendingRepo, chunkRepo := newMoveWikiService(t)

	err := svc.moveOneKnowledge(moveWikiCtx(), "kn-1",
		wikiEnabledKB("kb-src"), wikiEnabledKB("kb-dst"), "reuse_vectors")

	require.NoError(t, err)
	assert.Equal(t, "kb-dst", chunkRepo.movedToKB)

	dstOps := opsFor(pendingRepo.ops, "kb-dst")
	require.Len(t, dstOps, 1)
	assert.Equal(t, WikiOpIngest, dstOps[0].Op)
	assert.Equal(t, "kn-1", dstOps[0].DedupKey)
	assert.Equal(t, []string{"kb-src"}, wikiRepo.listedKBs)
	srcOps := opsFor(pendingRepo.ops, "kb-src")
	require.Len(t, srcOps, 1)
	assert.Equal(t, WikiOpRetract, srcOps[0].Op)
	require.Len(t, pendingRepo.ops, 2)
	assert.Equal(t, "kb-dst", pendingRepo.ops[0].ScopeID,
		"target rebuild must be durable before source cleanup starts")
	assert.Equal(t, "kb-src", pendingRepo.ops[1].ScopeID)
}

func TestMoveOneKnowledgeSkipsWikiWorkForNonWikiKBs(t *testing.T) {
	svc, wikiRepo, pendingRepo, _ := newMoveWikiService(t)

	err := svc.moveOneKnowledge(moveWikiCtx(), "kn-1",
		&types.KnowledgeBase{ID: "kb-src"}, &types.KnowledgeBase{ID: "kb-dst"}, "reuse_vectors")

	require.NoError(t, err)
	assert.Empty(t, wikiRepo.listedKBs)
	assert.Empty(t, pendingRepo.ops)
}

func TestMoveOneKnowledgeKeepsSourceWikiWhenMoveFails(t *testing.T) {
	svc, wikiRepo, pendingRepo, chunkRepo := newMoveWikiService(t)
	chunkRepo.moveErr = errors.New("move chunks failed")

	err := svc.moveOneKnowledge(moveWikiCtx(), "kn-1",
		wikiEnabledKB("kb-src"), wikiEnabledKB("kb-dst"), "reuse_vectors")

	require.Error(t, err)
	assert.Empty(t, wikiRepo.listedKBs)
	assert.Empty(t, pendingRepo.ops, "source Wiki cleanup must wait for the ownership checkpoint")
	knowledge := svc.repo.(*moveWikiKnowledgeRepo).knowledge
	assert.Equal(t, "kb-src", knowledge.KnowledgeBaseID)
	assert.Equal(t, types.ParseStatusCompleted, knowledge.ParseStatus)
	assert.Empty(t, opsFor(pendingRepo.ops, "kb-dst"))
}

func TestMoveOneKnowledgeRetryPersistsMissingTargetWikiRefresh(t *testing.T) {
	svc, wikiRepo, pendingRepo, chunkRepo := newMoveWikiService(t)
	pendingRepo.failEnqueues = map[string]int{"kb-dst/" + WikiOpIngest: 1}

	firstErr := svc.moveOneKnowledge(moveWikiCtx(), "kn-1",
		wikiEnabledKB("kb-src"), wikiEnabledKB("kb-dst"), "reuse_vectors")
	require.Error(t, firstErr)
	assert.Equal(t, "kb-dst", chunkRepo.movedToKB)
	assert.Equal(t, "kb-dst", svc.repo.(*moveWikiKnowledgeRepo).knowledge.KnowledgeBaseID)
	assert.Empty(t, opsFor(pendingRepo.ops, "kb-dst"))
	assert.Empty(t, wikiRepo.listedKBs,
		"source Wiki must remain intact until the target refresh is durable")

	secondErr := svc.moveOneKnowledge(moveWikiCtx(), "kn-1",
		wikiEnabledKB("kb-src"), wikiEnabledKB("kb-dst"), "reuse_vectors")
	require.NoError(t, secondErr)
	assert.Equal(t, []string{"kb-src"}, wikiRepo.listedKBs,
		"source cleanup starts only after the retry durably persists the target refresh")
	dstOps := opsFor(pendingRepo.ops, "kb-dst")
	require.Len(t, dstOps, 1)
	assert.Equal(t, WikiOpIngest, dstOps[0].Op)
	require.Len(t, opsFor(pendingRepo.ops, "kb-src"), 1)
}

func TestMoveOneKnowledgeRetryCompletesFailedSourceWikiCleanup(t *testing.T) {
	svc, wikiRepo, pendingRepo, _ := newMoveWikiService(t)
	pendingRepo.failEnqueues = map[string]int{"kb-src/" + WikiOpRetract: 1}

	firstErr := svc.moveOneKnowledge(moveWikiCtx(), "kn-1",
		wikiEnabledKB("kb-src"), wikiEnabledKB("kb-dst"), "reuse_vectors")
	require.Error(t, firstErr)
	assert.Equal(t, "kb-dst", svc.repo.(*moveWikiKnowledgeRepo).knowledge.KnowledgeBaseID)
	require.Len(t, opsFor(pendingRepo.ops, "kb-dst"), 1,
		"target refresh is already durable at the retry checkpoint")
	assert.Empty(t, opsFor(pendingRepo.ops, "kb-src"))

	secondErr := svc.moveOneKnowledge(moveWikiCtx(), "kn-1",
		wikiEnabledKB("kb-src"), wikiEnabledKB("kb-dst"), "reuse_vectors")
	require.NoError(t, secondErr)
	assert.Equal(t, []string{"kb-src", "kb-src"}, wikiRepo.listedKBs)
	require.Len(t, opsFor(pendingRepo.ops, "kb-src"), 1)
}

func TestProcessKnowledgeMoveReturnsItemFailuresForAsynqRetry(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	knowledgeRepo := &moveWikiKnowledgeRepo{knowledge: &types.Knowledge{
		ID: "kn-1", TenantID: 1, KnowledgeBaseID: "kb-src",
		ParseStatus: types.ParseStatusCompleted,
	}}
	chunkRepo := &moveWikiChunkRepo{moveErr: errors.New("move chunks failed")}
	sourceKB := &types.KnowledgeBase{ID: "kb-src"}
	targetKB := &types.KnowledgeBase{ID: "kb-dst"}
	svc := &knowledgeService{
		repo:        knowledgeRepo,
		chunkRepo:   chunkRepo,
		kbService:   &moveWikiKBService{kbs: map[string]*types.KnowledgeBase{"kb-src": sourceKB, "kb-dst": targetKB}},
		tenantRepo:  &moveWikiTenantRepo{},
		redisClient: rdb,
	}
	payload, err := json.Marshal(types.KnowledgeMovePayload{
		TaskID: "move-task", TenantID: 1, SourceKBID: "kb-src", TargetKBID: "kb-dst",
		KnowledgeIDs: []string{"kn-1"}, Mode: "reuse_vectors",
	})
	require.NoError(t, err)

	err = svc.ProcessKnowledgeMove(context.Background(), asynq.NewTask(types.TypeKnowledgeMove, payload))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "move knowledge kn-1")
	assert.Equal(t, types.ParseStatusCompleted, knowledgeRepo.knowledge.ParseStatus)
}

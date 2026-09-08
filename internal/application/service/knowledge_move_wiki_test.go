package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
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
	ops []*types.TaskPendingOp
}

func (r *moveWikiPendingRepo) Enqueue(_ context.Context, op *types.TaskPendingOp) error {
	r.ops = append(r.ops, op)
	return nil
}

func (r *moveWikiPendingRepo) DeleteByDedupKey(_ context.Context, _, _, _, _, _ string) error {
	return nil
}

type moveWikiChunkRepo struct {
	interfaces.ChunkRepository
	movedToKB string
}

func (r *moveWikiChunkRepo) ListChunksByKnowledgeID(
	_ context.Context, _ uint64, _ string,
) ([]*types.Chunk, error) {
	return nil, nil
}

func (r *moveWikiChunkRepo) MoveChunksByKnowledgeID(
	_ context.Context, _ uint64, _ string, targetKBID string,
) error {
	r.movedToKB = targetKBID
	return nil
}

func wikiEnabledKB(id string) *types.KnowledgeBase {
	return &types.KnowledgeBase{
		ID:               id,
		TenantID:         1,
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
	ctx, _ := access.WithKBTransferTask(
		context.Background(),
		wikiEnabledKB("kb-src"),
		wikiEnabledKB("kb-dst"),
		1,
		access.KBTransferMove,
		"move-task",
		false,
	)
	return ctx
}

func TestMoveOneKnowledgeRejectsUnknownModeBeforeWikiCleanup(t *testing.T) {
	svc, wikiRepo, pendingRepo, _ := newMoveWikiService(t)
	err := svc.moveOneKnowledge(moveWikiCtx(), "kn-1", wikiEnabledKB("kb-src"), wikiEnabledKB("kb-dst"), "bogus")
	require.ErrorContains(t, err, "unknown move mode")
	require.Empty(t, wikiRepo.listedKBs)
	require.Empty(t, pendingRepo.ops)
	require.Equal(t, types.ParseStatusCompleted, svc.repo.(*moveWikiKnowledgeRepo).knowledge.ParseStatus)
}

func TestMoveOneKnowledgeReuseVectorsIngestsIntoTargetKB(t *testing.T) {
	// reuse_vectors keeps the existing chunks and never re-enters the parse
	// pipeline, so nothing else would tell the target KB to build wiki pages.
	svc, _, pendingRepo, chunkRepo := newMoveWikiService(t)

	err := svc.moveOneKnowledge(moveWikiCtx(), "kn-1",
		wikiEnabledKB("kb-src"), wikiEnabledKB("kb-dst"), "reuse_vectors")

	require.NoError(t, err)
	assert.Equal(t, "kb-dst", chunkRepo.movedToKB)

	dstOps := opsFor(pendingRepo.ops, "kb-dst")
	require.Len(t, dstOps, 1)
	assert.Equal(t, WikiOpIngest, dstOps[0].Op)
	assert.Equal(t, "kn-1", dstOps[0].DedupKey)
}

func TestMoveOneKnowledgeSkipsWikiWorkForNonWikiKBs(t *testing.T) {
	svc, wikiRepo, pendingRepo, _ := newMoveWikiService(t)

	err := svc.moveOneKnowledge(
		moveWikiCtx(),
		"kn-1",
		&types.KnowledgeBase{
			ID:       "kb-src",
			TenantID: 1,
		},
		&types.KnowledgeBase{ID: "kb-dst", TenantID: 1},
		"reuse_vectors",
	)

	require.NoError(t, err)
	assert.Empty(t, wikiRepo.listedKBs)
	assert.Empty(t, pendingRepo.ops)
}

func (r *moveWikiKnowledgeRepo) UpdateKnowledgeForTransfer(
	ctx context.Context,
	_ *types.Knowledge,
	after *types.Knowledge,
) error {
	return r.UpdateKnowledge(ctx, after)
}

func (r *moveWikiChunkRepo) ListAllChunksByKnowledgeID(context.Context, uint64, string) ([]*types.Chunk, error) {
	return nil, nil
}

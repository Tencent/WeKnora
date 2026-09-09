package service

import (
	"context"
	"encoding/json"
	"errors"
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
	commitErr error
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
	listedKBs  []string
	listErr    error
	beforeList func()
	pages      []*types.WikiPage
}

func (r *moveWikiPageRepo) ListBySourceRef(
	_ context.Context, kbID, _ string,
) ([]*types.WikiPage, error) {
	r.listedKBs = append(r.listedKBs, kbID)
	if r.beforeList != nil {
		r.beforeList()
	}
	return r.pages, r.listErr
}

type moveWikiPendingRepo struct {
	interfaces.TaskPendingOpsRepository
	ops    []*types.TaskPendingOp
	err    error
	failKB string
}

func (r *moveWikiPendingRepo) Enqueue(_ context.Context, op *types.TaskPendingOp) error {
	if r.err != nil && (r.failKB == "" || r.failKB == op.ScopeID) {
		return r.err
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
	moves     int
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
	r.moves++
	if r.moveErr != nil {
		return r.moveErr
	}
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
	if after.KnowledgeBaseID == "kb-dst" && r.commitErr != nil {
		return r.commitErr
	}
	return r.UpdateKnowledge(ctx, after)
}

func (r *moveWikiChunkRepo) ListAllChunksByKnowledgeID(context.Context, uint64, string) ([]*types.Chunk, error) {
	return nil, nil
}

type moveWikiMetaService struct {
	interfaces.WikiPageService
	updated []*types.WikiPage
}

func (s *moveWikiMetaService) UpdatePageMeta(_ context.Context, page *types.WikiPage) error {
	s.updated = append(s.updated, page)
	return nil
}

func TestMoveWikiCleanupRunsOnlyAfterOwnershipCommit(t *testing.T) {
	for _, stage := range []string{"chunks", "CAS"} {
		t.Run(stage, func(t *testing.T) {
			svc, wiki, pending, chunks := newMoveWikiService(t)
			repo := svc.repo.(*moveWikiKnowledgeRepo)
			failure := errors.New("transfer failed")
			if stage == "chunks" {
				chunks.moveErr = failure
			} else {
				repo.commitErr = failure
			}
			err := svc.moveOneKnowledge(
				moveWikiCtx(),
				"kn-1",
				wikiEnabledKB("kb-src"),
				wikiEnabledKB("kb-dst"),
				"reuse_vectors",
			)
			require.ErrorIs(t, err, failure)
			require.Equal(t, "kb-src", repo.knowledge.KnowledgeBaseID)
			require.Empty(t, wiki.listedKBs)
			require.Empty(t, pending.ops)
		})
	}
}

func TestMoveWikiCleanupFailureRetriesWithoutMovingAgain(t *testing.T) {
	for _, stage := range []string{"lookup", "enqueue", "target enqueue"} {
		t.Run(stage, func(t *testing.T) {
			svc, wiki, pending, chunks := newMoveWikiService(t)
			repo := svc.repo.(*moveWikiKnowledgeRepo)
			failure := errors.New("wiki unavailable")
			if stage == "lookup" {
				wiki.listErr = failure
			} else {
				pending.err = failure
				if stage == "target enqueue" {
					pending.failKB = "kb-dst"
				}
			}
			wiki.beforeList = func() { require.Equal(t, "kb-dst", repo.knowledge.KnowledgeBaseID) }
			err := svc.moveOneKnowledge(
				moveWikiCtx(),
				"kn-1",
				wikiEnabledKB("kb-src"),
				wikiEnabledKB("kb-dst"),
				"reuse_vectors",
			)
			require.ErrorIs(t, err, failure)
			require.Equal(t, "kb-dst", repo.knowledge.KnowledgeBaseID)
			require.Equal(t, []string{"kb-src"}, wiki.listedKBs)
			require.Empty(t, opsFor(pending.ops, "kb-dst"))
			wiki.listErr = nil
			pending.err = nil
			require.NoError(
				t,
				svc.moveOneKnowledge(
					moveWikiCtx(),
					"kn-1",
					wikiEnabledKB("kb-src"),
					wikiEnabledKB("kb-dst"),
					"reuse_vectors",
				),
			)
			require.Equal(t, 1, chunks.moves)
			require.Equal(t, []string{"kb-src", "kb-src"}, wiki.listedKBs)
			require.NotEmpty(t, opsFor(pending.ops, "kb-dst"))
		})
	}
}

func TestMoveReparseWikiUsesDurableSourceRefsAfterChunksDeleted(t *testing.T) {
	f := transferFixture(t, access.KBTransferMove)
	f.kbs.values["kb"].IndexingStrategy.WikiEnabled = true
	row, err := f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
	require.NoError(t, err)
	require.NoError(
		t,
		row.SetManualMetadata(types.NewManualKnowledgeMetadata("# content", types.ManualKnowledgeStatusPublish, 1)),
	)
	row.Description = "source summary"
	require.NoError(t, f.repo.UpdateKnowledge(f.ctx, row))
	page := &types.WikiPage{
		ID:              "page",
		KnowledgeBaseID: "kb",
		SourceRefs:      types.StringArray{"doc", "other-doc"},
		ChunkRefs:       types.StringArray{"chunk", "other-chunk"},
	}
	wiki := &moveWikiPageRepo{pages: []*types.WikiPage{page}}
	meta := &moveWikiMetaService{}
	pending := &moveWikiPendingRepo{}
	wiki.beforeList = func() {
		saved, err := f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
		require.NoError(t, err)
		require.Equal(t, "other", saved.KnowledgeBaseID)
		chunks, err := f.chunkRepo.ListAllChunksByKnowledgeID(f.ctx, 7, "doc")
		require.NoError(t, err)
		require.Empty(t, chunks)
	}
	f.svc.wikiRepo = wiki
	f.svc.wikiService = meta
	f.svc.taskPendingRepo = pending
	f.svc.task = &transferQueue{}
	require.NoError(t, f.svc.ProcessKnowledgeMove(context.Background(), moveTask(t, []string{"doc"}, "reparse")))
	require.Equal(t, []string{"kb"}, wiki.listedKBs)
	require.Len(t, meta.updated, 1)
	require.Equal(t, types.StringArray{"other-doc"}, meta.updated[0].SourceRefs)
	require.Equal(t, types.StringArray{"other-chunk"}, meta.updated[0].ChunkRefs)
	ops := opsFor(pending.ops, "kb")
	require.Len(t, ops, 1)
	var payload WikiPendingOp
	require.NoError(t, json.Unmarshal(ops[0].Payload, &payload))
	require.Equal(t, "source summary", payload.DocSummary)
}

func TestMoveWikiRetractionIsDurableBeforeRemovingPageSources(t *testing.T) {
	svc, wiki, pending, chunks := newMoveWikiService(t)
	page := &types.WikiPage{ID: "page", Slug: "concept/source", SourceRefs: types.StringArray{"kn-1", "other"}}
	wiki.pages = []*types.WikiPage{page}
	meta := &moveWikiMetaService{}
	svc.wikiService = meta
	pending.err = errors.New("enqueue unavailable")
	err := svc.moveOneKnowledge(
		moveWikiCtx(),
		"kn-1",
		wikiEnabledKB("kb-src"),
		wikiEnabledKB("kb-dst"),
		"reuse_vectors",
	)
	require.ErrorIs(t, err, pending.err)
	require.Empty(t, meta.updated)
	require.Contains(t, page.SourceRefs, "kn-1")
	pending.err = nil
	require.NoError(
		t,
		svc.moveOneKnowledge(moveWikiCtx(), "kn-1", wikiEnabledKB("kb-src"), wikiEnabledKB("kb-dst"), "reuse_vectors"),
	)
	require.Equal(t, 1, chunks.moves)
	require.Len(t, meta.updated, 1)
	var op WikiPendingOp
	require.NoError(t, json.Unmarshal(opsFor(pending.ops, "kb-src")[0].Payload, &op))
	require.Equal(t, []string{"concept/source"}, op.PageSlugs)
}

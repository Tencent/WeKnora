package service

import (
	"context"
	"testing"

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
	// columnWrites records the targeted column updates the move performs.
	// A cross-KB move must explicitly reset folder_id: full-row saves omit
	// that column (see omitFieldsOnUpdate), so without this write the document
	// would land in the target KB still pointing at a folder of the source KB.
	columnWrites []map[string]interface{}
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

func (r *moveWikiKnowledgeRepo) UpdateKnowledgeColumns(
	_ context.Context, _ string, columns map[string]interface{},
) error {
	r.columnWrites = append(r.columnWrites, columns)
	if folderID, ok := columns["folder_id"].(string); ok {
		r.knowledge.FolderID = folderID
	}
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
	*moveWikiKnowledgeRepo,
) {
	t.Helper()
	wikiRepo := &moveWikiPageRepo{}
	pendingRepo := &moveWikiPendingRepo{}
	chunkRepo := &moveWikiChunkRepo{}
	knowledgeRepo := &moveWikiKnowledgeRepo{knowledge: &types.Knowledge{
		ID:              "kn-1",
		TenantID:        1,
		Title:           "Doc",
		KnowledgeBaseID: "kb-src",
		ParseStatus:     types.ParseStatusCompleted,
		// The document sits in a folder of the source KB, so the move has to
		// clear the placement rather than carry a dead folder id across.
		FolderID: "folder-in-src",
	}}
	svc := &knowledgeService{
		repo:            knowledgeRepo,
		wikiRepo:        wikiRepo,
		taskPendingRepo: pendingRepo,
		task:            &wikiGuardTaskQueue{},
		chunkRepo:       chunkRepo,
	}
	return svc, wikiRepo, pendingRepo, chunkRepo, knowledgeRepo
}

func moveWikiCtx() context.Context {
	return context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
}

func TestMoveOneKnowledgeRetractsWikiFromSourceKB(t *testing.T) {
	// An unknown mode makes the move itself a no-op, which pins the cleanup as
	// unconditional: it runs before the mode dispatch, while the knowledge still
	// belongs to the source KB.
	svc, wikiRepo, pendingRepo, _, _ := newMoveWikiService(t)

	err := svc.moveOneKnowledge(moveWikiCtx(), "kn-1",
		wikiEnabledKB("kb-src"), wikiEnabledKB("kb-dst"), "bogus")

	require.Error(t, err)
	assert.Equal(t, []string{"kb-src"}, wikiRepo.listedKBs)

	srcOps := opsFor(pendingRepo.ops, "kb-src")
	require.Len(t, srcOps, 1)
	assert.Equal(t, WikiOpRetract, srcOps[0].Op)
	assert.Equal(t, "kn-1", srcOps[0].DedupKey)
}

func TestMoveOneKnowledgeReuseVectorsIngestsIntoTargetKB(t *testing.T) {
	// reuse_vectors keeps the existing chunks and never re-enters the parse
	// pipeline, so nothing else would tell the target KB to build wiki pages.
	svc, _, pendingRepo, chunkRepo, knowledgeRepo := newMoveWikiService(t)

	err := svc.moveOneKnowledge(moveWikiCtx(), "kn-1",
		wikiEnabledKB("kb-src"), wikiEnabledKB("kb-dst"), "reuse_vectors")

	require.NoError(t, err)
	assert.Equal(t, "kb-dst", chunkRepo.movedToKB)

	dstOps := opsFor(pendingRepo.ops, "kb-dst")
	require.Len(t, dstOps, 1)
	assert.Equal(t, WikiOpIngest, dstOps[0].Op)
	assert.Equal(t, "kn-1", dstOps[0].DedupKey)

	// The folder placement is reset by an explicit column write: folder ids are
	// KB-scoped, so keeping the source KB's id would point the document at a
	// folder that does not exist in the target KB.
	require.NotEmpty(t, knowledgeRepo.columnWrites)
	assert.Equal(t, types.KnowledgeFolderRootID, knowledgeRepo.knowledge.FolderID)
}

func TestMoveOneKnowledgeSkipsWikiWorkForNonWikiKBs(t *testing.T) {
	svc, wikiRepo, pendingRepo, _, _ := newMoveWikiService(t)

	err := svc.moveOneKnowledge(moveWikiCtx(), "kn-1",
		&types.KnowledgeBase{ID: "kb-src"}, &types.KnowledgeBase{ID: "kb-dst"}, "reuse_vectors")

	require.NoError(t, err)
	assert.Empty(t, wikiRepo.listedKBs)
	assert.Empty(t, pendingRepo.ops)
}

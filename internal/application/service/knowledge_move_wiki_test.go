package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Moving a document out of a wiki-enabled KB must reconcile that KB's wiki
// state through the same authoritative provenance lifecycle as delete.

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

type moveWikiProvenanceLifecycle struct {
	interfaces.WikiProvenanceLifecycleService
	cleanedKBs []string
	err        error
}

func (s *moveWikiProvenanceLifecycle) DeleteKnowledgeSources(
	_ context.Context, _ uint64, kbID, _ string, _ time.Time,
) (*types.WikiKnowledgeSourceCleanupResult, error) {
	s.cleanedKBs = append(s.cleanedKBs, kbID)
	if s.err != nil {
		return nil, s.err
	}
	return &types.WikiKnowledgeSourceCleanupResult{}, nil
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
	*knowledgeService, *moveWikiProvenanceLifecycle, *moveWikiPendingRepo, *moveWikiChunkRepo,
) {
	t.Helper()
	provenance := &moveWikiProvenanceLifecycle{}
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
		wikiProvenance:  provenance,
		taskPendingRepo: pendingRepo,
		task:            &wikiGuardTaskQueue{},
		chunkRepo:       chunkRepo,
	}
	return svc, provenance, pendingRepo, chunkRepo
}

func moveWikiCtx() context.Context {
	return context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
}

func TestMoveOneKnowledgeRetractsWikiFromSourceKB(t *testing.T) {
	// An unknown mode makes the move itself a no-op, which pins the cleanup as
	// unconditional: it runs before the mode dispatch, while the knowledge still
	// belongs to the source KB.
	svc, provenance, pendingRepo, _ := newMoveWikiService(t)

	err := svc.moveOneKnowledge(moveWikiCtx(), "kn-1",
		wikiEnabledKB("kb-src"), wikiEnabledKB("kb-dst"), "bogus")

	require.Error(t, err)
	assert.Equal(t, []string{"kb-src"}, provenance.cleanedKBs)

	srcOps := opsFor(pendingRepo.ops, "kb-src")
	require.Len(t, srcOps, 1)
	assert.Equal(t, WikiOpRetract, srcOps[0].Op)
	assert.Equal(t, "kn-1", srcOps[0].DedupKey)
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
	svc, provenance, pendingRepo, _ := newMoveWikiService(t)

	err := svc.moveOneKnowledge(moveWikiCtx(), "kn-1",
		&types.KnowledgeBase{ID: "kb-src"}, &types.KnowledgeBase{ID: "kb-dst"}, "reuse_vectors")

	require.NoError(t, err)
	assert.Empty(t, provenance.cleanedKBs)
	assert.Empty(t, pendingRepo.ops)
}

func TestMoveOneKnowledgeStopsWhenProvenanceCleanupFails(t *testing.T) {
	svc, provenance, pendingRepo, chunkRepo := newMoveWikiService(t)
	provenance.err = errors.New("ledger unavailable")

	err := svc.moveOneKnowledge(moveWikiCtx(), "kn-1",
		wikiEnabledKB("kb-src"), wikiEnabledKB("kb-dst"), "reuse_vectors")

	require.ErrorContains(t, err, "cleanup source wiki provenance before move")
	assert.Equal(t, []string{"kb-src"}, provenance.cleanedKBs)
	assert.Empty(t, chunkRepo.movedToKB)
	assert.Empty(t, opsFor(pendingRepo.ops, "kb-dst"))
}

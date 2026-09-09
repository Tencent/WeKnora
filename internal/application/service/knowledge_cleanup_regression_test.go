package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

func TestDeleteTaskMovingConflictStopsRetries(t *testing.T) {
	f := transferFixture(t, access.KBTransferMove)
	row, err := f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
	require.NoError(t, err)
	require.NoError(t, setTransferState(row, knowledgeTransferState{Operation: access.KBTransferMove, Phase: "moving"}))
	require.NoError(t, f.repo.UpdateKnowledge(f.ctx, row))
	beforeWrites := f.repo.writes
	raw, err := json.Marshal(
		types.KnowledgeListDeletePayload{TenantID: 7, KnowledgeBaseID: "kb", KnowledgeIDs: []string{"doc"}},
	)
	require.NoError(t, err)
	err = f.svc.ProcessKnowledgeListDelete(context.Background(), asynq.NewTask(types.TypeKnowledgeListDelete, raw))
	require.ErrorIs(t, err, asynq.SkipRetry)
	require.ErrorContains(t, err, "unfinished move")
	require.Equal(t, beforeWrites, f.repo.writes)
	require.Zero(t, f.chunkRepo.writes)
	require.Zero(t, f.graph.calls)
}

func TestDeleteWikiRemovesEveryChunkType(t *testing.T) {
	f := newDocumentWriteFixture(t)
	f.kbs.values["kb"].IndexingStrategy.WikiEnabled = true
	refs := types.StringArray{"chunk"}
	chunkTypes := []string{
		types.ChunkTypeParentText, types.ChunkTypeImageOCR, types.ChunkTypeImageCaption, types.ChunkTypeSummary,
	}
	for _, typ := range chunkTypes {
		require.NoError(
			t,
			f.db.Create(
				&types.Chunk{ID: typ, TenantID: 7, KnowledgeID: "doc", KnowledgeBaseID: "kb", ChunkType: typ},
			).Error,
		)
		refs = append(refs, typ)
	}
	refs = append(refs, "other-chunk")
	page := &types.WikiPage{
		ID:         "page",
		Slug:       "concept/shared",
		SourceRefs: types.StringArray{"doc", "other-doc"},
		ChunkRefs:  refs,
	}
	wiki := &moveWikiPageRepo{pages: []*types.WikiPage{page}}
	meta := &moveWikiMetaService{}
	f.svc.wikiRepo = wiki
	f.svc.wikiService = meta
	f.svc.taskPendingRepo = &moveWikiPendingRepo{}
	f.svc.task = &transferQueue{}
	require.NoError(t, f.svc.DeleteKnowledge(f.ctx, "doc"))
	require.Len(t, meta.updated, 1)
	require.Equal(t, types.StringArray{"other-chunk"}, meta.updated[0].ChunkRefs)
	require.Equal(t, types.StringArray{"other-doc"}, meta.updated[0].SourceRefs)
}

type cleanupKBFailure struct {
	interfaces.KnowledgeBaseService
	value *types.KnowledgeBase
	err   error
}

func (s *cleanupKBFailure) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return s.value, s.err
}

func TestCleanupStopsBeforeSideEffectsWhenKBUnavailable(t *testing.T) {
	for _, scenario := range []string{"storage error", "nil KB", "wrong KB", "wrong tenant"} {
		t.Run(scenario, func(t *testing.T) {
			f := newDocumentWriteFixture(t)
			lookup := &cleanupKBFailure{}
			switch scenario {
			case "storage error":
				lookup.err = errors.New("database unavailable")
			case "wrong KB":
				lookup.value = &types.KnowledgeBase{ID: "other", TenantID: 7}
			case "wrong tenant":
				lookup.value = &types.KnowledgeBase{ID: "kb", TenantID: 8}
			}
			f.svc.kbService = lookup
			row, err := f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
			require.NoError(t, err)
			row.EmbeddingModelID = "bound-model"
			// Engine/model dependencies remain absent: touching a fallback backend
			// would panic instead of preserving the lookup error and original data.
			err = f.svc.cleanupKnowledgeResources(f.ctx, row)
			require.Error(t, err)
			if lookup.err != nil {
				require.ErrorIs(t, err, lookup.err)
			}
			require.Zero(t, f.chunkRepo.writes)
			require.Zero(t, f.graph.calls)
			require.Empty(t, f.files.deleted)
			require.Empty(t, f.tenants.adjustments)
		})
	}
}

func TestMoveReparseKBLookupFailurePreservesSourceCheckpoint(t *testing.T) {
	f := transferFixture(t, access.KBTransferMove)
	row, err := f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
	require.NoError(t, err)
	require.NoError(
		t,
		row.SetManualMetadata(types.NewManualKnowledgeMetadata("content", types.ManualKnowledgeStatusPublish, 1)),
	)
	require.NoError(t, f.repo.UpdateKnowledge(f.ctx, row))
	failure := errors.New("source KB load failed")
	f.svc.kbService = &cleanupKBFailure{err: failure}
	err = f.svc.moveOneKnowledge(f.ctx, "doc", f.kbs.values["kb"], f.kbs.values["other"], "reparse")
	require.ErrorIs(t, err, failure)
	row, err = f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
	require.NoError(t, err)
	require.Equal(t, "kb", row.KnowledgeBaseID)
	state, err := transferState(row)
	require.NoError(t, err)
	require.Equal(t, "moving", state.Phase)
	require.Zero(t, f.chunkRepo.writes)
	require.Zero(t, f.graph.calls)
}

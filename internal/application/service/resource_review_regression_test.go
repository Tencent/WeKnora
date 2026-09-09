package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/access"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

func TestReparseRejectsMovingDocumentBeforeSideEffects(t *testing.T) {
	f := newDocumentWriteFixture(t)
	row, err := f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
	require.NoError(t, err)
	require.NoError(t, setTransferState(row, knowledgeTransferState{Operation: access.KBTransferMove, Phase: "moving"}))
	require.NoError(t, f.repo.UpdateKnowledge(f.ctx, row))
	f.repo.writes = 0
	_, err = f.svc.ReparseKnowledge(f.ctx, "doc", nil)
	var appErr *apperrors.AppError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, 409, appErr.HTTPCode)
	require.Zero(t, f.repo.writes)
	require.Zero(t, f.chunkRepo.writes)
	require.Zero(t, f.graph.calls)
}

func TestReparseRequiresExplicitWriteGrant(t *testing.T) {
	f := newDocumentWriteFixture(t)
	ctx := types.WithExecutionTenant(context.Background(), 7)
	_, err := f.svc.ReparseKnowledge(ctx, "doc", nil)
	require.Error(t, err)
	require.Zero(t, f.repo.writes)
	require.Zero(t, f.chunkRepo.writes)
}

func TestReparseTaskPinsOriginalKBBeforeAnySubmission(t *testing.T) {
	for _, scenario := range []string{"moved", "missing", "mixed legacy", "blank ID", "moving"} {
		t.Run(scenario, func(t *testing.T) {
			f := newDocumentWriteFixture(t)
			payload := types.KnowledgeListReparsePayload{
				TenantID: 7, KnowledgeBaseID: "kb", KnowledgeIDs: []string{"doc"},
			}
			switch scenario {
			case "moved":
				require.NoError(
					t,
					f.db.Model(&types.Knowledge{}).Where("id = ?", "doc").Update("knowledge_base_id", "other").Error,
				)
			case "missing":
				payload.KnowledgeIDs = append(payload.KnowledgeIDs, "missing")
			case "mixed legacy":
				payload.KnowledgeBaseID = ""
				payload.KnowledgeIDs = append(payload.KnowledgeIDs, "other-doc")
			case "blank ID":
				payload.KnowledgeIDs = append(payload.KnowledgeIDs, " ")
			case "moving":
				row, err := f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
				require.NoError(t, err)
				require.NoError(
					t,
					setTransferState(row, knowledgeTransferState{Operation: access.KBTransferMove, Phase: "moving"}),
				)
				require.NoError(t, f.repo.UpdateKnowledge(f.ctx, row))
			}
			f.repo.writes = 0
			raw, err := json.Marshal(payload)
			require.NoError(t, err)
			err = f.svc.ProcessKnowledgeListReparse(
				context.Background(), asynq.NewTask(types.TypeKnowledgeListReparse, raw),
			)
			require.ErrorIs(t, err, asynq.SkipRetry)
			require.Zero(t, f.repo.writes)
			require.Zero(t, f.chunkRepo.writes)
		})
	}
}

func TestReparseTaskGrantCannotFollowMoveAfterPreflight(t *testing.T) {
	f := newDocumentWriteFixture(t)
	ctx, ids, err := f.svc.reparseTaskScope(context.Background(), types.KnowledgeListReparsePayload{
		TenantID: 7, KnowledgeBaseID: "kb", KnowledgeIDs: []string{"doc", "doc"},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"doc"}, ids)
	require.NoError(t, access.RequireKBWrite(ctx, f.kbs.values["kb"]))
	require.Error(t, access.RequireKBWrite(ctx, f.kbs.values["other"]))
	require.NoError(t, f.db.Model(&types.Knowledge{}).Where("id = ?", "doc").Update("knowledge_base_id", "other").Error)
	_, err = f.svc.ReparseKnowledge(ctx, "doc", nil)
	require.Error(t, err)
	require.Zero(t, f.repo.writes)
	require.Zero(t, f.chunkRepo.writes)
}

type sharedChunkFailureRepo struct {
	interfaces.ChunkRepository
	failure error
}

func (r sharedChunkFailureRepo) ListChunksByID(context.Context, uint64, []string) ([]*types.Chunk, error) {
	return []*types.Chunk{{ID: "own", TenantID: 1, KnowledgeBaseID: "own"}}, nil
}

func (r sharedChunkFailureRepo) ListChunksByIDOnly(context.Context, []string) ([]*types.Chunk, error) {
	return nil, r.failure
}

func TestSharedChunkReadPropagatesStorageFailure(t *testing.T) {
	failure := errors.New("shared chunk storage unavailable")
	svc := &knowledgeBaseService{chunkRepo: sharedChunkFailureRepo{failure: failure}}
	rows, err := svc.listChunksByIDWithShared(newSharedAccessContext(), 1, []string{"own", "shared"})
	require.ErrorIs(t, err, failure)
	require.Nil(t, rows)
}

func TestFAQCloneDoesNotInheritTransferState(t *testing.T) {
	f := transferFixture(t, access.KBTransferClone)
	kb := &types.KnowledgeBase{ID: "new-faq", TenantID: 7}
	src := &types.Knowledge{
		Metadata: types.JSON(`{"custom":{"value":3},"_knowledge_transfer":{"operation":"move","phase":"moving"}}`),
	}
	before := string(src.Metadata)
	dst, err := f.svc.getOrCreateFAQKnowledge(f.ctx, kb, src)
	require.NoError(t, err)
	require.JSONEq(t, `{"custom":{"value":3}}`, string(dst.Metadata))
	require.Equal(t, before, string(src.Metadata))
	require.NoError(t, access.RejectMovingKnowledge(dst))
}

type partialCloneEngine struct {
	parentChildRetrieveEngine
	vectors        map[string]int
	cleanupFailure bool
}

func (e *partialCloneEngine) Support() []types.RetrieverType {
	return []types.RetrieverType{types.VectorRetrieverType, types.KeywordsRetrieverType}
}

func (e *partialCloneEngine) CopyIndices(
	_ context.Context, _ string, documents, _ map[string]string, _ string, _ int, _ string,
) error {
	for _, id := range documents {
		e.vectors[id] = 1
	}
	return errors.New("partial vector copy")
}

func (e *partialCloneEngine) DeleteByKnowledgeIDList(_ context.Context, ids []string, _ int, _ string) error {
	if e.cleanupFailure {
		return errors.New("vector cleanup unavailable")
	}
	for _, id := range ids {
		delete(e.vectors, id)
	}
	return nil
}

func TestClonePartialIndexFailureCleansOnlyItsDestination(t *testing.T) {
	for _, cleanupFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "cleaned", true: "cleanup fails visibly"}[cleanupFailure], func(t *testing.T) {
			t.Setenv("RETRIEVE_DRIVER", "postgres")
			f := transferFixture(t, access.KBTransferClone)
			engine := &partialCloneEngine{vectors: map[string]int{"doc": 1}, cleanupFailure: cleanupFailure}
			f.svc.retrieveEngine = parentChildRetrieveRegistry{engine: engine}
			f.svc.modelService = parentChildModelService{embedder: parentChildEmbedder{}}
			f.kbs.values["other"].EmbeddingModelID = "model"
			src, err := f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
			require.NoError(t, err)
			err = f.svc.cloneKnowledge(f.ctx, src, f.kbs.values["other"])
			require.ErrorContains(t, err, "partial vector copy")
			rows, err := f.repo.ListKnowledgeByKnowledgeBaseID(f.ctx, 7, "other")
			require.NoError(t, err)
			var dst *types.Knowledge
			for _, row := range rows {
				if row.ID != "other-doc" {
					dst = row
				}
			}
			require.NotNil(t, dst)
			require.Equal(t, types.ParseStatusFailed, dst.ParseStatus)
			chunks, err := f.chunkRepo.ListAllChunksByKnowledgeID(f.ctx, 7, dst.ID)
			require.NoError(t, err)
			if cleanupFailure {
				require.Len(t, chunks, 1, "retain image references when index cleanup is uncertain")
				require.Equal(t, 1, engine.vectors[dst.ID])
				require.Contains(t, dst.ErrorMessage, "vector cleanup unavailable")
			} else {
				require.Empty(t, chunks)
				require.Zero(t, engine.vectors[dst.ID])
			}
			require.Equal(t, 1, engine.vectors["doc"])
			var tenant types.Tenant
			require.NoError(t, f.db.First(&tenant, 7).Error)
			require.EqualValues(t, 5, tenant.StorageUsed, "failed clones must not change storage accounting")
		})
	}
}

func TestReparseWorkerSubmitsPinnedAndLegacySingleKBTasks(t *testing.T) {
	for _, kbID := range []string{"kb", ""} {
		t.Run("scope="+kbID, func(t *testing.T) {
			f := newDocumentWriteFixture(t)
			row, err := f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
			require.NoError(t, err)
			require.NoError(
				t,
				row.SetManualMetadata(
					types.NewManualKnowledgeMetadata("# content", types.ManualKnowledgeStatusPublish, 1),
				),
			)
			require.NoError(t, f.repo.UpdateKnowledge(f.ctx, row))
			f.svc.task = &transferQueue{}
			raw, err := json.Marshal(types.KnowledgeListReparsePayload{
				TenantID: 7, KnowledgeBaseID: kbID, KnowledgeIDs: []string{"doc"},
			})
			require.NoError(t, err)
			require.NoError(
				t,
				f.svc.ProcessKnowledgeListReparse(
					context.Background(), asynq.NewTask(types.TypeKnowledgeListReparse, raw),
				),
			)
			row, err = f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
			require.NoError(t, err)
			require.Equal(t, types.ParseStatusPending, row.ParseStatus)
		})
	}
}

func TestMovedReparseTransferPhaseDoesNotBlockItsParser(t *testing.T) {
	row := &types.Knowledge{ID: "doc", TenantID: 7, KnowledgeBaseID: "target", ParseStatus: types.ParseStatusPending}
	for _, phase := range []string{"reparse_pending", "done"} {
		require.NoError(
			t,
			setTransferState(row, knowledgeTransferState{
				Operation: access.KBTransferMove, Mode: "reparse", Phase: phase,
			}),
		)
		require.NoError(t, validateProcessingKnowledge(row, 7, "target", "doc"))
		require.ErrorIs(t, validateProcessingKnowledge(row, 7, "source", "doc"), asynq.SkipRetry)
		require.Equal(t, types.ParseStatusPending, row.ParseStatus, "transfer state is independent of parse completion")
	}
}

type orphanMoveEngine struct {
	partialCloneEngine
	calls  int
	chunks []string
}

func (e *orphanMoveEngine) MoveKnowledgeIndices(
	_ context.Context, _, _, _ string, chunks []string, _ int, _ string,
) error {
	e.calls++
	e.chunks = append([]string(nil), chunks...)
	return nil
}

func TestMoveStillVisitsVectorBackendWithoutDatabaseChunks(t *testing.T) {
	t.Setenv("RETRIEVE_DRIVER", "postgres")
	f := transferFixture(t, access.KBTransferMove)
	engine := &orphanMoveEngine{}
	f.svc.retrieveEngine = parentChildRetrieveRegistry{engine: engine}
	f.svc.modelService = parentChildModelService{embedder: parentChildEmbedder{}}
	f.kbs.values["kb"].EmbeddingModelID = "model"
	f.kbs.values["other"].EmbeddingModelID = "model"
	require.NoError(
		t,
		f.db.Model(&types.Knowledge{}).Where("id = ?", "doc").Update("embedding_model_id", "model").Error,
	)
	require.NoError(t, f.db.Where("knowledge_id = ?", "doc").Delete(&types.Chunk{}).Error)
	require.NoError(t, f.svc.ProcessKnowledgeMove(context.Background(), moveTask(t, []string{"doc"}, "reuse_vectors")))
	require.Equal(t, 1, engine.calls)
	require.Empty(t, engine.chunks)
	row, err := f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
	require.NoError(t, err)
	require.Equal(t, "other", row.KnowledgeBaseID)
}

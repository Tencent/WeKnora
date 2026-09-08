package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func transferFixture(t *testing.T, operation access.KBTransferOperation) *documentWriteFixture {
	t.Helper()
	f := newDocumentWriteFixture(t)
	require.NoError(t, f.db.AutoMigrate(&types.Tenant{}))
	require.NoError(t, f.db.Create(&types.Tenant{ID: 7, Name: "tenant", StorageUsed: 5}).Error)
	require.NoError(
		t,
		f.db.Model(&types.Knowledge{}).Where("id = ?", "doc").Update("parse_status", types.ParseStatusCompleted).Error,
	)
	ctx, err := access.WithKBTransferTask(
		context.Background(),
		f.kbs.values["kb"],
		f.kbs.values["other"],
		7,
		operation,
		"transfer-task",
		false,
	)
	require.NoError(t, err)
	f.ctx = context.WithValue(ctx, types.TenantInfoContextKey, &types.Tenant{ID: 7, StorageUsed: 5})
	mr := miniredis.RunT(t)
	f.svc.redisClient = redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { require.NoError(t, f.svc.redisClient.Close()) })
	return f
}

func moveTask(t *testing.T, ids []string, mode string) *asynq.Task {
	t.Helper()
	raw, err := json.Marshal(
		types.KnowledgeMovePayload{
			TenantID:     7,
			TaskID:       "transfer-task",
			SourceKBID:   "kb",
			TargetKBID:   "other",
			KnowledgeIDs: ids,
			Mode:         mode,
		},
	)
	require.NoError(t, err)
	return asynq.NewTask(types.TypeKnowledgeMove, raw)
}

func TestMoveBatchPreflightHasNoPartialMutations(t *testing.T) {
	for _, ids := range [][]string{{"doc", "missing"}, {"doc", "other-doc"}, {"doc", ""}} {
		t.Run(ids[1], func(t *testing.T) {
			f := transferFixture(t, access.KBTransferMove)
			err := f.svc.ProcessKnowledgeMove(context.Background(), moveTask(t, ids, "reuse_vectors"))
			require.Error(t, err)
			row, err := f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
			require.NoError(t, err)
			require.Equal(t, types.ParseStatusCompleted, row.ParseStatus)
			require.Equal(t, "kb", row.KnowledgeBaseID)
			require.Empty(t, row.Metadata)
			require.Zero(t, f.chunkRepo.writes)
			require.Zero(t, f.graph.calls)
		})
	}
}

func TestMoveRejectsInvalidPairAndModeBeforeWrites(t *testing.T) {
	for _, change := range []string{"tenant", "same KB", "mode", "chunk", "tag"} {
		t.Run(change, func(t *testing.T) {
			f := transferFixture(t, access.KBTransferMove)
			mode := "reuse_vectors"
			switch change {
			case "tenant":
				f.kbs.values["other"].TenantID = 8
			case "same KB":
				f.kbs.values["other"].ID = "kb"
			case "mode":
				mode = "bogus"
			case "chunk":
				require.NoError(
					t,
					f.db.Model(&types.Chunk{}).Where("id = ?", "chunk").Update("knowledge_base_id", "third").Error,
				)
			case "tag":
				require.NoError(t, f.db.Model(&types.Chunk{}).Where("id = ?", "chunk").Update("tag_id", "tag").Error)
				require.NoError(
					t,
					f.db.Model(&types.KnowledgeTag{}).Where("id = ?", "tag").Update("knowledge_base_id", "other").Error,
				)
			}
			require.Error(t, f.svc.ProcessKnowledgeMove(context.Background(), moveTask(t, []string{"doc"}, mode)))
			row, err := f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
			require.NoError(t, err)
			require.Equal(t, types.ParseStatusCompleted, row.ParseStatus)
			require.Empty(t, row.Metadata)
		})
	}
}

type transferFaultRepo struct {
	interfaces.KnowledgeRepository
	failID string
	claims int
}

func (r *transferFaultRepo) DeleteKnowledgeTagRelations(ctx context.Context, id string) error {
	if id == r.failID {
		return errors.New("temporary tag cleanup failure")
	}
	return r.KnowledgeRepository.DeleteKnowledgeTagRelations(ctx, id)
}

func (r *transferFaultRepo) UpdateKnowledgeForTransfer(ctx context.Context, before, after *types.Knowledge) error {
	r.claims++
	return r.KnowledgeRepository.UpdateKnowledgeForTransfer(ctx, before, after)
}

func TestMovePartialFailureResumesAndSkipsCompletedDocuments(t *testing.T) {
	f := transferFixture(t, access.KBTransferMove)
	require.NoError(
		t,
		f.db.Create(
			&types.Knowledge{ID: "second", TenantID: 7, KnowledgeBaseID: "kb", ParseStatus: types.ParseStatusCompleted},
		).Error,
	)
	require.NoError(
		t,
		f.db.Create(
			&types.Chunk{
				ID:              "second-chunk",
				TenantID:        7,
				KnowledgeID:     "second",
				KnowledgeBaseID: "kb",
				ChunkType:       types.ChunkTypeImageOCR,
			},
		).Error,
	)
	fault := &transferFaultRepo{KnowledgeRepository: f.repo, failID: "second"}
	f.svc.repo = fault
	task := moveTask(t, []string{"doc", "doc", "second"}, "reuse_vectors")
	require.ErrorContains(t, f.svc.ProcessKnowledgeMove(context.Background(), task), "temporary tag cleanup failure")
	first, err := f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
	require.NoError(t, err)
	require.Equal(t, "other", first.KnowledgeBaseID)
	firstUpdated := first.UpdatedAt
	second, err := f.repo.GetKnowledgeByID(f.ctx, 7, "second")
	require.NoError(t, err)
	require.Equal(t, "kb", second.KnowledgeBaseID)
	state, err := transferState(second)
	require.NoError(t, err)
	require.Equal(t, "moving", state.Phase)
	fault.failID = ""
	require.NoError(t, f.svc.ProcessKnowledgeMove(context.Background(), task))
	first, err = f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
	require.NoError(t, err)
	require.Equal(t, firstUpdated, first.UpdatedAt, "completed item must not be moved again")
	second, err = f.repo.GetKnowledgeByID(f.ctx, 7, "second")
	require.NoError(t, err)
	require.Equal(t, "other", second.KnowledgeBaseID)
	require.Equal(t, types.ParseStatusCompleted, second.ParseStatus)
	progress, err := f.svc.GetKnowledgeMoveProgress(f.ctx, "transfer-task")
	require.NoError(t, err)
	require.Equal(t, 2, progress.Total)
	require.Zero(t, progress.Failed)
	require.Equal(t, types.KBCloneStatusCompleted, progress.Status)
}

type transferQueue struct {
	fail bool
	ids  []string
}

func (q *transferQueue) Enqueue(_ *asynq.Task, options ...asynq.Option) (*asynq.TaskInfo, error) {
	for _, option := range options {
		if option.Type() == asynq.TaskIDOpt {
			q.ids = append(q.ids, option.Value().(string))
		}
	}
	if q.fail {
		return nil, errors.New("queue unavailable")
	}
	return &asynq.TaskInfo{ID: "processing-task"}, nil
}

func TestMoveReparseRetryOnlyReenqueuesAndAccountsOnce(t *testing.T) {
	f := transferFixture(t, access.KBTransferMove)
	row, err := f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
	require.NoError(t, err)
	require.NoError(
		t,
		row.SetManualMetadata(types.NewManualKnowledgeMetadata("# content", types.ManualKnowledgeStatusPublish, 1)),
	)
	require.NoError(t, f.repo.UpdateKnowledge(f.ctx, row))
	queue := &transferQueue{fail: true}
	f.svc.task = queue
	task := moveTask(t, []string{"doc"}, "reparse")
	require.ErrorContains(t, f.svc.ProcessKnowledgeMove(context.Background(), task), "queue unavailable")
	row, err = f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
	require.NoError(t, err)
	require.Equal(t, "other", row.KnowledgeBaseID)
	require.Zero(t, row.StorageSize)
	cleanupCalls := f.graph.calls
	var tenant types.Tenant
	require.NoError(t, f.db.First(&tenant, 7).Error)
	require.Zero(t, tenant.StorageUsed)
	// A parser may already have written manual metadata before the move's ack.
	meta, err := row.ManualMetadata()
	require.NoError(t, err)
	require.NoError(t, row.SetManualMetadata(meta))
	require.NoError(t, f.repo.UpdateKnowledge(f.ctx, row))
	queue.fail = false
	require.NoError(t, f.svc.ProcessKnowledgeMove(context.Background(), task))
	require.Equal(t, cleanupCalls, f.graph.calls)
	require.Len(t, queue.ids, 2)
	require.Equal(t, queue.ids[0], queue.ids[1])
	require.NoError(t, f.db.First(&tenant, 7).Error)
	require.Zero(t, tenant.StorageUsed)
	row, err = f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
	require.NoError(t, err)
	state, err := transferState(row)
	require.NoError(t, err)
	require.Equal(t, "done", state.Phase)
}

type cloneFaultChunks struct {
	interfaces.ChunkRepository
	fail bool
}

func (r *cloneFaultChunks) CreateChunks(ctx context.Context, chunks []*types.Chunk) error {
	if err := r.ChunkRepository.CreateChunks(ctx, chunks); err != nil {
		return err
	}
	if r.fail {
		return errors.New("lost clone acknowledgement")
	}
	return nil
}

func TestCloneRetryReplacesIncompleteCopyWithoutDuplicatingEmptyHashes(t *testing.T) {
	f := transferFixture(t, access.KBTransferClone)
	fault := &cloneFaultChunks{ChunkRepository: f.chunkRepo, fail: true}
	f.svc.chunkRepo = fault
	require.ErrorContains(
		t,
		f.svc.executeKnowledgeClone(f.ctx, f.kbs.values["kb"], f.kbs.values["other"], nil),
		"lost clone acknowledgement",
	)
	rows, err := f.repo.ListKnowledgeByKnowledgeBaseID(f.ctx, 7, "other")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, types.ParseStatusFailed, rows[0].ParseStatus)
	failedChunks, chunkErr := f.chunkRepo.ListAllChunksByKnowledgeID(f.ctx, 7, rows[0].ID)
	require.NoError(t, chunkErr)
	require.Empty(t, failedChunks, "a lost create acknowledgement must also be rolled back")
	fault.fail = false
	require.NoError(t, f.svc.executeKnowledgeClone(f.ctx, f.kbs.values["kb"], f.kbs.values["other"], nil))
	rows, err = f.repo.ListKnowledgeByKnowledgeBaseID(f.ctx, 7, "other")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, types.ParseStatusCompleted, rows[0].ParseStatus)
	id := rows[0].ID
	require.NoError(t, f.svc.executeKnowledgeClone(f.ctx, f.kbs.values["kb"], f.kbs.values["other"], nil))
	rows, err = f.repo.ListKnowledgeByKnowledgeBaseID(f.ctx, 7, "other")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, id, rows[0].ID)
	var tenant types.Tenant
	require.NoError(t, f.db.First(&tenant, 7).Error)
	require.EqualValues(t, 10, tenant.StorageUsed)
}

func TestClonePreflightDoesNotDeleteTargetForUnreadySource(t *testing.T) {
	f := transferFixture(t, access.KBTransferClone)
	require.NoError(
		t,
		f.db.Model(&types.Knowledge{}).Where("id = ?", "doc").Update("parse_status", types.ParseStatusProcessing).Error,
	)
	require.Error(t, f.svc.executeKnowledgeClone(f.ctx, f.kbs.values["kb"], f.kbs.values["other"], nil))
	_, err := f.repo.GetKnowledgeByID(f.ctx, 7, "other-doc")
	require.NoError(t, err)
	require.Zero(t, f.repo.writes)
	require.Zero(t, f.chunkRepo.writes)
}

func TestTransferCheckpointCannotResurrectOrOverwriteChangedDocument(t *testing.T) {
	f := transferFixture(t, access.KBTransferMove)
	before, err := f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
	require.NoError(t, err)
	after := *before
	after.StorageSize = 0
	after.KnowledgeBaseID = "other"
	require.NoError(t, f.db.Model(&types.Knowledge{}).Where("id = ?", "doc").Update("knowledge_base_id", "third").Error)
	require.Error(t, f.repo.UpdateKnowledgeForTransfer(f.ctx, before, &after))
	var tenant types.Tenant
	require.NoError(t, f.db.First(&tenant, 7).Error)
	require.EqualValues(t, 5, tenant.StorageUsed)
	require.NoError(t, f.db.Where("id = ?", "doc").Delete(&types.Knowledge{}).Error)
	require.Error(t, f.repo.UpdateKnowledgeForTransfer(f.ctx, before, &after))
	_, err = f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
	require.Error(t, err)
}

func TestProcessingTasksRejectOldKnowledgeBaseAfterMove(t *testing.T) {
	for _, taskType := range []string{types.TypeDocumentProcess, types.TypeManualProcess} {
		t.Run(taskType, func(t *testing.T) {
			f := transferFixture(t, access.KBTransferMove)
			require.NoError(
				t,
				f.db.Model(&types.Knowledge{}).
					Where("id = ?", "doc").
					Updates(map[string]any{"knowledge_base_id": "other", "parse_status": types.ParseStatusPending}).
					Error,
			)
			payload, err := json.Marshal(
				types.DocumentProcessPayload{TenantID: 7, KnowledgeID: "doc", KnowledgeBaseID: "kb"},
			)
			require.NoError(t, err)
			task := asynq.NewTask(taskType, payload)
			if taskType == types.TypeDocumentProcess {
				err = f.svc.ProcessDocument(context.Background(), task)
			} else {
				err = f.svc.ProcessManualUpdate(context.Background(), task)
			}
			require.ErrorIs(t, err, asynq.SkipRetry)
			require.Zero(t, f.repo.writes)
			require.Zero(t, f.graph.calls)
		})
	}
}

func TestCopyServiceResumesReservedTargetAndRequiresTransferGrant(t *testing.T) {
	repo := newFakeKBRepo()
	repo.rows["src"] = &types.KnowledgeBase{ID: "src", TenantID: 1}
	svc := newPR3KBService(repo, &fakeRegistry{}, &fakeOwnership{})
	_, _, err := svc.CopyKnowledgeBase(ctxWithTenant(1), "src", "")
	require.ErrorIs(t, err, access.ErrForbidden)
	require.Len(t, repo.rows, 1)
	ctx, err := access.WithKBTransferTask(
		context.Background(),
		repo.rows["src"],
		&types.KnowledgeBase{ID: "reserved", TenantID: 1, CreatorID: "creator"},
		1,
		access.KBTransferClone,
		"task",
		true,
	)
	require.NoError(t, err)
	for range 2 {
		_, target, err := svc.CopyKnowledgeBase(ctx, "src", "reserved")
		require.NoError(t, err)
		require.Equal(t, "reserved", target.ID)
		require.Equal(t, "creator", target.CreatorID)
	}
	require.Len(t, repo.rows, 2)
	_, _, err = svc.CopyKnowledgeBase(ctx, "src", "other")
	require.ErrorIs(t, err, access.ErrForbidden)
}

func TestProcessingCannotEnterClaimedMoveBeforeKBBindingChanges(t *testing.T) {
	f := transferFixture(t, access.KBTransferMove)
	row, err := f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
	require.NoError(t, err)
	require.NoError(
		t,
		setTransferState(
			row,
			knowledgeTransferState{
				TaskID:    "transfer-task",
				Operation: access.KBTransferMove,
				SourceKB:  "kb",
				TargetKB:  "other",
				SourceID:  "doc",
				Mode:      "reuse_vectors",
				Phase:     "moving",
			},
		),
	)
	row.ParseStatus = types.ParseStatusProcessing
	require.NoError(t, f.repo.UpdateKnowledge(f.ctx, row))
	f.repo.writes = 0
	payload, err := json.Marshal(types.DocumentProcessPayload{TenantID: 7, KnowledgeID: "doc", KnowledgeBaseID: "kb"})
	require.NoError(t, err)
	require.ErrorIs(
		t,
		f.svc.ProcessDocument(context.Background(), asynq.NewTask(types.TypeDocumentProcess, payload)),
		asynq.SkipRetry,
	)
	require.Zero(t, f.repo.writes)
	require.Zero(t, f.graph.calls)
}

func TestAdmissionProgressCannotOverwriteFinishedWorker(t *testing.T) {
	f := transferFixture(t, access.KBTransferMove)
	require.NoError(
		t,
		f.svc.saveKnowledgeMoveProgress(
			f.ctx,
			&types.KnowledgeMoveProgress{TaskID: "task", Status: types.KBCloneStatusCompleted, Progress: 100},
		),
	)
	require.NoError(
		t,
		f.svc.SaveKnowledgeMoveProgress(
			f.ctx,
			&types.KnowledgeMoveProgress{TaskID: "task", Status: types.KBCloneStatusPending},
		),
	)
	progress, err := f.svc.GetKnowledgeMoveProgress(f.ctx, "task")
	require.NoError(t, err)
	require.Equal(t, types.KBCloneStatusCompleted, progress.Status)
	require.Equal(t, 100, progress.Progress)
}

func TestClonePreservesFailedChunkIndexStatus(t *testing.T) {
	f := transferFixture(t, access.KBTransferClone)
	require.NoError(t, f.db.Exec(`CREATE TRIGGER truncate_clone_created_time AFTER INSERT ON knowledges
        BEGIN UPDATE knowledges SET updated_at = strftime('%Y-%m-%d %H:%M:%S', NEW.updated_at) || '+00:00'
        WHERE id = NEW.id; END`).Error)
	require.NoError(t, f.db.Model(&types.Chunk{}).Where("id = ?", "chunk").Update("index_status", "failed").Error)
	require.NoError(t, f.svc.executeKnowledgeClone(f.ctx, f.kbs.values["kb"], f.kbs.values["other"], nil))
	rows, err := f.repo.ListKnowledgeByKnowledgeBaseID(f.ctx, 7, "other")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	chunks, err := f.chunkRepo.ListAllChunksByKnowledgeID(f.ctx, 7, rows[0].ID)
	require.NoError(t, err)
	require.Len(t, chunks, 1)
	require.Equal(t, "failed", chunks[0].IndexStatus)
}

func TestTableSummaryCleanupDoesNotFollowMovedDocument(t *testing.T) {
	f := newDocumentWriteFixture(t)
	row, err := f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
	require.NoError(t, err)
	chunk, err := f.chunkRepo.GetChunkByID(f.ctx, 7, "chunk")
	require.NoError(t, err)
	require.NoError(t, f.db.Model(&types.Knowledge{}).Where("id = ?", "doc").Update("knowledge_base_id", "other").Error)
	worker := &DataTableSummaryService{knowledgeService: f.svc, chunkService: f.chunks}
	worker.cleanupOnFailure(
		f.ctx, &extractionResources{knowledge: row}, []*types.Chunk{chunk}, errors.New("index failed"),
	)
	f.requireNoWrites(t)
	current, err := f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
	require.NoError(t, err)
	require.Equal(t, "other", current.KnowledgeBaseID)
	require.Equal(t, row.ParseStatus, current.ParseStatus)
}

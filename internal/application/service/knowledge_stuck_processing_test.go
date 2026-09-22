package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A failed processing→finalizing handoff must be retried, not acked: acking
// read as "no longer processing" and left the row stuck in processing.
func TestPostProcessRetriesWhenFinalizingHandoffFails(t *testing.T) {
	const knowledgeID = "knowledge-handoff-fails"
	queue := &wikiEnqueueFailureTaskQueue{}
	svc, repo := newWikiEnqueueTestService(knowledgeID, &wikiEnqueueFailurePendingRepo{}, queue)
	svc.kbService.(*wikiEnqueueFailureKBService).kb.IndexingStrategy.WikiEnabled = false
	repo.setFinalizingErr = errors.New("postgres unavailable")

	err := svc.Handle(context.Background(), newWikiEnqueuePostProcessTask(t, knowledgeID))

	require.ErrorIs(t, err, repo.setFinalizingErr)
	assert.Equal(t, types.ParseStatusProcessing, repo.knowledge.ParseStatus)
	assert.Empty(t, queue.taskTypes, "no enrichment may fan out before the handoff lands")
}

type wikiUnavailablePendingRepo struct {
	interfaces.TaskPendingOpsRepository
	drainKeys []string
	drainErr  error
	drained   []string
}

func (r *wikiUnavailablePendingRepo) DrainUnclaimed(
	_ context.Context, taskType, scope, scopeID, op string, _ time.Time,
) ([]string, error) {
	r.drained = append(r.drained, taskType+"|"+scope+"|"+scopeID+"|"+op)
	return r.drainKeys, r.drainErr
}

type wikiUnavailableModelService struct {
	interfaces.ModelService
	err error
}

func (s *wikiUnavailableModelService) GetChatModel(context.Context, string) (chat.Chat, error) {
	return nil, s.err
}

// A wiki that cannot run (disabled, no model, model deleted) fails the same
// way on every retry, so its queued ingest ops must be released instead of
// holding their documents in "finalizing" forever.
func TestWikiIngestReleasesDocumentsWhenWikiUnavailable(t *testing.T) {
	payload, err := json.Marshal(WikiIngestPayload{TenantID: 7, KnowledgeBaseID: "kb-1"})
	require.NoError(t, err)
	enabled := types.IndexingStrategy{WikiEnabled: true}
	tests := []struct {
		name     string
		kb       *types.KnowledgeBase
		modelErr error
	}{
		{name: "wiki disabled", kb: &types.KnowledgeBase{ID: "kb-1"}},
		{name: "no synthesis model", kb: &types.KnowledgeBase{ID: "kb-1", IndexingStrategy: enabled}},
		{
			name: "synthesis model deleted",
			kb: &types.KnowledgeBase{
				ID: "kb-1", IndexingStrategy: enabled,
				WikiConfig: &types.WikiConfig{SynthesisModelID: "gone"},
			},
			modelErr: ErrModelNotFound,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pending := &wikiUnavailablePendingRepo{drainKeys: []string{"k-1", "k-2"}}
			knowledgeRepo := &wikiEnqueueFailureKnowledgeRepo{}
			svc := &wikiIngestService{
				kbService:     &wikiGuardKBService{kb: test.kb},
				modelService:  &wikiUnavailableModelService{err: test.modelErr},
				pendingRepo:   pending,
				knowledgeRepo: knowledgeRepo,
			}

			err := svc.ProcessWikiIngest(context.Background(), asynq.NewTask(types.TypeWikiIngest, payload))

			require.NoError(t, err)
			assert.Equal(t, []string{wikiTaskType + "|" + wikiTaskScope + "|kb-1|" + WikiOpIngest}, pending.drained)
			assert.Equal(t, []string{"k-1", "k-2"}, knowledgeRepo.finalized)
		})
	}
}

// A transient model lookup failure keeps the ops and retries as before.
func TestWikiIngestKeepsOpsOnTransientModelError(t *testing.T) {
	payload, err := json.Marshal(WikiIngestPayload{TenantID: 7, KnowledgeBaseID: "kb-1"})
	require.NoError(t, err)
	pending := &wikiUnavailablePendingRepo{}
	transient := errors.New("connection reset")
	svc := &wikiIngestService{
		kbService: &wikiGuardKBService{kb: &types.KnowledgeBase{
			ID: "kb-1", IndexingStrategy: types.IndexingStrategy{WikiEnabled: true},
			WikiConfig: &types.WikiConfig{SynthesisModelID: "m-1"},
		}},
		modelService: &wikiUnavailableModelService{err: transient},
		pendingRepo:  pending,
	}

	err = svc.ProcessWikiIngest(context.Background(), asynq.NewTask(types.TypeWikiIngest, payload))

	require.ErrorIs(t, err, transient)
	assert.Empty(t, pending.drained)
}

type abortCheckRepo struct {
	interfaces.KnowledgeRepository
	knowledge *types.Knowledge
	err       error
}

func (r *abortCheckRepo) GetKnowledgeByID(context.Context, uint64, string) (*types.Knowledge, error) {
	return r.knowledge, r.err
}

// Only a row that is really gone may read as deleting; callers wipe chunks
// and index on that status.
func TestIsKnowledgeAbortedDistinguishesMissingFromUnreadable(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	tests := []struct {
		name        string
		ctx         context.Context
		repo        *abortCheckRepo
		wantAborted bool
		wantStatus  string
	}{
		{
			name: "missing", ctx: context.Background(),
			repo:        &abortCheckRepo{err: apprepo.ErrKnowledgeNotFound},
			wantAborted: true, wantStatus: types.ParseStatusDeleting,
		},
		{
			name: "transient read error", ctx: context.Background(),
			repo: &abortCheckRepo{err: errors.New("connection reset")},
		},
		{
			name: "worker context done", ctx: cancelled,
			repo:        &abortCheckRepo{err: context.Canceled},
			wantAborted: true, wantStatus: abortStatusInterrupted,
		},
		{
			name: "cancelled", ctx: context.Background(),
			repo:        &abortCheckRepo{knowledge: &types.Knowledge{ParseStatus: types.ParseStatusCancelled}},
			wantAborted: true, wantStatus: types.ParseStatusCancelled,
		},
		{
			name: "processing", ctx: context.Background(),
			repo:       &abortCheckRepo{knowledge: &types.Knowledge{ParseStatus: types.ParseStatusProcessing}},
			wantStatus: types.ParseStatusProcessing,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			svc := &knowledgeService{repo: test.repo}
			aborted, status := svc.isKnowledgeAborted(test.ctx, 1, "k-1")
			assert.Equal(t, test.wantAborted, aborted)
			assert.Equal(t, test.wantStatus, status)
		})
	}
}

// Rows held only by a durable wiki op get their KB's trigger re-armed, once
// per KB and at most once per threshold.
func TestHousekeepingRearmsWikiTriggerForDurablyHeldRows(t *testing.T) {
	db := setupHousekeepingDB(t)
	queue := &wikiGuardTaskQueue{}
	svc := newHousekeepingSvcForTest(db)
	svc.task = queue
	stale := time.Now().Add(-3 * time.Hour)
	for _, id := range []string{"k-1", "k-2"} {
		require.NoError(t, db.Exec(
			`INSERT INTO knowledges (id, tenant_id, knowledge_base_id, parse_status, updated_at)
			 VALUES (?, 7, 'kb-1', ?, ?)`, id, types.ParseStatusFinalizing, stale,
		).Error)
		insertWikiPendingOp(t, db, "kb-1", id)
	}

	svc.runSweep(context.Background())
	svc.runSweep(context.Background())

	require.Len(t, queue.tasks, 1)
	assert.Equal(t, types.TypeWikiIngest, queue.tasks[0].Type())
	var payload WikiIngestPayload
	require.NoError(t, json.Unmarshal(queue.tasks[0].Payload(), &payload))
	assert.Equal(t, uint64(7), payload.TenantID)
	assert.Equal(t, "kb-1", payload.KnowledgeBaseID)
	var status string
	require.NoError(t, db.Raw(`SELECT parse_status FROM knowledges WHERE id = 'k-1'`).Scan(&status).Error)
	assert.Equal(t, types.ParseStatusFinalizing, status)
}

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

func (r *wikiUnavailablePendingRepo) DrainUnclaimedAndRelease(
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
			svc := &wikiIngestService{
				kbService:    &wikiGuardKBService{kb: test.kb},
				modelService: &wikiUnavailableModelService{err: test.modelErr},
				pendingRepo:  pending,
			}

			err := svc.ProcessWikiIngest(context.Background(), asynq.NewTask(types.TypeWikiIngest, payload))

			require.NoError(t, err)
			assert.Equal(t, []string{wikiTaskType + "|" + wikiTaskScope + "|kb-1|" + WikiOpIngest}, pending.drained)
		})
	}
}

// A drain that fails (and so rolled back) is retried, not acked.
func TestWikiIngestRetriesWhenReleaseFails(t *testing.T) {
	payload, err := json.Marshal(WikiIngestPayload{TenantID: 7, KnowledgeBaseID: "kb-1"})
	require.NoError(t, err)
	releaseErr := errors.New("postgres unavailable")
	svc := &wikiIngestService{
		kbService:   &wikiGuardKBService{kb: &types.KnowledgeBase{ID: "kb-1"}},
		pendingRepo: &wikiUnavailablePendingRepo{drainErr: releaseErr},
	}

	err = svc.ProcessWikiIngest(context.Background(), asynq.NewTask(types.TypeWikiIngest, payload))

	require.ErrorIs(t, err, releaseErr)
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
			repo:        &abortCheckRepo{err: errors.New("connection reset")},
			wantAborted: true, wantStatus: abortStatusUnreadable,
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
// per KB and at most once per threshold, in the language the ops were
// queued with rather than the server default.
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
		require.NoError(t, db.Exec(
			`INSERT INTO task_pending_ops (tenant_id, task_type, scope, scope_id, op, dedup_key, payload)
			 VALUES (7, ?, ?, 'kb-1', ?, ?, ?)`,
			wikiTaskType, wikiTaskScope, WikiOpIngest, id,
			`{"op":"ingest","knowledge_id":"`+id+`","language":"en-US"}`,
		).Error)
	}

	svc.runSweep(context.Background())
	svc.runSweep(context.Background())

	require.Len(t, queue.tasks, 1)
	assert.Equal(t, types.TypeWikiIngest, queue.tasks[0].Type())
	var payload WikiIngestPayload
	require.NoError(t, json.Unmarshal(queue.tasks[0].Payload(), &payload))
	assert.Equal(t, uint64(7), payload.TenantID)
	assert.Equal(t, "kb-1", payload.KnowledgeBaseID)
	assert.Equal(t, "en-US", payload.Language)
	var status string
	require.NoError(t, db.Raw(`SELECT parse_status FROM knowledges WHERE id = 'k-1'`).Scan(&status).Error)
	assert.Equal(t, types.ParseStatusFinalizing, status)
}

type pipelineReadFailRepo struct {
	interfaces.KnowledgeRepository
	err     error
	updates int
}

func (r *pipelineReadFailRepo) GetKnowledgeByID(context.Context, uint64, string) (*types.Knowledge, error) {
	return nil, r.err
}

func (r *pipelineReadFailRepo) UpdateKnowledge(context.Context, *types.Knowledge) error {
	r.updates++
	return nil
}

// A row the abort check cannot read must stop the pipeline without writing
// the in-memory row back (that Save would clobber an unseen cancel), and the
// task must be retried rather than acked. chunkRepo is nil on purpose: going
// on past the check would panic.
func TestProcessChunksRetriesWhenAbortCheckCannotRead(t *testing.T) {
	repo := &pipelineReadFailRepo{err: errors.New("connection reset")}
	svc := &knowledgeService{repo: repo}
	knowledge := &types.Knowledge{ID: "k-1", TenantID: 1, ParseStatus: types.ParseStatusProcessing}

	err := svc.processChunks(context.Background(), &types.KnowledgeBase{ID: "kb-1"}, knowledge,
		[]types.ParsedChunk{{Content: "body"}})

	require.Error(t, err)
	assert.Zero(t, repo.updates)
}

type vectorStoreOwnership struct {
	owned bool
	err   error
}

func (o vectorStoreOwnership) StoreOwnedBy(context.Context, string, uint64) (bool, error) {
	return o.owned, o.err
}

type deleteCountingChunkRepo struct {
	interfaces.ChunkRepository
	deletes int
}

func (r *deleteCountingChunkRepo) DeleteChunksByKnowledgeID(context.Context, uint64, string) error {
	r.deletes++
	return nil
}

// The vector store is resolved before the old chunks are deleted: a store
// that is gone fails the attempt with the document's data intact, and an
// interrupted lookup is retried with nothing written.
func TestProcessChunksResolvesVectorStoreBeforeDeletingChunks(t *testing.T) {
	storeID := "store-1"
	kb := &types.KnowledgeBase{
		ID: "kb-1", TenantID: 1, EmbeddingModelID: "embedding-1", VectorStoreID: &storeID,
		IndexingStrategy: types.IndexingStrategy{VectorEnabled: true},
	}
	tests := []struct {
		name       string
		ownership  vectorStoreOwnership
		wantErr    bool
		wantStatus string
	}{
		{name: "store gone", ownership: vectorStoreOwnership{owned: false}, wantStatus: types.ParseStatusFailed},
		{name: "lookup interrupted", ownership: vectorStoreOwnership{err: context.Canceled}, wantErr: true},
		{name: "lookup failed", ownership: vectorStoreOwnership{err: errors.New("connection reset")}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			knowledge := &types.Knowledge{
				ID: "k-1", TenantID: 1, KnowledgeBaseID: "kb-1", ParseStatus: types.ParseStatusProcessing,
			}
			repo := &embedFailureKnowledgeRepo{knowledge: knowledge}
			chunks := &deleteCountingChunkRepo{}
			svc := &knowledgeService{
				repo:         repo,
				chunkRepo:    chunks,
				modelService: parentChildModelService{embedder: parentChildEmbedder{}},
				ownership:    test.ownership,
			}
			ctx := context.WithValue(context.Background(), types.TenantInfoContextKey, &types.Tenant{ID: 1})

			err := svc.processChunks(ctx, kb, knowledge, []types.ParsedChunk{{Content: "body"}})

			assert.Zero(t, chunks.deletes, "existing chunks must survive an unresolvable vector store")
			if test.wantErr {
				require.Error(t, err)
				assert.Empty(t, repo.updates)
				return
			}
			require.NoError(t, err)
			require.Len(t, repo.updates, 1)
			assert.Equal(t, test.wantStatus, repo.updates[0].ParseStatus)
			assert.Contains(t, repo.updates[0].ErrorMessage, "failed to resolve vector store")
		})
	}
}

package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// summaryColumnsRepo fails the test on a full-row save and records every
// column write, so a test can check the summary task only touches the
// columns it owns.
type summaryColumnsRepo struct {
	interfaces.KnowledgeRepository
	t         *testing.T
	knowledge *types.Knowledge
	writes    []map[string]interface{}
}

func (r *summaryColumnsRepo) GetKnowledgeByID(context.Context, uint64, string) (*types.Knowledge, error) {
	k := *r.knowledge
	return &k, nil
}

func (r *summaryColumnsRepo) UpdateKnowledge(context.Context, *types.Knowledge) error {
	r.t.Fatal("the summary task must not save the whole row")
	return nil
}

func (r *summaryColumnsRepo) UpdateKnowledgeColumns(_ context.Context, _ string, values map[string]interface{}) error {
	r.writes = append(r.writes, values)
	return nil
}

func (r *summaryColumnsRepo) FinalizeSubtask(context.Context, string, int) (int, bool, error) {
	return 0, false, nil
}

type emptySummaryChunkRepo struct {
	interfaces.ChunkRepository
}

func (emptySummaryChunkRepo) ListChunksByKnowledgeID(context.Context, uint64, string) ([]*types.Chunk, error) {
	return nil, nil
}

func summaryKnowledge() *types.Knowledge {
	return &types.Knowledge{
		ID: "k-1", TenantID: 1, KnowledgeBaseID: "kb-1",
		ParseStatus: types.ParseStatusFinalizing, SummaryStatus: types.SummaryStatusPending,
	}
}

func summaryKB() *types.KnowledgeBase {
	return &types.KnowledgeBase{ID: "kb-1", TenantID: 1, SummaryModelID: "summary-model"}
}

func summaryGenerationTask(t *testing.T) *asynq.Task {
	t.Helper()
	payload, err := json.Marshal(types.SummaryGenerationPayload{
		TenantID: 1, KnowledgeID: "k-1", KnowledgeBaseID: "kb-1",
	})
	require.NoError(t, err)
	return asynq.NewTask(types.TypeSummaryGeneration, payload)
}

func assertSummaryColumnsOnly(t *testing.T, writes []map[string]interface{}) {
	t.Helper()
	require.NotEmpty(t, writes)
	for _, values := range writes {
		for column := range values {
			assert.Contains(t, []string{"description", "profile", "summary_status", "updated_at"}, column)
		}
	}
}

// The summary task holds a row snapshot across its LLM call; a full-row save
// of it wrote that moment's parse status back over a cancel, a housekeeping
// failure or a reparse that landed meanwhile.
func TestProcessSummaryGenerationWritesOnlySummaryColumns(t *testing.T) {
	repo := &summaryColumnsRepo{t: t, knowledge: summaryKnowledge()}
	svc := &knowledgeService{
		repo:         repo,
		kbService:    &reparseFailureKBService{kb: summaryKB()},
		chunkService: &wikiEnqueueFailureChunkService{},
	}

	err := svc.ProcessSummaryGeneration(context.Background(), summaryGenerationTask(t))

	require.NoError(t, err)
	assertSummaryColumnsOnly(t, repo.writes)
	last := repo.writes[len(repo.writes)-1]
	assert.Equal(t, types.SummaryStatusFailed, last["summary_status"], "no text chunks fails the summary")
}

func TestRegenerateKnowledgeSummaryWritesOnlySummaryColumns(t *testing.T) {
	repo := &summaryColumnsRepo{t: t, knowledge: summaryKnowledge()}
	svc := &knowledgeService{
		repo:      repo,
		kbService: &reparseFailureKBService{kb: summaryKB()},
		chunkRepo: emptySummaryChunkRepo{},
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))

	_, err := svc.RegenerateKnowledgeSummary(ctx, "k-1")

	require.ErrorIs(t, err, errInsufficientSummaryContent)
	assertSummaryColumnsOnly(t, repo.writes)
}

// cancellingChunkService cancels the knowledge in the database while the
// summary task is running, as a user cancel landing mid-run would.
type cancellingChunkService struct {
	interfaces.ChunkService
	db *gorm.DB
	t  *testing.T
}

func (s *cancellingChunkService) ListChunksByKnowledgeID(context.Context, string) ([]*types.Chunk, error) {
	require.NoError(s.t, s.db.Model(&types.Knowledge{}).Where("id = ?", "k-1").
		Updates(map[string]interface{}{
			"parse_status":           types.ParseStatusCancelled,
			"pending_subtasks_count": 0,
		}).Error)
	return nil, nil
}

func TestProcessSummaryGenerationKeepsCancelThatLandedMidRun(t *testing.T) {
	db := setupKnowledgeSharedAccessDB(t)
	seed := summaryKnowledge()
	seed.PendingSubtasksCount = 1
	seedKnowledge(t, db, seed)
	svc := &knowledgeService{
		repo:         repository.NewKnowledgeRepository(db),
		kbService:    &reparseFailureKBService{kb: summaryKB()},
		chunkService: &cancellingChunkService{db: db, t: t},
	}

	require.NoError(t, svc.ProcessSummaryGeneration(context.Background(), summaryGenerationTask(t)))

	var row types.Knowledge
	require.NoError(t, db.Where("id = ?", "k-1").First(&row).Error)
	assert.Equal(t, types.ParseStatusCancelled, row.ParseStatus)
	assert.Equal(t, types.SummaryStatusFailed, row.SummaryStatus)
}

// A cancel ends the run's summary task with it, so a summary still pending or
// processing must not keep a spinner on the cancelled row; a summary that
// already finished is kept.
func TestCancelKnowledgeParseClosesUnfinishedSummary(t *testing.T) {
	tests := []struct {
		summary string
		want    string
	}{
		{types.SummaryStatusPending, types.SummaryStatusNone},
		{types.SummaryStatusProcessing, types.SummaryStatusNone},
		{types.SummaryStatusCompleted, types.SummaryStatusCompleted},
	}
	for _, test := range tests {
		t.Run(test.summary, func(t *testing.T) {
			db := setupKnowledgeSharedAccessDB(t)
			seed := summaryKnowledge()
			seed.SummaryStatus = test.summary
			seed.PendingSubtasksCount = 2
			seedKnowledge(t, db, seed)
			svc := &knowledgeService{repo: repository.NewKnowledgeRepository(db)}
			ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))

			got, err := svc.CancelKnowledgeParse(ctx, "k-1")

			require.NoError(t, err)
			assert.Equal(t, test.want, got.SummaryStatus)
			var row types.Knowledge
			require.NoError(t, db.Where("id = ?", "k-1").First(&row).Error)
			assert.Equal(t, types.ParseStatusCancelled, row.ParseStatus)
			assert.Equal(t, test.want, row.SummaryStatus)
			assert.Zero(t, row.PendingSubtasksCount)
		})
	}
}

// Manual knowledge with nothing to index had no later stage to move it out
// of "processing", so it must be failed on the spot.
func TestTriggerManualProcessingFailsEmptyContent(t *testing.T) {
	db := setupKnowledgeSharedAccessDB(t)
	seed := summaryKnowledge()
	seed.ParseStatus = types.ParseStatusProcessing
	seed.Type = types.KnowledgeTypeManual
	seedKnowledge(t, db, seed)
	svc := &knowledgeService{repo: repository.NewKnowledgeRepository(db)}
	before := time.Now().Add(-time.Second)

	err := svc.triggerManualProcessing(context.Background(), summaryKB(), seed, " \n\t ", true)

	require.NoError(t, err)
	var row types.Knowledge
	require.NoError(t, db.Where("id = ?", "k-1").First(&row).Error)
	assert.Equal(t, types.ParseStatusFailed, row.ParseStatus)
	assert.NotEmpty(t, row.ErrorMessage)
	assert.True(t, row.UpdatedAt.After(before))
}

type summaryRetryRepo struct {
	interfaces.KnowledgeRepository
	rows   []*types.Knowledge
	status map[string]string
}

func (r *summaryRetryRepo) ListFailedKnowledgeSummaries(context.Context, uint64, string) ([]*types.Knowledge, error) {
	return r.rows, nil
}

func (r *summaryRetryRepo) TransitionKnowledgeSummaryStatus(
	_ context.Context, k *types.Knowledge, from, to string,
) (bool, error) {
	if r.status[k.ID] != from {
		return false, nil
	}
	r.status[k.ID] = to
	return true, nil
}

type summaryRetryEnqueuer struct {
	t     *testing.T
	calls []string
}

func (q *summaryRetryEnqueuer) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	require.Equal(q.t, types.TypeSummaryGeneration, task.Type())
	var payload types.SummaryGenerationPayload
	require.NoError(q.t, json.Unmarshal(task.Payload(), &payload))
	require.True(q.t, payload.Refresh)
	require.Equal(q.t, uint64(1), payload.TenantID)
	require.Equal(q.t, "kb-1", payload.KnowledgeBaseID)
	q.calls = append(q.calls, payload.KnowledgeID)
	if payload.KnowledgeID == "queue-fails" {
		return nil, errors.New("queue unavailable")
	}
	return &asynq.TaskInfo{}, nil
}

func TestRetryFailedKnowledgeSummariesRestoresFailedEnqueuesAndSkipsExistingJobs(t *testing.T) {
	kb := summaryKB()
	repo := &summaryRetryRepo{status: map[string]string{
		"queue-fails":    types.SummaryStatusFailed,
		"success":        types.SummaryStatusFailed,
		"already-queued": types.SummaryStatusPending,
	}}
	for _, id := range []string{"queue-fails", "success", "already-queued"} {
		repo.rows = append(repo.rows, &types.Knowledge{
			ID: id, TenantID: kb.TenantID, KnowledgeBaseID: kb.ID,
		})
	}
	queue := &summaryRetryEnqueuer{t: t}
	svc := &knowledgeService{repo: repo, task: queue, kbService: &reparseFailureKBService{kb: kb}}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, kb.TenantID)
	submitted, failed, err := svc.RetryFailedKnowledgeSummaries(ctx, kb.ID)
	require.NoError(t, err)
	require.Equal(t, 1, submitted)
	require.Equal(t, 1, failed)
	require.Equal(t, []string{"queue-fails", "success"}, queue.calls)
	require.Equal(t, types.SummaryStatusPending, repo.status["success"])
	require.Equal(t, types.SummaryStatusFailed, repo.status["queue-fails"])
}

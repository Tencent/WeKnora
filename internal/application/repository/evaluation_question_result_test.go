package repository

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newEvaluationQuestionCommandFixture(
	task *types.EvaluationTaskEntity,
	version uint64,
	sampleIndex int,
) interfaces.EvaluationQuestionResultCommand {
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	return interfaces.EvaluationQuestionResultCommand{
		TenantID:        task.TenantID,
		TaskID:          task.ID,
		OwnerID:         task.OwnerID,
		ExpectedVersion: version,
		Total:           3,
		Finished:        sampleIndex + 1,
		Metric:          types.JSON(`{"retrieval_metrics":{"precision":0.5}}`),
		Now:             now,
		LeaseExpiresAt:  now.Add(time.Minute),
		Result: &types.EvaluationQuestionResultInput{
			SampleIndex:     sampleIndex,
			QID:             "q1",
			Question:        "question one?",
			ReferenceAnswer: "answer one",
			GroundTruthPIDs: []int{3},
			SearchResults: []types.EvaluationRankedResult{
				{Rank: 1, PID: -1, Score: 0.9, Provenance: types.EvaluationRankProvenanceUnknown},
				{Rank: 2, PID: 3, Score: 0.8, Provenance: types.EvaluationRankProvenanceKnown},
				{Rank: 3, PID: -1, Score: 0.7, Provenance: types.EvaluationRankProvenanceDuplicate},
			},
			RerankResults: []types.EvaluationRankedResult{
				{Rank: 1, PID: 3, Score: 0.95, Provenance: types.EvaluationRankProvenanceKnown},
			},
			GenerationPIDs: []int{3},
			GeneratedText:  "generated answer",
			PerSampleMetrics: &types.MetricResult{
				RetrievalMetrics: types.RetrievalMetrics{Precision: 0.5, MRR: 0.5},
			},
			Status: types.EvaluationQuestionStatusSuccess,
		},
	}
}

func startEvaluationQuestionTask(
	t *testing.T,
	repo interfaces.EvaluationTaskRepository,
	task *types.EvaluationTaskEntity,
) *types.EvaluationTaskEntity {
	t.Helper()
	now := time.Now().UTC()
	task.LeaseExpiresAt = ptrToTime(now.Add(time.Minute))
	require.NoError(t, repo.CreateTask(context.Background(), task.TenantID, task))
	started, err := repo.TryStartTask(context.Background(), types.EvaluationTaskStartCommand{
		TenantID: task.TenantID, TaskID: task.ID, OwnerID: task.OwnerID,
		ExpectedVersion: task.Version, Now: now, LeaseExpiresAt: now.Add(time.Minute),
	})
	require.NoError(t, err)
	return started
}

func TestEvaluationQuestionResultPublishesAtomically(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	taskRepo := NewEvaluationTaskRepository(db)
	repo := NewEvaluationQuestionResultRepository(db)
	ctx := context.Background()

	task := newEvaluationTaskEntity(31, "question-atomic")
	started := startEvaluationQuestionTask(t, taskRepo, task)

	updated, inserted, err := repo.PublishQuestionResult(ctx,
		newEvaluationQuestionCommandFixture(started, started.Version, 0))
	require.NoError(t, err)
	require.True(t, inserted)
	assert.Equal(t, 1, updated.Finished)
	assert.Equal(t, started.Version+1, updated.Version)

	// The per-question row persisted with its provenance placeholders.
	rows, err := repo.ListQuestionResults(ctx, task.TenantID, task.ID, 0, 10)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "q1", rows[0].QID)
	assert.Len(t, rows[0].ResultHash, 64)
	var searchRanked []types.EvaluationRankedResult
	require.NoError(t, jsonUnmarshal(rows[0].SearchResults, &searchRanked))
	require.Len(t, searchRanked, 3)
	assert.Equal(t, -1, searchRanked[0].PID, "unknown source keeps pid=-1")
	assert.Equal(t, 3, searchRanked[1].PID)
	assert.Equal(t, -1, searchRanked[2].PID, "duplicate PID keeps pid=-1")
}

func TestEvaluationQuestionResultRejectsSampleIndexEqualToTotal(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	taskRepo := NewEvaluationTaskRepository(db)
	repo := NewEvaluationQuestionResultRepository(db)
	ctx := context.Background()

	task := newEvaluationTaskEntity(31, "question-index-boundary")
	started := startEvaluationQuestionTask(t, taskRepo, task)
	command := newEvaluationQuestionCommandFixture(started, started.Version, 0)
	command.Result.SampleIndex = command.Total

	updated, inserted, err := repo.PublishQuestionResult(ctx, command)
	require.EqualError(t, err, "publish evaluation question result: expected 0 <= sample_index < total")
	require.Nil(t, updated)
	require.False(t, inserted)
	rows, listErr := repo.ListQuestionResults(ctx, task.TenantID, task.ID, 0, 10)
	require.NoError(t, listErr)
	require.Empty(t, rows)
	persisted, getErr := taskRepo.GetTask(ctx, task.TenantID, task.ID)
	require.NoError(t, getErr)
	assert.Equal(t, started.Version, persisted.Version)
	assert.Zero(t, persisted.Finished)
}

func TestEvaluationQuestionResultRejectsFinishedJumpAndRollsBack(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	taskRepo := NewEvaluationTaskRepository(db)
	repo := NewEvaluationQuestionResultRepository(db)
	ctx := context.Background()

	task := newEvaluationTaskEntity(31, "question-finished-jump")
	started := startEvaluationQuestionTask(t, taskRepo, task)
	command := newEvaluationQuestionCommandFixture(started, started.Version, 0)
	command.Finished = 2

	updated, inserted, err := repo.PublishQuestionResult(ctx, command)
	require.ErrorIs(t, err, ErrEvaluationTaskStateConflict)
	require.Nil(t, updated)
	require.False(t, inserted)
	rows, listErr := repo.ListQuestionResults(ctx, task.TenantID, task.ID, 0, 10)
	require.NoError(t, listErr)
	require.Empty(t, rows)
	persisted, getErr := taskRepo.GetTask(ctx, task.TenantID, task.ID)
	require.NoError(t, getErr)
	assert.Equal(t, started.Version, persisted.Version)
	assert.Zero(t, persisted.Total)
	assert.Zero(t, persisted.Finished)
	assert.Empty(t, persisted.Metric)
	assert.Empty(t, persisted.RuntimeMetrics)
}

func TestEvaluationQuestionResultIdempotentRetryAndConflict(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	taskRepo := NewEvaluationTaskRepository(db)
	repo := NewEvaluationQuestionResultRepository(db)
	ctx := context.Background()

	task := newEvaluationTaskEntity(32, "question-idempotent")
	started := startEvaluationQuestionTask(t, taskRepo, task)
	command := newEvaluationQuestionCommandFixture(started, started.Version, 0)

	_, inserted, err := repo.PublishQuestionResult(ctx, command)
	require.NoError(t, err)
	require.True(t, inserted)

	// Identical retry: idempotent, no finished increment, no version bump.
	current, err := taskRepo.GetTask(ctx, task.TenantID, task.ID)
	require.NoError(t, err)
	retryCommand := newEvaluationQuestionCommandFixture(current, current.Version, 0)
	_, inserted, err = repo.PublishQuestionResult(ctx, retryCommand)
	require.NoError(t, err)
	assert.False(t, inserted)
	afterRetry, err := taskRepo.GetTask(ctx, task.TenantID, task.ID)
	require.NoError(t, err)
	assert.Equal(t, current.Version, afterRetry.Version)
	assert.Equal(t, 1, afterRetry.Finished)

	// A different result hash for the same sample_index conflicts.
	otherCommand := newEvaluationQuestionCommandFixture(afterRetry, afterRetry.Version, 0)
	otherCommand.Result.GeneratedText = "different generation"
	_, _, err = repo.PublishQuestionResult(ctx, otherCommand)
	require.ErrorIs(t, err, ErrEvaluationQuestionResultConflict)
}

func TestEvaluationQuestionResultRejectsStaleOwnerAndLease(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	taskRepo := NewEvaluationTaskRepository(db)
	repo := NewEvaluationQuestionResultRepository(db)
	ctx := context.Background()

	task := newEvaluationTaskEntity(33, "question-stale")
	started := startEvaluationQuestionTask(t, taskRepo, task)

	// Wrong owner is rejected by the task-state conditions.
	wrongOwner := newEvaluationQuestionCommandFixture(started, started.Version, 0)
	wrongOwner.OwnerID = "another-owner"
	_, _, err := repo.PublishQuestionResult(ctx, wrongOwner)
	require.ErrorIs(t, err, ErrEvaluationTaskStateConflict)

	// Stale version is rejected.
	staleVersion := newEvaluationQuestionCommandFixture(started, started.Version+99, 0)
	_, _, err = repo.PublishQuestionResult(ctx, staleVersion)
	require.ErrorIs(t, err, ErrEvaluationTaskStateConflict)

	// Expired lease is rejected.
	expired := newEvaluationQuestionCommandFixture(started, started.Version, 0)
	expired.Now = time.Now().UTC().Add(2 * time.Hour)
	expired.LeaseExpiresAt = expired.Now.Add(time.Minute)
	_, _, err = repo.PublishQuestionResult(ctx, expired)
	require.ErrorIs(t, err, ErrEvaluationTaskStateConflict)

	// No per-question orphan remains after rejected publications.
	rows, err := repo.ListQuestionResults(ctx, task.TenantID, task.ID, 0, 10)
	require.NoError(t, err)
	assert.Empty(t, rows)
}

func TestEvaluationQuestionResultRejectsCanceledTask(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	taskRepo := NewEvaluationTaskRepository(db)
	repo := NewEvaluationQuestionResultRepository(db)
	ctx := context.Background()

	task := newEvaluationTaskEntity(34, "question-canceled")
	started := startEvaluationQuestionTask(t, taskRepo, task)
	canceled, err := taskRepo.RequestCancel(ctx, types.EvaluationTaskCancelCommand{
		TenantID: task.TenantID,
		TaskID:   task.ID,
		Now:      time.Now().UTC(),
	})
	require.NoError(t, err)
	require.NotNil(t, canceled.CancelRequestedAt)
	require.Equal(t, started.Version, canceled.Version)

	_, _, err = repo.PublishQuestionResult(ctx,
		newEvaluationQuestionCommandFixture(started, started.Version, 0))
	require.ErrorIs(t, err, ErrEvaluationQuestionResultCanceled)
	rows, listErr := repo.ListQuestionResults(ctx, task.TenantID, task.ID, 0, 10)
	require.NoError(t, listErr)
	require.Empty(t, rows)
}

func TestEvaluationQuestionResultKeysetPaginationAndTenantIsolation(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	taskRepo := NewEvaluationTaskRepository(db)
	repo := NewEvaluationQuestionResultRepository(db)
	ctx := context.Background()

	task := newEvaluationTaskEntity(35, "question-paging")
	started := startEvaluationQuestionTask(t, taskRepo, task)
	version := started.Version
	for sample := 0; sample < 3; sample++ {
		command := newEvaluationQuestionCommandFixture(started, version, sample)
		updated, inserted, err := repo.PublishQuestionResult(ctx, command)
		require.NoError(t, err)
		require.True(t, inserted)
		version = updated.Version
	}

	page1, err := repo.ListQuestionResults(ctx, task.TenantID, task.ID, 0, 2)
	require.NoError(t, err)
	require.Len(t, page1, 2)
	assert.Equal(t, 0, page1[0].SampleIndex)
	assert.Equal(t, 1, page1[1].SampleIndex)

	page2, err := repo.ListQuestionResults(ctx, task.TenantID, task.ID, 2, 2)
	require.NoError(t, err)
	require.Len(t, page2, 1)
	assert.Equal(t, 2, page2[0].SampleIndex)

	// Cross-tenant reads return nothing, matching the NotFound convention.
	crossTenant, err := repo.ListQuestionResults(ctx, 99, task.ID, 0, 10)
	require.NoError(t, err)
	assert.Empty(t, crossTenant)

	// Cursor helpers round-trip and reject foreign cursors.
	cursor, err := types.EncodeEvaluationQuestionCursor(task.ID, 2)
	require.NoError(t, err)
	from, err := types.DecodeEvaluationQuestionCursor(task.ID, cursor)
	require.NoError(t, err)
	assert.Equal(t, 2, from)
	_, err = types.DecodeEvaluationQuestionCursor("other-task", cursor)
	require.Error(t, err)
}

func TestEvaluationQuestionResultsApplyFrozenSourceAllowList(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	taskRepo := NewEvaluationTaskRepository(db)
	repo := NewEvaluationQuestionResultRepository(db)
	ctx := context.Background()

	task := newEvaluationTaskEntity(36, "question-api-key-scope")
	source := "kb-allowed"
	experiment, err := json.Marshal(types.EvaluationExperimentSnapshot{SourceKnowledgeBaseID: &source})
	require.NoError(t, err)
	datasetVersionID := "version-1"
	contentHash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	experimentHash := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	task.ExperimentSnapshot = experiment
	task.DatasetVersionID = &datasetVersionID
	task.DatasetContentSHA256 = &contentHash
	task.ExperimentSHA256 = &experimentHash
	started := startEvaluationQuestionTask(t, taskRepo, task)
	_, inserted, err := repo.PublishQuestionResult(ctx,
		newEvaluationQuestionCommandFixture(started, started.Version, 0))
	require.NoError(t, err)
	require.True(t, inserted)

	denied := types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{
		KnowledgeBaseIDs: types.StringArray{"kb-denied"},
	})
	_, err = repo.ListQuestionResults(denied, task.TenantID, task.ID, 0, 10)
	require.ErrorIs(t, err, interfaces.ErrEvaluationTaskNotFound)

	allowed := types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{
		KnowledgeBaseIDs: types.StringArray{"kb-allowed"},
	})
	rows, err := repo.ListQuestionResults(allowed, task.TenantID, task.ID, 0, 10)
	require.NoError(t, err)
	require.Len(t, rows, 1)
}

func TestEvaluationQuestionResultConcurrentOrderDoesNotChangeOutcome(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	taskRepo := NewEvaluationTaskRepository(db)
	repo := NewEvaluationQuestionResultRepository(db)
	ctx := context.Background()

	task := newEvaluationTaskEntity(36, "question-concurrent")
	started := startEvaluationQuestionTask(t, taskRepo, task)

	// Samples complete in reversed order with staggered versions; every
	// publication re-reads the current version first, mirroring the worker's
	// publishMu serialization.
	samples := []int{2, 1, 0}
	var waiters sync.WaitGroup
	errs := make(chan error, len(samples))
	versionMu := sync.Mutex{}
	version := started.Version
	finished := 0
	for _, sample := range samples {
		sample := sample
		waiters.Add(1)
		go func() {
			defer waiters.Done()
			versionMu.Lock()
			command := newEvaluationQuestionCommandFixture(started, version, sample)
			command.Finished = finished + 1
			updated, _, err := repo.PublishQuestionResult(ctx, command)
			if err == nil {
				version = updated.Version
				finished = command.Finished
			}
			versionMu.Unlock()
			errs <- err
		}()
	}
	waiters.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	rows, err := repo.ListQuestionResults(ctx, task.TenantID, task.ID, 0, 10)
	require.NoError(t, err)
	require.Len(t, rows, 3)
	for i, row := range rows {
		assert.Equal(t, i, row.SampleIndex, "canonical order follows sample_index, not completion order")
	}
	final, err := taskRepo.GetTask(ctx, task.TenantID, task.ID)
	require.NoError(t, err)
	assert.Equal(t, 3, final.Finished)
}

func TestEvaluationQuestionResultTerminalKeepsPartialRows(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	taskRepo := NewEvaluationTaskRepository(db)
	repo := NewEvaluationQuestionResultRepository(db)
	ctx := context.Background()

	task := newEvaluationTaskEntity(37, "question-partial")
	started := startEvaluationQuestionTask(t, taskRepo, task)
	updated, inserted, err := repo.PublishQuestionResult(ctx,
		newEvaluationQuestionCommandFixture(started, started.Version, 0))
	require.NoError(t, err)
	require.True(t, inserted)

	// The task fails after one of three samples: the published row survives,
	// and no zero-value rows are fabricated for unfinished samples.
	_, err = taskRepo.PublishTerminal(ctx, types.EvaluationTaskTerminalCommand{
		TenantID: task.TenantID, TaskID: task.ID, OwnerID: task.OwnerID,
		ExpectedVersion: updated.Version, Status: types.EvaluationStatueFailed,
		EndTime: time.Now().UTC(), ErrMsg: "worker failed on sample 1",
	})
	require.NoError(t, err)

	rows, err := repo.ListQuestionResults(ctx, task.TenantID, task.ID, 0, 10)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, 0, rows[0].SampleIndex)
	assert.Equal(t, types.EvaluationQuestionStatusSuccess, rows[0].Status)
}

func jsonUnmarshal(data types.JSON, target any) error {
	return json.Unmarshal(data, target)
}

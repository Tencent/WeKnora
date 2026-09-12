package service

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

type runtimeQuestionRepository struct {
	interfaces.EvaluationQuestionResultRepository
	command interfaces.EvaluationQuestionResultCommand
}

func (r *runtimeQuestionRepository) PublishQuestionResult(
	_ context.Context,
	command interfaces.EvaluationQuestionResultCommand,
) (*types.EvaluationTaskEntity, bool, error) {
	r.command = command
	return &types.EvaluationTaskEntity{
		Version:        command.ExpectedVersion + 1,
		Metric:         command.Metric,
		RuntimeMetrics: command.RuntimeMetrics,
	}, true, nil
}

func TestEvaluationRuntimeCollectorBuildsTaskSnapshot(t *testing.T) {
	startedAt := time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)
	collector := newEvaluationRuntimeCollector(startedAt)
	collector.addDatasetLoad(12 * time.Millisecond)
	collector.addIndexing(25 * time.Millisecond)
	collector.addExecution(40 * time.Millisecond)
	collector.addPersistence(3 * time.Millisecond)
	collector.addCleanup(5 * time.Millisecond)
	collector.sampleStarted()
	collector.sampleSucceeded(11, 4, 15, true)
	collector.sampleStarted()
	collector.sampleFailed(false)
	endedAt := startedAt.Add(100 * time.Millisecond)

	snapshot := collector.snapshot(endedAt)
	require.NotNil(t, snapshot)
	assert.Equal(t, 1, snapshot.SchemaVersion)
	assert.Equal(t, startedAt, snapshot.StartedAt)
	require.NotNil(t, snapshot.EndedAt)
	assert.Equal(t, endedAt, *snapshot.EndedAt)
	assert.Equal(t, int64(12), *snapshot.Durations.DatasetLoadMs)
	assert.Equal(t, int64(25), *snapshot.Durations.IndexingMs)
	assert.Equal(t, int64(40), *snapshot.Durations.ExecutionMs)
	assert.Equal(t, int64(3), *snapshot.Durations.PersistenceMs)
	assert.Equal(t, int64(5), *snapshot.Durations.CleanupMs)
	assert.Equal(t, int64(100), *snapshot.Durations.TotalMs)
	assert.Equal(t, 2, snapshot.Samples.Started)
	assert.Equal(t, 1, snapshot.Samples.Success)
	assert.Equal(t, 1, snapshot.Samples.Failed)
	assert.Equal(t, 1, snapshot.Failure.Numerator)
	assert.Equal(t, 2, snapshot.Failure.Denominator)
	assert.Equal(t, 11, snapshot.Tokens.PromptTokens)
	assert.Equal(t, 4, snapshot.Tokens.CompletionTokens)
	assert.Equal(t, 15, snapshot.Tokens.TotalTokens)
	assert.Equal(t, 1, snapshot.Tokens.ReportedSamples)
	assert.Equal(t, 0, snapshot.Tokens.UnreportedSamples)
}

func TestEvaluationRuntimeCollectorCountsCanceledAndUnreportedSamples(t *testing.T) {
	collector := newEvaluationRuntimeCollector(time.Now().UTC())
	collector.sampleStarted()
	collector.sampleFailed(true)
	collector.sampleStarted()
	collector.sampleSucceeded(0, 0, 0, false)

	snapshot := collector.snapshot(time.Now().UTC())
	assert.Equal(t, 1, snapshot.Samples.Canceled)
	assert.Equal(t, 1, snapshot.Samples.Success)
	assert.Equal(t, 1, snapshot.Tokens.UnreportedSamples)
	assert.Equal(t, 1, snapshot.Failure.Numerator)
	assert.Equal(t, 2, snapshot.Failure.Denominator)
}

func TestFinalizeRecoveredEvaluationRuntimeClassifiesUnresolvedSamples(t *testing.T) {
	startedAt := time.Date(2026, 8, 31, 9, 0, 0, 0, time.UTC)
	raw, err := json.Marshal(types.EvaluationRuntimeMetrics{
		SchemaVersion: 1,
		StartedAt:     startedAt,
		Samples:       types.EvaluationRuntimeSamples{Total: 5, Started: 3, Success: 1},
	})
	require.NoError(t, err)

	encoded, err := finalizeRecoveredEvaluationRuntime(raw, startedAt, startedAt.Add(time.Second), false)
	require.NoError(t, err)
	var snapshot types.EvaluationRuntimeMetrics
	require.NoError(t, json.Unmarshal(encoded, &snapshot))
	assert.Equal(t, 2, snapshot.Samples.Interrupted)
	assert.Equal(t, 2, snapshot.Samples.NotStarted)
	assert.Equal(t, 2, snapshot.Failure.Numerator)
	assert.Equal(t, 3, snapshot.Failure.Denominator)
	assert.Equal(t, int64(1000), *snapshot.Durations.TotalMs)
}

func TestPublishFailedEvaluationQuestionPersistsStatusAndRuntimeFacts(t *testing.T) {
	plan, err := types.DefaultEvaluationMetricPlan()
	require.NoError(t, err)
	repository := &runtimeQuestionRepository{}
	service := &EvaluationService{questionResultRepository: repository}
	hook := NewHookMetric(1, "knowledge-1")
	hook.recordInit(0)
	hook.recordQaPair(0, &types.QAPair{QID: 7, Question: "failed?", Answer: "answer"})
	collector := newEvaluationRuntimeCollector(time.Now().UTC())
	collector.setTotal(1)
	collector.sampleStarted()
	collector.sampleFailed(false)
	timings := &types.EvaluationPipelineTimings{}
	timings.AddStage(types.CHUNK_SEARCH, 3*time.Millisecond)
	chatManage := &types.ChatManage{PipelineContext: types.PipelineContext{EvaluationTimings: timings}}
	detail := &types.EvaluationDetail{
		Params:     &types.ChatManage{},
		Experiment: &types.EvaluationExperimentSnapshot{MetricPlan: plan},
	}
	runState := &evaluationRunState{tenantID: 1, taskID: "task-1", ownerID: "owner-1", version: 2}
	finished := 0

	err = service.publishFailedEvaluationQuestion(
		context.Background(), detail, runState, hook, collector, chatManage,
		&sync.Mutex{}, &finished, 1, 0, 9*time.Millisecond, false,
	)
	require.NoError(t, err)
	assert.Equal(t, 1, finished)
	require.NotNil(t, repository.command.Result)
	assert.Equal(t, types.EvaluationQuestionStatusFailed, repository.command.Result.Status)
	assert.Equal(t, "evaluation_question_failed", repository.command.Result.ErrorCode)
	assert.Equal(t, int64(3), *repository.command.Result.RetrievalMs)
	assert.Nil(t, repository.command.Result.RerankMs)
	assert.Equal(t, int64(9), *repository.command.Result.TotalMs)
	assert.False(t, repository.command.Result.UsageReported)
	require.NotEmpty(t, repository.command.RuntimeMetrics)
}

package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const evaluationComparisonContentHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func comparisonTaskFixture(t *testing.T, taskID string, topK int, precision float64) *types.EvaluationTaskEntity {
	t.Helper()
	plan, err := types.DefaultEvaluationMetricPlan()
	require.NoError(t, err)
	sourceID := "kb-source"
	experiment := &types.EvaluationExperimentSnapshot{
		SchemaVersion: types.EvaluationExperimentSchemaVersion,
		Dataset: types.EvaluationDatasetSnapshot{
			DatasetID: "dataset-a", DatasetVersionID: "version-a", VersionNumber: 3,
			ContentSHA256: evaluationComparisonContentHash,
		},
		SourceKnowledgeBaseID: &sourceID,
		Models: types.EvaluationModelSetSnapshot{
			Chat: &types.EvaluationModelSnapshot{ID: "chat-a"},
		},
		Configuration: types.EvaluationConfigurationSnapshot{
			Retrieval: types.EvaluationRetrievalSnapshot{EmbeddingTopK: topK},
			Generation: types.EvaluationGenerationSnapshot{
				Seed: intPointer(42), SeedProvided: true, SeedSupport: types.EvaluationSeedSupportApplied,
			},
		},
		MetricPlan: plan,
	}
	snapshot, err := experiment.CanonicalJSON()
	require.NoError(t, err)
	versionID := "version-a"
	experimentHash := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	return &types.EvaluationTaskEntity{
		ID: taskID, TenantID: 7, DatasetID: "dataset-a", Status: types.EvaluationStatueSuccess,
		StartTime:        time.Date(2026, 8, 31, 11, 0, 0, 0, time.UTC),
		DatasetVersionID: &versionID, DatasetContentSHA256: stringPointer(evaluationComparisonContentHash),
		ExperimentSnapshot: snapshot, ExperimentSHA256: &experimentHash,
		Metric: types.JSON(`{"retrieval_metrics":{"precision":` + jsonNumber(precision) + `}}`),
	}
}

func intPointer(value int) *int { return &value }

func stringPointer(value string) *string { return &value }

func jsonNumber(value float64) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func comparisonServiceContext() context.Context {
	return context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
}

func comparisonQuestionFixture(
	t *testing.T,
	taskID string,
	index int,
	status string,
	precision float64,
) *types.EvaluationQuestionResultEntity {
	t.Helper()
	row := evaluationExportQuestionFixture(t, index, "question")
	row.TaskID = taskID
	row.Status = status
	plan, err := types.DefaultEvaluationMetricPlan()
	require.NoError(t, err)
	metric := types.MetricResult{Scores: make(map[string]types.EvaluationMetricScore)}
	metric.RetrievalMetrics.Precision = precision
	for _, spec := range plan.Metrics {
		if spec.Key != "retrieval.precision" {
			continue
		}
		value := precision
		metric.Scores[spec.InstanceID] = types.EvaluationMetricScore{
			Value: &value, Status: types.EvaluationMetricObservationValid,
		}
	}
	encoded, err := json.Marshal(&metric)
	require.NoError(t, err)
	row.PerSampleMetrics = types.JSON(encoded)
	return row
}

func setComparisonTaskResultCount(
	task *types.EvaluationTaskEntity,
	rows []*types.EvaluationQuestionResultEntity,
) {
	task.Total = len(rows)
	task.Finished = len(rows)
}

func TestCompareEvaluationsBuildsStableParameterAndMetricDeltas(t *testing.T) {
	repo := newFakeEvaluationTaskRepository()
	repo.register(comparisonTaskFixture(t, "task-a", 5, 0.5))
	repo.register(comparisonTaskFixture(t, "task-b", 10, 0.6))
	svc := &EvaluationService{evaluationTaskRepository: repo}

	response, err := svc.CompareEvaluations(comparisonServiceContext(), types.EvaluationComparisonRequest{
		TaskIDs: []string{"task-a", "task-b"},
	})
	require.NoError(t, err)
	require.Equal(t, "task-a", response.BaselineTaskID)
	require.True(t, response.Runs[0].IsBaseline)
	require.Equal(t, 1, repo.countCalls("GetTasksByIDs"))

	topK := comparisonParameterByPointer(t, response, "/configuration/retrieval/embedding_top_k")
	require.True(t, topK.Differ)
	require.JSONEq(t, `5`, string(topK.Values[0].Value))
	require.JSONEq(t, `10`, string(topK.Values[1].Value))

	precision := comparisonMetricByPointer(t, response, "/retrieval_metrics/precision")
	require.True(t, precision.Compatible)
	require.Equal(t, "retrieval.precision", precision.Key)
	require.Nil(t, precision.Values[0].Delta)
	require.NotNil(t, precision.Values[1].Delta)
	require.InDelta(t, 0.1, *precision.Values[1].Delta, 1e-12)
	require.NotNil(t, precision.Values[1].RelativeDelta)
	require.InDelta(t, 0.2, *precision.Values[1].RelativeDelta, 1e-12)
}

func TestCompareEvaluationsIncludesDeterministicMetricAndSuccessIntervals(t *testing.T) {
	total20, total40 := int64(20), int64(40)
	tokens10, tokens30 := 10, 30
	prompt4, prompt12 := 4, 12
	completion6, completion18 := 6, 18
	taskRepo := newFakeEvaluationTaskRepository()
	rows := []*types.EvaluationQuestionResultEntity{
		comparisonQuestionFixture(t, "task-a", 0, types.EvaluationQuestionStatusSuccess, 0.25),
		comparisonQuestionFixture(t, "task-a", 1, types.EvaluationQuestionStatusSuccess, 0.75),
		comparisonQuestionFixture(t, "task-a", 2, types.EvaluationQuestionStatusFailed, 0),
		comparisonQuestionFixture(t, "task-b", 0, types.EvaluationQuestionStatusSuccess, 0.6),
	}
	rows[0].TotalMs, rows[0].TotalTokens = &total20, &tokens10
	rows[0].PromptTokens, rows[0].CompletionTokens, rows[0].UsageReported = &prompt4, &completion6, true
	rows[1].TotalMs, rows[1].TotalTokens = &total40, &tokens30
	rows[1].PromptTokens, rows[1].CompletionTokens, rows[1].UsageReported = &prompt12, &completion18, true
	taskA := comparisonTaskFixture(t, "task-a", 5, 0.5)
	taskB := comparisonTaskFixture(t, "task-b", 5, 0.6)
	setComparisonTaskResultCount(taskA, rows[:3])
	setComparisonTaskResultCount(taskB, rows[3:])
	taskRepo.register(taskA)
	taskRepo.register(taskB)
	questionRepo := &fakeEvaluationExportQuestionRepository{rows: rows}
	svc := &EvaluationService{evaluationTaskRepository: taskRepo, questionResultRepository: questionRepo}

	request := types.EvaluationComparisonRequest{TaskIDs: []string{"task-a", "task-b"}}
	first, err := svc.CompareEvaluations(comparisonServiceContext(), request)
	require.NoError(t, err)
	second, err := svc.CompareEvaluations(comparisonServiceContext(), request)
	require.NoError(t, err)

	require.NotNil(t, first.Runs[0].QuestionSuccessRate)
	require.Equal(t, "wilson_score", first.Runs[0].QuestionSuccessRate.Method)
	require.Equal(t, 3, first.Runs[0].QuestionSuccessRate.Samples)
	require.Equal(t, types.EvaluationStatisticsValid, first.Runs[0].QuestionSuccessStatus)
	require.Equal(t, 3, first.Runs[0].QuestionNTotal)
	require.InDelta(t, 2.0/3.0, first.Runs[0].QuestionSuccessRate.Estimate, 1e-12)
	require.NotNil(t, first.Runs[0].TotalLatency)
	require.InDelta(t, 30, first.Runs[0].TotalLatency.P50, 1e-12)
	require.EqualValues(t, 40, first.Runs[0].TokenTotals.Total)
	require.Nil(t, first.Runs[1].QuestionSuccessRate)
	require.Equal(t, types.EvaluationStatisticsInsufficientSample, first.Runs[1].QuestionSuccessStatus)
	metric := comparisonMetricByPointer(t, first, "/retrieval_metrics/precision")
	require.NotNil(t, metric.Values[0].Confidence)
	require.Equal(t, "percentile_bootstrap", metric.Values[0].Confidence.Method)
	require.Equal(t, 2, metric.Values[0].Confidence.Samples)
	require.Equal(t, 3, metric.Values[0].NTotal)
	require.Equal(t, 2, metric.Values[0].NValid)
	require.Equal(t, 1, metric.Values[0].NMissing)
	require.Nil(t, metric.Values[1].Confidence)
	require.Equal(t, types.EvaluationStatisticsInsufficientSample, metric.Values[1].ConfidenceStatus)
	require.Equal(t, metric.Values[0].Confidence, comparisonMetricByPointer(
		t, second, "/retrieval_metrics/precision",
	).Values[0].Confidence)
}

func TestCompareEvaluationsKeepsRelativeDeltaNullForZeroBaseline(t *testing.T) {
	repo := newFakeEvaluationTaskRepository()
	repo.register(comparisonTaskFixture(t, "task-a", 5, 0))
	repo.register(comparisonTaskFixture(t, "task-b", 5, 0.25))
	svc := &EvaluationService{evaluationTaskRepository: repo}

	response, err := svc.CompareEvaluations(comparisonServiceContext(), types.EvaluationComparisonRequest{
		TaskIDs: []string{"task-a", "task-b"},
	})
	require.NoError(t, err)
	precision := comparisonMetricByPointer(t, response, "/retrieval_metrics/precision")
	require.NotNil(t, precision.Values[0].Value)
	require.Zero(t, *precision.Values[0].Value)
	require.NotNil(t, precision.Values[1].Delta)
	require.Nil(t, precision.Values[1].RelativeDelta)
	require.Equal(t, types.EvaluationComparisonReasonBaselineZero, precision.Values[1].RelativeReason)
}

func TestEvaluationAvailableNumericMetricsKeepsZeroAndExcludesMissing(t *testing.T) {
	plan, err := types.DefaultEvaluationMetricPlan()
	require.NoError(t, err)
	zero := 0.0
	result := types.MetricResult{Scores: make(map[string]types.EvaluationMetricScore, len(plan.Metrics))}
	result.RetrievalMetrics.Precision = 1
	result.RetrievalMetrics.Recall = 0.875
	var precisionSpec, recallSpec, mapSpec types.EvaluationMetricSpecSnapshot
	for _, spec := range plan.Metrics {
		switch spec.Key {
		case "retrieval.precision":
			precisionSpec = spec
			result.Scores[spec.InstanceID] = types.EvaluationMetricScore{
				Value: &zero, Status: types.EvaluationMetricObservationValid,
			}
		case "retrieval.map":
			mapSpec = spec
			result.Scores[spec.InstanceID] = types.EvaluationMetricScore{
				Status:    types.EvaluationMetricObservationMissing,
				ErrorCode: "relevance_labels_missing",
			}
		case "retrieval.recall":
			recallSpec = spec
		}
	}
	raw, err := json.Marshal(result)
	require.NoError(t, err)
	metrics, err := evaluationAvailableNumericMetrics(types.JSON(raw), plan)
	require.NoError(t, err)

	require.Contains(t, metrics, "/retrieval_metrics/precision")
	require.Zero(t, metrics["/retrieval_metrics/precision"])
	require.Contains(t, metrics, types.EvaluationMetricScoreValuePointer(precisionSpec.InstanceID))
	require.NotContains(t, metrics, "/retrieval_metrics/map")
	require.NotContains(t, metrics, types.EvaluationMetricScoreValuePointer(mapSpec.InstanceID))
	require.NotContains(t, metrics, "/retrieval_metrics/recall")
	require.NotContains(t, metrics, types.EvaluationMetricScoreValuePointer(recallSpec.InstanceID))
}

func TestEvaluationAvailableNumericMetricsSupportsEntireLegacyFixedResult(t *testing.T) {
	plan, err := types.DefaultEvaluationMetricPlan()
	require.NoError(t, err)
	raw, err := json.Marshal(types.MetricResult{
		RetrievalMetrics: types.RetrievalMetrics{Precision: 0.625},
	})
	require.NoError(t, err)

	metrics, err := evaluationAvailableNumericMetrics(types.JSON(raw), plan)
	require.NoError(t, err)
	require.Contains(t, metrics, "/retrieval_metrics/precision")
	require.Equal(t, 0.625, metrics["/retrieval_metrics/precision"])
}

func TestEvaluationAvailableNumericMetricsRejectsInvalidJSON(t *testing.T) {
	plan, err := types.DefaultEvaluationMetricPlan()
	require.NoError(t, err)

	_, err = evaluationAvailableNumericMetrics(types.JSON(`{`), plan)
	require.ErrorIs(t, err, types.ErrEvaluationComparisonDataInvalid)
}

func TestEvaluationAvailableNumericMetricsRejectsUnknownStatus(t *testing.T) {
	plan, err := types.DefaultEvaluationMetricPlan()
	require.NoError(t, err)
	spec := plan.Metrics[0]
	raw, err := json.Marshal(types.MetricResult{Scores: map[string]types.EvaluationMetricScore{
		spec.InstanceID: {Status: "unknown"},
	}})
	require.NoError(t, err)

	_, err = evaluationAvailableNumericMetrics(types.JSON(raw), plan)
	require.ErrorIs(t, err, types.ErrEvaluationComparisonDataInvalid)
}

func TestEvaluationStatisticsSeedIsStableAndSeparatesInputs(t *testing.T) {
	seed := evaluationStatisticsSeed("task-a", "/metrics/precision")
	require.Equal(t, seed, evaluationStatisticsSeed("task-a", "/metrics/precision"))
	require.NotEqual(t, evaluationStatisticsSeed("ab", "c"), evaluationStatisticsSeed("a", "bc"))
	require.GreaterOrEqual(t, seed, int64(0))
}

func TestCompareEvaluationsMarksFrozenMetricIdentityMismatch(t *testing.T) {
	repo := newFakeEvaluationTaskRepository()
	baseline := comparisonTaskFixture(t, "task-a", 5, 0.5)
	other := comparisonTaskFixture(t, "task-b", 5, 0.6)
	var experiment types.EvaluationExperimentSnapshot
	require.NoError(t, json.Unmarshal(other.ExperimentSnapshot, &experiment))
	precisionInstanceID := ""
	for index := range experiment.MetricPlan.Metrics {
		if experiment.MetricPlan.Metrics[index].Key == "retrieval.precision" {
			experiment.MetricPlan.Metrics[index].Version = "2.0.0"
			experiment.MetricPlan.Metrics[index].InstanceID = types.EvaluationMetricInstanceID(
				experiment.MetricPlan.Metrics[index].Key,
				experiment.MetricPlan.Metrics[index].Version,
				experiment.MetricPlan.Metrics[index].ConfigSHA256,
			)
			precisionInstanceID = experiment.MetricPlan.Metrics[index].InstanceID
		}
	}
	encoded, err := experiment.CanonicalJSON()
	require.NoError(t, err)
	other.ExperimentSnapshot = encoded
	otherPrecision := 0.6
	metric := types.MetricResult{Scores: map[string]types.EvaluationMetricScore{
		precisionInstanceID: {Value: &otherPrecision, Status: types.EvaluationMetricObservationValid},
	}}
	metric.RetrievalMetrics.Precision = otherPrecision
	metricJSON, err := json.Marshal(metric)
	require.NoError(t, err)
	other.Metric = types.JSON(metricJSON)
	repo.register(baseline)
	repo.register(other)
	svc := &EvaluationService{evaluationTaskRepository: repo}

	response, err := svc.CompareEvaluations(comparisonServiceContext(), types.EvaluationComparisonRequest{
		TaskIDs: []string{"task-a", "task-b"},
	})
	require.NoError(t, err)
	precision := comparisonMetricByPointer(t, response, "/retrieval_metrics/precision")
	require.False(t, precision.Compatible)
	require.Equal(t, types.EvaluationComparisonValueIncompatible, precision.Values[0].Status)
	require.Equal(t, types.EvaluationComparisonReasonIdentityDiffers, precision.Values[1].Reason)
	require.Nil(t, precision.Values[1].Delta)
}

func TestCompareEvaluationsRejectsMissingCrossTenantAndIncompatibleRuns(t *testing.T) {
	repo := newFakeEvaluationTaskRepository()
	repo.register(comparisonTaskFixture(t, "task-a", 5, 0.5))
	hidden := comparisonTaskFixture(t, "task-hidden", 5, 0.6)
	hidden.TenantID = 8
	repo.register(hidden)
	svc := &EvaluationService{evaluationTaskRepository: repo}

	_, err := svc.CompareEvaluations(comparisonServiceContext(), types.EvaluationComparisonRequest{
		TaskIDs: []string{"task-a", "task-hidden"},
	})
	require.ErrorIs(t, err, types.ErrEvaluationComparisonTaskNotFound)

	failed := comparisonTaskFixture(t, "task-failed", 5, 0.6)
	failed.Status = types.EvaluationStatueFailed
	repo.register(failed)
	_, err = svc.CompareEvaluations(comparisonServiceContext(), types.EvaluationComparisonRequest{
		TaskIDs: []string{"task-a", "task-failed"},
	})
	require.ErrorIs(t, err, types.ErrEvaluationComparisonConflict)

	incomplete := comparisonTaskFixture(t, "task-incomplete", 5, 0.6)
	incomplete.ExperimentSHA256 = nil
	repo.register(incomplete)
	_, err = svc.CompareEvaluations(comparisonServiceContext(), types.EvaluationComparisonRequest{
		TaskIDs: []string{"task-a", "task-incomplete"},
	})
	require.ErrorIs(t, err, types.ErrEvaluationComparisonConflict)

	mismatch := comparisonTaskFixture(t, "task-mismatch", 5, 0.6)
	mismatch.DatasetContentSHA256 = stringPointer("cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc")
	repo.register(mismatch)
	_, err = svc.CompareEvaluations(comparisonServiceContext(), types.EvaluationComparisonRequest{
		TaskIDs: []string{"task-a", "task-mismatch"},
	})
	require.ErrorIs(t, err, types.ErrEvaluationComparisonConflict)
}

func TestCompareEvaluationsEnforcesAPIKeySourceKnowledgeBaseScope(t *testing.T) {
	repo := newFakeEvaluationTaskRepository()
	repo.register(comparisonTaskFixture(t, "task-a", 5, 0.5))
	repo.register(comparisonTaskFixture(t, "task-b", 5, 0.6))
	svc := &EvaluationService{evaluationTaskRepository: repo}
	ctx := types.WithTenantAPIKeyScope(comparisonServiceContext(), types.TenantAPIKeyScope{
		KnowledgeBaseIDs: []string{"kb-other"},
	})
	_, err := svc.CompareEvaluations(ctx, types.EvaluationComparisonRequest{
		TaskIDs: []string{"task-a", "task-b"},
	})
	require.ErrorIs(t, err, types.ErrEvaluationComparisonTaskNotFound)
}

func comparisonParameterByPointer(
	t *testing.T,
	response *types.EvaluationComparisonResponse,
	pointer string,
) types.EvaluationComparisonParameter {
	t.Helper()
	for _, parameter := range response.Parameters {
		if parameter.Pointer == pointer {
			return parameter
		}
	}
	t.Fatalf("comparison parameter %s was not found", pointer)
	return types.EvaluationComparisonParameter{}
}

func comparisonMetricByPointer(
	t *testing.T,
	response *types.EvaluationComparisonResponse,
	pointer string,
) types.EvaluationComparisonMetric {
	t.Helper()
	for _, metric := range response.Metrics {
		if metric.Pointer == pointer {
			return metric
		}
	}
	t.Fatalf("comparison metric %s was not found", pointer)
	return types.EvaluationComparisonMetric{}
}

func TestNormalizeComparisonDeduplicatesBeforeLimitResult(t *testing.T) {
	ids, err := types.NormalizeEvaluationComparisonTaskIDs([]string{"task-a", "task-b", "task-a"})
	require.NoError(t, err)
	assert.Equal(t, []string{"task-a", "task-b"}, ids)
}

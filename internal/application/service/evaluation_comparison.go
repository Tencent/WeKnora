package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"sort"
	"strings"

	evaluationstats "github.com/Tencent/WeKnora/internal/evaluation/statistics"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type evaluationComparisonInput struct {
	entity          *types.EvaluationTaskEntity
	experiment      *types.EvaluationExperimentSnapshot
	parameters      map[string]json.RawMessage
	metrics         map[string]float64
	metricSamples   map[string][]float64
	questionSuccess *types.EvaluationConfidenceInterval
	questionStatus  string
	questionTotal   int
	totalLatencies  []float64
	tokenTotals     types.EvaluationTokenTotals
}

// CompareEvaluations aligns stable parameters and compatible numeric metrics across frozen runs.
func (e *EvaluationService) CompareEvaluations(
	ctx context.Context,
	request types.EvaluationComparisonRequest,
) (*types.EvaluationComparisonResponse, error) {
	ids, err := types.NormalizeEvaluationComparisonTaskIDs(request.TaskIDs)
	if err != nil {
		return nil, err
	}
	baselineID := strings.TrimSpace(request.BaselineTaskID)
	if baselineID == "" {
		baselineID = ids[0]
	}
	if !containsEvaluationTaskID(ids, baselineID) {
		return nil, fmt.Errorf(
			"%w: baseline_task_id must be included in task_ids",
			types.ErrEvaluationComparisonInvalid,
		)
	}
	tenantID := types.MustTenantIDFromContext(ctx)
	tasksByID, err := e.evaluationTaskRepository.GetTasksByIDs(ctx, tenantID, ids)
	if err != nil {
		return nil, err
	}

	inputs := make([]evaluationComparisonInput, 0, len(ids))
	for _, taskID := range ids {
		entity, exists := tasksByID[taskID]
		if !exists {
			return nil, fmt.Errorf("%w: %s", types.ErrEvaluationComparisonTaskNotFound, taskID)
		}
		if err := AuthorizeEvaluationTaskForAPIKey(ctx, entity); err != nil {
			if errors.Is(err, interfaces.ErrEvaluationTaskNotFound) {
				return nil, fmt.Errorf("%w: %s", types.ErrEvaluationComparisonTaskNotFound, taskID)
			}
			return nil, err
		}
		experiment, complete, err := decodeEvaluationExperiment(entity)
		if err != nil {
			return nil, fmt.Errorf("%w: task %s experiment: %v", types.ErrEvaluationComparisonDataInvalid, taskID, err)
		}
		if entity.Status != types.EvaluationStatueSuccess {
			return nil, fmt.Errorf(
				"%w: task %s status is %d",
				types.ErrEvaluationComparisonConflict,
				taskID,
				entity.Status,
			)
		}
		if !complete || experiment == nil {
			return nil, fmt.Errorf(
				"%w: task %s provenance is incomplete",
				types.ErrEvaluationComparisonConflict,
				taskID,
			)
		}
		parameters, err := types.FlattenEvaluationComparisonParameters(entity.ExperimentSnapshot)
		if err != nil {
			return nil, fmt.Errorf("task %s: %w", taskID, err)
		}
		metrics, err := evaluationAvailableNumericMetrics(entity.Metric, experiment.MetricPlan)
		if err != nil {
			return nil, fmt.Errorf("task %s: %w", taskID, err)
		}
		inputs = append(inputs, evaluationComparisonInput{
			entity: entity, experiment: experiment, parameters: parameters, metrics: metrics,
			metricSamples: map[string][]float64{},
		})
	}
	if e.questionResultRepository != nil {
		for index := range inputs {
			if err := e.loadEvaluationComparisonStatistics(ctx, tenantID, &inputs[index]); err != nil {
				return nil, err
			}
		}
	}
	if err := validateEvaluationComparisonContent(inputs); err != nil {
		return nil, err
	}

	runs := make([]types.EvaluationComparisonRun, 0, len(inputs))
	for _, input := range inputs {
		runs = append(runs, types.EvaluationComparisonRun{
			TaskID:                input.entity.ID,
			Status:                input.entity.Status,
			IsBaseline:            input.entity.ID == baselineID,
			DatasetID:             input.entity.DatasetID,
			DatasetVersionID:      *input.entity.DatasetVersionID,
			VersionNumber:         input.experiment.Dataset.VersionNumber,
			DatasetContentSHA256:  *input.entity.DatasetContentSHA256,
			ProvenanceComplete:    true,
			QuestionSuccessRate:   input.questionSuccess,
			QuestionSuccessStatus: input.questionStatus,
			QuestionNTotal:        input.questionTotal,
			QuestionNValid:        input.questionTotal,
			QuestionNMissing:      0,
			TotalLatency:          evaluationLatencyPercentiles(input.totalLatencies, input.questionTotal),
			TokenTotals:           input.tokenTotals,
		})
	}
	return &types.EvaluationComparisonResponse{
		SchemaVersion:  types.EvaluationComparisonSchemaVersion,
		BaselineTaskID: baselineID,
		Runs:           runs,
		Parameters:     buildEvaluationComparisonParameters(inputs),
		Metrics:        buildEvaluationComparisonMetrics(inputs, baselineID),
	}, nil
}

func containsEvaluationTaskID(ids []string, target string) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}

func validateEvaluationComparisonContent(inputs []evaluationComparisonInput) error {
	baselineHash := *inputs[0].entity.DatasetContentSHA256
	for _, input := range inputs[1:] {
		if *input.entity.DatasetContentSHA256 != baselineHash {
			return fmt.Errorf(
				"%w: dataset content SHA-256 differs for task %s",
				types.ErrEvaluationComparisonConflict,
				input.entity.ID,
			)
		}
	}
	return nil
}

func buildEvaluationComparisonParameters(inputs []evaluationComparisonInput) []types.EvaluationComparisonParameter {
	pointers := unionEvaluationComparisonPointers(
		inputs,
		func(input evaluationComparisonInput) map[string]json.RawMessage {
			return input.parameters
		},
	)
	parameters := make([]types.EvaluationComparisonParameter, 0, len(pointers))
	for _, pointer := range pointers {
		parameter := types.EvaluationComparisonParameter{
			Pointer: pointer,
			Values:  make([]types.EvaluationComparisonParameterValue, 0, len(inputs)),
		}
		reference := ""
		for index, input := range inputs {
			raw, exists := input.parameters[pointer]
			value := types.EvaluationComparisonParameterValue{
				TaskID: input.entity.ID, Missing: !exists, Value: raw,
			}
			canonical := "missing"
			if exists {
				canonical = "value:" + string(raw)
			}
			if index == 0 {
				reference = canonical
			} else if canonical != reference {
				parameter.Differ = true
			}
			parameter.Values = append(parameter.Values, value)
		}
		parameters = append(parameters, parameter)
	}
	return parameters
}

func buildEvaluationComparisonMetrics(
	inputs []evaluationComparisonInput,
	baselineID string,
) []types.EvaluationComparisonMetric {
	pointerSet := make(map[string]struct{})
	for _, input := range inputs {
		for pointer := range input.metrics {
			pointerSet[pointer] = struct{}{}
		}
	}
	pointers := make([]string, 0, len(pointerSet))
	for pointer := range pointerSet {
		pointers = append(pointers, pointer)
	}
	sort.Strings(pointers)

	metrics := make([]types.EvaluationComparisonMetric, 0, len(pointers))
	for _, pointer := range pointers {
		identities := make([]types.EvaluationComparisonMetricIdentity, len(inputs))
		identitiesFound := make([]bool, len(inputs))
		for index, input := range inputs {
			identities[index], identitiesFound[index] = types.EvaluationComparisonMetricIdentityForPath(
				input.experiment,
				pointer,
			)
		}
		identity, identityFound := firstEvaluationMetricIdentity(identities, identitiesFound)
		compatible, reason := compatibleEvaluationMetricIdentities(identities, identitiesFound)
		metric := types.EvaluationComparisonMetric{
			Pointer:        pointer,
			Key:            identity.Key,
			Version:        identity.Version,
			ConfigSHA256:   identity.ConfigSHA256,
			Compatible:     compatible,
			BaselineTaskID: baselineID,
			Values:         make([]types.EvaluationComparisonMetricValue, 0, len(inputs)),
		}
		if !identityFound {
			metric.Key = pointer
		}
		for _, input := range inputs {
			value, exists := input.metrics[pointer]
			wire := types.EvaluationComparisonMetricValue{
				TaskID: input.entity.ID, IsBaseline: input.entity.ID == baselineID,
				Status:           types.EvaluationComparisonValueMissing,
				ConfidenceStatus: types.EvaluationStatisticsInsufficientSample,
				NTotal:           input.questionTotal,
			}
			if exists {
				wire.Value = &value
				wire.Status = types.EvaluationComparisonValueValid
				if !compatible {
					wire.Status = types.EvaluationComparisonValueIncompatible
					wire.Reason = reason
				}
				samples := input.metricSamples[pointer]
				wire.NValid = len(samples)
				wire.NMissing = wire.NTotal - wire.NValid
				if len(samples) >= 2 {
					interval, intervalErr := evaluationstats.BootstrapMean(
						samples, 0.95, 2000, evaluationStatisticsSeed(input.entity.ID, pointer),
					)
					if intervalErr == nil {
						wire.Confidence = evaluationConfidenceInterval(interval)
						wire.ConfidenceStatus = types.EvaluationStatisticsValid
					}
				}
			} else {
				wire.NMissing = wire.NTotal
			}
			metric.Values = append(metric.Values, wire)
		}
		if compatible {
			completeEvaluationMetricDeltas(&metric)
		}
		metrics = append(metrics, metric)
	}
	return metrics
}

func (e *EvaluationService) loadEvaluationComparisonStatistics(
	ctx context.Context,
	tenantID uint64,
	input *evaluationComparisonInput,
) error {
	successes := 0
	trials := 0
	sampleIndexFrom := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		rows, err := e.questionResultRepository.ListQuestionResults(
			ctx,
			tenantID,
			input.entity.ID,
			sampleIndexFrom,
			types.EvaluationQuestionPageMaxSize,
		)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			trials++
			if row.TotalMs != nil {
				input.totalLatencies = append(input.totalLatencies, float64(*row.TotalMs))
			}
			if row.TotalTokens != nil {
				input.tokenTotals.NValid++
				input.tokenTotals.Total += int64(*row.TotalTokens)
				if row.PromptTokens != nil {
					input.tokenTotals.Prompt += int64(*row.PromptTokens)
				}
				if row.CompletionTokens != nil {
					input.tokenTotals.Completion += int64(*row.CompletionTokens)
				}
			}
			if row.Status != types.EvaluationQuestionStatusSuccess {
				continue
			}
			successes++
			metrics, flattenErr := evaluationAvailableNumericMetrics(
				row.PerSampleMetrics,
				input.experiment.MetricPlan,
			)
			if flattenErr != nil {
				return fmt.Errorf("task %s sample %d metrics: %w", input.entity.ID, row.SampleIndex, flattenErr)
			}
			for pointer, value := range metrics {
				input.metricSamples[pointer] = append(input.metricSamples[pointer], value)
			}
		}
		if len(rows) < types.EvaluationQuestionPageMaxSize {
			break
		}
		next := rows[len(rows)-1].SampleIndex + 1
		if next <= sampleIndexFrom {
			return fmt.Errorf(
				"%w: task %s question pagination did not advance",
				types.ErrEvaluationComparisonDataInvalid,
				input.entity.ID,
			)
		}
		sampleIndexFrom = next
	}
	input.questionTotal = trials
	input.tokenTotals.NTotal = trials
	input.tokenTotals.NMissing = trials - input.tokenTotals.NValid
	input.questionStatus = types.EvaluationStatisticsInsufficientSample
	if trials >= 2 {
		interval, err := evaluationstats.Wilson(successes, trials, 0.95)
		if err == nil {
			input.questionSuccess = evaluationConfidenceInterval(interval)
			input.questionStatus = types.EvaluationStatisticsValid
		}
	}
	return nil
}

// evaluationAvailableNumericMetrics projects a metric result through its
// frozen plan. Dynamic score state decides availability, while fixed fields
// remain comparison aliases only when their mapping is unambiguous.
func evaluationAvailableNumericMetrics(
	raw types.JSON,
	plan *types.EvaluationMetricPlanSnapshot,
) (map[string]float64, error) {
	if plan == nil {
		return nil, fmt.Errorf("%w: frozen metric plan is required", types.ErrEvaluationComparisonDataInvalid)
	}
	var result types.MetricResult
	if len(strings.TrimSpace(string(raw))) == 0 || !json.Valid(raw) {
		return nil, fmt.Errorf("%w: metric result is invalid JSON", types.ErrEvaluationComparisonDataInvalid)
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("%w: decode metric result: %v", types.ErrEvaluationComparisonDataInvalid, err)
	}

	fixedCounts := make(map[string]int)
	for _, spec := range plan.Metrics {
		if pointer, ok := types.EvaluationMetricFixedPointer(spec); ok {
			fixedCounts[pointer]++
		}
	}
	legacyFixedOnly := len(result.Scores) == 0

	metrics := make(map[string]float64, len(plan.Metrics)*2)
	for _, spec := range plan.Metrics {
		score, exists := result.Scores[spec.InstanceID]
		fixedPointer, hasFixedPointer := types.EvaluationMetricFixedPointer(spec)
		if !exists {
			// Version 1 fixed fields are the defined compatibility representation
			// only for persisted results that carry no dynamic score state at all.
			if legacyFixedOnly && spec.Version == "1.0.0" && hasFixedPointer && fixedCounts[fixedPointer] == 1 {
				value, _ := types.EvaluationMetricFixedValue(&result, fixedPointer)
				metrics[fixedPointer] = value
			}
			continue
		}
		switch score.Status {
		case types.EvaluationMetricObservationMissing,
			types.EvaluationMetricObservationSkipped,
			types.EvaluationMetricObservationFailed:
			continue
		case types.EvaluationMetricObservationValid:
		default:
			return nil, fmt.Errorf(
				"%w: metric %s has unknown status %q",
				types.ErrEvaluationComparisonDataInvalid,
				spec.InstanceID,
				score.Status,
			)
		}
		if score.Value == nil {
			return nil, fmt.Errorf(
				"%w: valid metric %s has no value",
				types.ErrEvaluationComparisonDataInvalid,
				spec.InstanceID,
			)
		}
		metrics[types.EvaluationMetricScoreValuePointer(spec.InstanceID)] = *score.Value
		if !hasFixedPointer || fixedCounts[fixedPointer] != 1 {
			continue
		}
		metrics[fixedPointer] = *score.Value
	}
	return metrics, nil
}

func evaluationLatencyPercentiles(values []float64, total int) *types.EvaluationPercentiles {
	if len(values) == 0 {
		return nil
	}
	p50, p95, p99, err := evaluationstats.Percentiles(values)
	if err != nil {
		return nil
	}
	return &types.EvaluationPercentiles{
		P50: p50, P95: p95, P99: p99,
		NTotal: total, NValid: len(values), NMissing: total - len(values),
	}
}

// evaluationStatisticsSeed derives a repeatable bootstrap seed. It carries no
// integrity or security meaning, so a stable non-cryptographic hash is enough.
func evaluationStatisticsSeed(taskID, pointer string) int64 {
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(taskID))
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write([]byte(pointer))
	return int64(hasher.Sum64() & ((1 << 63) - 1))
}

func evaluationConfidenceInterval(interval *evaluationstats.Interval) *types.EvaluationConfidenceInterval {
	if interval == nil {
		return nil
	}
	return &types.EvaluationConfidenceInterval{
		Estimate: interval.Estimate, Lower: interval.Lower, Upper: interval.Upper,
		Confidence: interval.Confidence, Method: interval.Method, Samples: interval.Samples,
		Iterations: interval.Iterations, Seed: interval.Seed,
	}
}

func firstEvaluationMetricIdentity(
	identities []types.EvaluationComparisonMetricIdentity,
	found []bool,
) (types.EvaluationComparisonMetricIdentity, bool) {
	for index, identity := range identities {
		if found[index] {
			return identity, true
		}
	}
	return types.EvaluationComparisonMetricIdentity{}, false
}

func compatibleEvaluationMetricIdentities(
	identities []types.EvaluationComparisonMetricIdentity,
	found []bool,
) (bool, string) {
	reference, ok := firstEvaluationMetricIdentity(identities, found)
	if !ok {
		return false, types.EvaluationComparisonReasonIdentityMissing
	}
	for index, identity := range identities {
		if !found[index] {
			return false, types.EvaluationComparisonReasonIdentityMissing
		}
		if identity != reference {
			return false, types.EvaluationComparisonReasonIdentityDiffers
		}
	}
	return true, ""
}

func completeEvaluationMetricDeltas(metric *types.EvaluationComparisonMetric) {
	var baseline *float64
	for _, value := range metric.Values {
		if value.IsBaseline && value.Status == types.EvaluationComparisonValueValid {
			baseline = value.Value
			break
		}
	}
	if baseline == nil {
		for index := range metric.Values {
			if metric.Values[index].Status == types.EvaluationComparisonValueValid {
				metric.Values[index].Reason = types.EvaluationComparisonReasonBaselineMissing
			}
		}
		return
	}
	for index := range metric.Values {
		value := &metric.Values[index]
		if value.IsBaseline || value.Status != types.EvaluationComparisonValueValid || value.Value == nil {
			continue
		}
		delta := *value.Value - *baseline
		value.Delta = &delta
		if *baseline == 0 {
			value.RelativeReason = types.EvaluationComparisonReasonBaselineZero
			continue
		}
		relative := delta / math.Abs(*baseline)
		value.RelativeDelta = &relative
	}
}

func unionEvaluationComparisonPointers(
	inputs []evaluationComparisonInput,
	selectValues func(evaluationComparisonInput) map[string]json.RawMessage,
) []string {
	set := make(map[string]struct{})
	for _, input := range inputs {
		for pointer := range selectValues(input) {
			set[pointer] = struct{}{}
		}
	}
	pointers := make([]string, 0, len(set))
	for pointer := range set {
		pointers = append(pointers, pointer)
	}
	sort.Strings(pointers)
	return pointers
}

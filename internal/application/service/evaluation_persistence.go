package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

type evaluationRunState struct {
	tenantID       uint64
	taskID         string
	ownerID        string
	version        uint64
	metric         types.JSON
	runtimeMetrics types.JSON
	leaseExpiresAt time.Time
}

func newEvaluationRunState(entity *types.EvaluationTaskEntity) (*evaluationRunState, error) {
	if entity == nil || entity.TenantID == 0 || entity.ID == "" || entity.OwnerID == "" || entity.Version == 0 {
		return nil, errors.New(
			"initialize evaluation run state: persisted tenant, task, owner, and version are required",
		)
	}
	var leaseExpiresAt time.Time
	if entity.LeaseExpiresAt != nil {
		leaseExpiresAt = entity.LeaseExpiresAt.UTC()
	}
	return &evaluationRunState{
		tenantID:       entity.TenantID,
		taskID:         entity.ID,
		ownerID:        entity.OwnerID,
		version:        entity.Version,
		metric:         append(types.JSON(nil), entity.Metric...),
		runtimeMetrics: append(types.JSON(nil), entity.RuntimeMetrics...),
		leaseExpiresAt: leaseExpiresAt,
	}, nil
}

func evaluationDetailToEntity(
	detail *types.EvaluationDetail,
	temporaryKnowledgeBaseID string,
	ownerID string,
	leaseExpiresAt time.Time,
) (*types.EvaluationTaskEntity, error) {
	if detail == nil || detail.Task == nil || detail.Params == nil {
		return nil, errors.New("persist evaluation detail: task and params are required")
	}
	if detail.Task.ID == "" || detail.Task.TenantID == 0 || detail.Task.DatasetID == "" {
		return nil, errors.New("persist evaluation detail: id, tenant_id, and dataset_id are required")
	}
	if detail.Task.StartTime.IsZero() {
		return nil, errors.New("persist evaluation detail: start_time is required")
	}
	if temporaryKnowledgeBaseID == "" || ownerID == "" || leaseExpiresAt.IsZero() {
		return nil, errors.New("persist evaluation detail: temporary knowledge base, owner, and lease are required")
	}
	if !isKnownEvaluationStatus(detail.Task.Status) {
		return nil, fmt.Errorf("persist evaluation detail: unsupported status %d", detail.Task.Status)
	}

	params, err := json.Marshal(detail.Params)
	if err != nil {
		return nil, fmt.Errorf("persist evaluation params: %w", err)
	}
	cleanupErrors := detail.Task.CleanupErrors
	if cleanupErrors == nil {
		cleanupErrors = []string{}
	}
	cleanupJSON, err := json.Marshal(cleanupErrors)
	if err != nil {
		return nil, fmt.Errorf("persist evaluation cleanup errors: %w", err)
	}
	var metricJSON types.JSON
	if detail.Metric != nil {
		encodedMetric, marshalErr := json.Marshal(detail.Metric)
		if marshalErr != nil {
			return nil, fmt.Errorf("persist evaluation metric: %w", marshalErr)
		}
		metricJSON = types.JSON(encodedMetric)
	}
	var runtimeMetricsJSON types.JSON
	if detail.RuntimeMetrics != nil {
		encodedRuntimeMetrics, marshalErr := json.Marshal(detail.RuntimeMetrics)
		if marshalErr != nil {
			return nil, fmt.Errorf("persist evaluation runtime metrics: %w", marshalErr)
		}
		runtimeMetricsJSON = types.JSON(encodedRuntimeMetrics)
	}

	startTime := detail.Task.StartTime.UTC()
	leaseExpiresAt = leaseExpiresAt.UTC()
	var endTime *time.Time
	if detail.Task.EndTime != nil {
		normalizedEndTime := detail.Task.EndTime.UTC()
		endTime = &normalizedEndTime
	}
	var cancelRequestedAt *time.Time
	if detail.Task.CancelRequestedAt != nil {
		normalizedCancelRequestedAt := detail.Task.CancelRequestedAt.UTC()
		cancelRequestedAt = &normalizedCancelRequestedAt
	}
	return &types.EvaluationTaskEntity{
		ID:                       detail.Task.ID,
		TenantID:                 detail.Task.TenantID,
		DatasetID:                detail.Task.DatasetID,
		Status:                   detail.Task.Status,
		StartTime:                startTime,
		EndTime:                  endTime,
		Total:                    detail.Task.Total,
		Finished:                 detail.Task.Finished,
		ErrMsg:                   detail.Task.ErrMsg,
		CleanupErrors:            types.JSON(cleanupJSON),
		Params:                   types.JSON(params),
		Metric:                   metricJSON,
		RuntimeMetrics:           runtimeMetricsJSON,
		TemporaryKnowledgeBaseID: temporaryKnowledgeBaseID,
		OwnerID:                  ownerID,
		LeaseExpiresAt:           &leaseExpiresAt,
		CancelRequestedAt:        cancelRequestedAt,
	}, nil
}

func evaluationEntityToDetail(entity *types.EvaluationTaskEntity) (*types.EvaluationDetail, error) {
	if entity == nil {
		return nil, errors.New("decode evaluation task: entity is required")
	}
	if entity.ID == "" || entity.TenantID == 0 || entity.DatasetID == "" || entity.StartTime.IsZero() {
		return nil, errors.New("decode evaluation task: id, tenant_id, dataset_id, and start_time are required")
	}
	if !isKnownEvaluationStatus(entity.Status) {
		return nil, fmt.Errorf("decode evaluation task: unsupported status %d", entity.Status)
	}

	task, err := EvaluationTaskEntityToAPITask(entity)
	if err != nil {
		return nil, err
	}
	params, err := decodeEvaluationParams(entity.Params)
	if err != nil {
		return nil, err
	}
	metric, err := decodeEvaluationMetric(entity.Metric)
	if err != nil {
		return nil, err
	}
	runtimeMetrics, err := decodeEvaluationRuntimeMetrics(entity.RuntimeMetrics)
	if err != nil {
		return nil, err
	}

	experiment, provenanceComplete, err := decodeEvaluationExperiment(entity)
	if err != nil {
		return nil, err
	}
	return &types.EvaluationDetail{
		Task:               task,
		Params:             params,
		Metric:             metric,
		RuntimeMetrics:     runtimeMetrics,
		Experiment:         experiment,
		ProvenanceComplete: provenanceComplete,
	}, nil
}

// decodeEvaluationExperiment restores the frozen experiment manifest. Pre-M3
// tasks keep null provenance: experiment=nil and provenance_complete=false
// instead of an empty fabricated object.
func decodeEvaluationExperiment(
	entity *types.EvaluationTaskEntity,
) (*types.EvaluationExperimentSnapshot, bool, error) {
	trimmed := bytes.TrimSpace(entity.ExperimentSnapshot)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, false, nil
	}
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &shape); err != nil || shape == nil {
		return nil, false, errors.New("decode evaluation experiment: invalid JSON object")
	}
	var experiment types.EvaluationExperimentSnapshot
	if err := json.Unmarshal(trimmed, &experiment); err != nil {
		return nil, false, fmt.Errorf("decode evaluation experiment: %w", err)
	}
	complete := entity.DatasetVersionID != nil && *entity.DatasetVersionID != "" &&
		entity.DatasetContentSHA256 != nil && len(*entity.DatasetContentSHA256) == 64 &&
		entity.ExperimentSHA256 != nil && len(*entity.ExperimentSHA256) == 64
	return &experiment, complete, nil
}

// encodeEvaluationExperiment serializes the frozen manifest for persistence.
func encodeEvaluationExperiment(
	experiment *types.EvaluationExperimentSnapshot,
) (types.JSON, error) {
	if experiment == nil {
		return nil, nil
	}
	canonical, err := experiment.CanonicalJSON()
	if err != nil {
		return nil, fmt.Errorf("encode evaluation experiment: %w", err)
	}
	return types.JSON(canonical), nil
}

func decodeEvaluationParams(value types.JSON) (*types.ChatManage, error) {
	if len(value) == 0 || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return nil, errors.New("decode evaluation params: JSON object is required")
	}
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(value, &shape); err != nil || shape == nil {
		return nil, errors.New("decode evaluation params: invalid JSON object")
	}
	var params types.ChatManage
	if err := json.Unmarshal(value, &params); err != nil {
		return nil, fmt.Errorf("decode evaluation params: %w", err)
	}
	if params.ChatModelID == "" {
		return nil, errors.New("decode evaluation params: chat_model_id is required")
	}
	return &params, nil
}

func decodeEvaluationCleanupErrors(value types.JSON) ([]string, error) {
	var cleanupErrors []string
	if err := json.Unmarshal(value, &cleanupErrors); err != nil || cleanupErrors == nil {
		return nil, errors.New("decode evaluation cleanup errors: invalid JSON string array")
	}
	return cleanupErrors, nil
}

func decodeEvaluationMetric(value types.JSON) (*types.MetricResult, error) {
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &shape); err != nil || shape == nil {
		return nil, errors.New("decode evaluation metric: invalid JSON object")
	}
	var metric types.MetricResult
	if err := json.Unmarshal(trimmed, &metric); err != nil {
		return nil, fmt.Errorf("decode evaluation metric: %w", err)
	}
	return &metric, nil
}

func encodeEvaluationMetric(metric *types.MetricResult) (types.JSON, error) {
	if metric == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(metric)
	if err != nil {
		return nil, fmt.Errorf("encode evaluation metric: %w", err)
	}
	return types.JSON(encoded), nil
}

func decodeEvaluationRuntimeMetrics(value types.JSON) (*types.EvaluationRuntimeMetrics, error) {
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &shape); err != nil || shape == nil {
		return nil, errors.New("decode evaluation runtime metrics: invalid JSON object")
	}
	var runtimeMetrics types.EvaluationRuntimeMetrics
	if err := json.Unmarshal(trimmed, &runtimeMetrics); err != nil {
		return nil, fmt.Errorf("decode evaluation runtime metrics: %w", err)
	}
	if runtimeMetrics.SchemaVersion != 1 || runtimeMetrics.StartedAt.IsZero() {
		return nil, errors.New("decode evaluation runtime metrics: schema_version=1 and started_at are required")
	}
	return &runtimeMetrics, nil
}

func encodeEvaluationRuntimeMetrics(runtimeMetrics *types.EvaluationRuntimeMetrics) (types.JSON, error) {
	if runtimeMetrics == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(runtimeMetrics)
	if err != nil {
		return nil, fmt.Errorf("encode evaluation runtime metrics: %w", err)
	}
	return types.JSON(encoded), nil
}

func encodeEvaluationCleanupErrors(cleanupErrors []string) (types.JSON, error) {
	if cleanupErrors == nil {
		cleanupErrors = []string{}
	}
	encoded, err := json.Marshal(cleanupErrors)
	if err != nil {
		return nil, fmt.Errorf("encode evaluation cleanup errors: %w", err)
	}
	return types.JSON(encoded), nil
}

func isKnownEvaluationStatus(status types.EvaluationStatue) bool {
	return status >= types.EvaluationStatuePending && status <= types.EvaluationStatueCanceled
}

// EvaluationTaskEntityToAPITask projects one entity into the public task
// shape without decoding params or metrics, keeping list responses lean.
func EvaluationTaskEntityToAPITask(entity *types.EvaluationTaskEntity) (*types.EvaluationTask, error) {
	if entity == nil {
		return nil, errors.New("project evaluation task: entity is required")
	}
	if !isKnownEvaluationStatus(entity.Status) {
		return nil, fmt.Errorf("project evaluation task: unsupported status %d", entity.Status)
	}
	cleanupErrors, err := decodeEvaluationCleanupErrors(entity.CleanupErrors)
	if err != nil {
		return nil, err
	}
	var endTime *time.Time
	if entity.EndTime != nil {
		normalizedEndTime := entity.EndTime.UTC()
		endTime = &normalizedEndTime
	}
	var cancelRequestedAt *time.Time
	if entity.CancelRequestedAt != nil {
		normalizedCancelRequestedAt := entity.CancelRequestedAt.UTC()
		cancelRequestedAt = &normalizedCancelRequestedAt
	}
	return &types.EvaluationTask{
		ID:                entity.ID,
		TenantID:          entity.TenantID,
		DatasetID:         entity.DatasetID,
		StartTime:         entity.StartTime.UTC(),
		EndTime:           endTime,
		Status:            entity.Status,
		ErrMsg:            entity.ErrMsg,
		CancelRequestedAt: cancelRequestedAt,
		CleanupErrors:     cleanupErrors,
		Labels:            append([]string(nil), entity.Labels...),
		DatasetVersionID:  entity.DatasetVersionID,
		ProvenanceComplete: entity.DatasetVersionID != nil && *entity.DatasetVersionID != "" &&
			entity.DatasetContentSHA256 != nil && len(*entity.DatasetContentSHA256) == 64 &&
			entity.ExperimentSHA256 != nil && len(*entity.ExperimentSHA256) == 64 &&
			len(entity.ExperimentSnapshot) > 0,
		Total:    entity.Total,
		Finished: entity.Finished,
	}, nil
}

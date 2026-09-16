package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

// EvaluationComparisonRequest selects runs and an optional baseline.
type EvaluationComparisonRequest struct {
	TaskIDs        []string `json:"task_ids"`
	BaselineTaskID string   `json:"baseline_task_id,omitempty"`
}

// EvaluationComparisonRun describes a persisted run and its observed coverage.
type EvaluationComparisonRun struct {
	TaskID                string                        `json:"task_id"`
	Status                EvaluationStatus              `json:"status"`
	IsBaseline            bool                          `json:"is_baseline"`
	DatasetID             string                        `json:"dataset_id"`
	DatasetVersionID      string                        `json:"dataset_version_id"`
	VersionNumber         int                           `json:"version_number"`
	DatasetContentSHA256  string                        `json:"dataset_content_sha256"`
	ProvenanceComplete    bool                          `json:"provenance_complete"`
	QuestionSuccessRate   *EvaluationConfidenceInterval `json:"question_success_rate,omitempty"`
	QuestionSuccessStatus string                        `json:"question_success_status"`
	QuestionNTotal        int                           `json:"question_n_total"`
	QuestionNValid        int                           `json:"question_n_valid"`
	QuestionNMissing      int                           `json:"question_n_missing"`
	TotalLatency          *EvaluationPercentiles        `json:"total_latency_ms,omitempty"`
	TokenTotals           EvaluationTokenTotals         `json:"token_totals"`
}

// EvaluationPercentiles reports latency quantiles with valid and missing sample counts.
type EvaluationPercentiles struct {
	P50      float64 `json:"p50"`
	P95      float64 `json:"p95"`
	P99      float64 `json:"p99"`
	NTotal   int     `json:"n_total"`
	NValid   int     `json:"n_valid"`
	NMissing int     `json:"n_missing"`
}

// EvaluationTokenTotals preserves token totals and reporting coverage.
type EvaluationTokenTotals struct {
	Prompt     int64 `json:"prompt"`
	Completion int64 `json:"completion"`
	Total      int64 `json:"total"`
	NTotal     int   `json:"n_total"`
	NValid     int   `json:"n_valid"`
	NMissing   int   `json:"n_missing"`
}

// EvaluationConfidenceInterval is a deterministic two-sided estimate interval.
type EvaluationConfidenceInterval struct {
	Estimate   float64 `json:"estimate"`
	Lower      float64 `json:"lower"`
	Upper      float64 `json:"upper"`
	Confidence float64 `json:"confidence"`
	Method     string  `json:"method"`
	Samples    int     `json:"samples"`
	Iterations int     `json:"iterations,omitempty"`
	Seed       int64   `json:"seed,omitempty"`
}

// EvaluationComparisonParameterValue contains one run's value at a configuration pointer.
type EvaluationComparisonParameterValue struct {
	TaskID  string          `json:"task_id"`
	Missing bool            `json:"missing"`
	Value   json.RawMessage `json:"value,omitempty"`
}

// EvaluationComparisonParameter compares a configuration field across runs.
type EvaluationComparisonParameter struct {
	Pointer string                               `json:"pointer"`
	Differ  bool                                 `json:"differ"`
	Values  []EvaluationComparisonParameterValue `json:"values"`
}

// EvaluationComparisonMetricValue contains one run's metric value and uncertainty.
type EvaluationComparisonMetricValue struct {
	TaskID           string                        `json:"task_id"`
	IsBaseline       bool                          `json:"is_baseline"`
	Status           string                        `json:"status"`
	Value            *float64                      `json:"value"`
	Delta            *float64                      `json:"delta"`
	RelativeDelta    *float64                      `json:"relative_delta"`
	RelativeReason   string                        `json:"relative_reason,omitempty"`
	Reason           string                        `json:"reason,omitempty"`
	Confidence       *EvaluationConfidenceInterval `json:"confidence,omitempty"`
	ConfidenceStatus string                        `json:"confidence_status"`
	NTotal           int                           `json:"n_total"`
	NValid           int                           `json:"n_valid"`
	NMissing         int                           `json:"n_missing"`
}

// EvaluationComparisonMetric groups values with their algorithm identity and compatibility.
type EvaluationComparisonMetric struct {
	Pointer        string                            `json:"pointer"`
	Key            string                            `json:"key"`
	Version        string                            `json:"version"`
	ConfigSHA256   string                            `json:"config_sha256"`
	Compatible     bool                              `json:"compatible"`
	BaselineTaskID string                            `json:"baseline_task_id"`
	Values         []EvaluationComparisonMetricValue `json:"values"`
}

// EvaluationComparisonResponse contains the server's persisted-run comparison.
type EvaluationComparisonResponse struct {
	SchemaVersion  int                             `json:"schema_version"`
	BaselineTaskID string                          `json:"baseline_task_id"`
	Runs           []EvaluationComparisonRun       `json:"runs"`
	Parameters     []EvaluationComparisonParameter `json:"parameters"`
	Metrics        []EvaluationComparisonMetric    `json:"metrics"`
}

type evaluationComparisonEnvelope struct {
	Success bool                          `json:"success"`
	Data    *EvaluationComparisonResponse `json:"data"`
}

// CompareEvaluationRuns decodes the server's authoritative comparison.
func (c *Client) CompareEvaluationRuns(
	ctx context.Context,
	request EvaluationComparisonRequest,
) (*EvaluationComparisonResponse, error) {
	if len(request.TaskIDs) < 2 {
		return nil, errors.New("evaluation comparison requires at least two task IDs")
	}
	response, err := c.doRequest(
		ctx,
		http.MethodPost,
		"/api/v1/evaluation/comparisons",
		request,
		nil,
	)
	if err != nil {
		return nil, err
	}
	var envelope evaluationComparisonEnvelope
	if err := parseResponse(response, &envelope); err != nil {
		return nil, err
	}
	if !envelope.Success {
		return nil, errors.New("evaluation comparison response is not successful")
	}
	if envelope.Data == nil {
		return nil, errors.New("evaluation comparison response is missing data")
	}
	if envelope.Data.Runs == nil || envelope.Data.Parameters == nil || envelope.Data.Metrics == nil {
		return nil, errors.New("evaluation comparison response has an incomplete data envelope")
	}
	return envelope.Data, nil
}

// Package client provides the implementation for interacting with the WeKnora API.
// The evaluation interfaces start and retrieve knowledge-base evaluation runs.
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

const (
	EvaluationStatusPending = "pending"
	EvaluationStatusRunning = "running"
	EvaluationStatusSuccess = "success"
	EvaluationStatusFailed  = "failed"
)

// decodeEvaluationStatus accepts the server's numeric enum and string values
// from older deployments, while exposing a stable, readable status.
func decodeEvaluationStatus(data []byte) (string, error) {
	var numeric int
	if err := json.Unmarshal(data, &numeric); err == nil {
		switch numeric {
		case 0:
			return EvaluationStatusPending, nil
		case 1:
			return EvaluationStatusRunning, nil
		case 2:
			return EvaluationStatusSuccess, nil
		case 3:
			return EvaluationStatusFailed, nil
		default:
			return "", fmt.Errorf("unknown evaluation status: %d", numeric)
		}
	}

	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return "", fmt.Errorf("decode evaluation status: %w", err)
	}
	return value, nil
}

// EvaluationTask represents the task object returned inside an evaluation detail.
type EvaluationTask struct {
	ID        string    `json:"id"`
	TenantID  uint64    `json:"tenant_id"`
	DatasetID string    `json:"dataset_id"`
	StartTime time.Time `json:"start_time"`
	Status    string    `json:"status"`
	ErrMsg    string    `json:"err_msg,omitempty"`
	Total     int       `json:"total,omitempty"`
	Finished  int       `json:"finished,omitempty"`

	// Deprecated compatibility fields. The server does not return these
	// directly; Progress is derived from Finished and Total.
	Progress    int    `json:"-"`
	EmbeddingID string `json:"-"`
	ChatID      string `json:"-"`
	RerankID    string `json:"-"`
	CreatedAt   string `json:"-"`
	CompleteAt  string `json:"-"`
	ErrorMsg    string `json:"-"`
}

// UnmarshalJSON normalizes the server's numeric status without changing the
// SDK's existing public Status string field.
func (t *EvaluationTask) UnmarshalJSON(data []byte) error {
	type taskAlias EvaluationTask
	var raw struct {
		*taskAlias
		Status json.RawMessage `json:"status"`
	}
	raw.taskAlias = (*taskAlias)(t)
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	status, err := decodeEvaluationStatus(raw.Status)
	if err != nil {
		return err
	}
	t.Status = status
	return nil
}

func (t *EvaluationTask) hydrateCompatibilityFields() {
	if t == nil {
		return
	}
	if t.Total > 0 {
		t.Progress = t.Finished * 100 / t.Total
	}
	t.CreatedAt = t.StartTime.Format(time.RFC3339Nano)
	t.ErrorMsg = t.ErrMsg
}

// EvaluationMetricResult contains the retrieval and generation aggregates.
type EvaluationMetricResult struct {
	RetrievalMetrics  EvaluationRetrievalMetrics  `json:"retrieval_metrics"`
	GenerationMetrics EvaluationGenerationMetrics `json:"generation_metrics"`
}

// EvaluationRetrievalMetrics contains retrieval-quality metrics.
type EvaluationRetrievalMetrics struct {
	Precision float64 `json:"precision"`
	Recall    float64 `json:"recall"`
	NDCG3     float64 `json:"ndcg3"`
	NDCG10    float64 `json:"ndcg10"`
	MRR       float64 `json:"mrr"`
	MAP       float64 `json:"map"`
}

// EvaluationGenerationMetrics contains answer-overlap metrics.
type EvaluationGenerationMetrics struct {
	BLEU1  float64 `json:"bleu1"`
	BLEU2  float64 `json:"bleu2"`
	BLEU4  float64 `json:"bleu4"`
	ROUGE1 float64 `json:"rouge1"`
	ROUGE2 float64 `json:"rouge2"`
	ROUGEL float64 `json:"rougel"`
}

// EvaluationResult matches the server's EvaluationDetail response.
type EvaluationResult struct {
	Task   *EvaluationTask         `json:"task"`
	Params map[string]interface{}  `json:"params"`
	Metric *EvaluationMetricResult `json:"metric,omitempty"`

	// Deprecated flat fields retained for source compatibility with older SDK
	// callers. They are populated from Task and Metric after decoding.
	TaskID       string                   `json:"-"`
	Status       string                   `json:"-"`
	Progress     int                      `json:"-"`
	TotalQueries int                      `json:"-"`
	TotalSamples int                      `json:"-"`
	Metrics      map[string]float64       `json:"-"`
	QueriesStat  []map[string]interface{} `json:"-"`
	CreatedAt    string                   `json:"-"`
	CompleteAt   string                   `json:"-"`
	ErrorMsg     string                   `json:"-"`
}

func (r *EvaluationResult) hydrateCompatibilityFields() {
	if r == nil || r.Task == nil {
		return
	}
	r.Task.hydrateCompatibilityFields()
	r.TaskID = r.Task.ID
	r.Status = r.Task.Status
	r.Progress = r.Task.Progress
	r.TotalQueries = r.Task.Total
	r.TotalSamples = r.Task.Total
	r.CreatedAt = r.Task.CreatedAt
	r.ErrorMsg = r.Task.ErrMsg

	if r.Metric == nil {
		return
	}
	r.Metrics = map[string]float64{
		"precision": r.Metric.RetrievalMetrics.Precision,
		"recall":    r.Metric.RetrievalMetrics.Recall,
		"ndcg3":     r.Metric.RetrievalMetrics.NDCG3,
		"ndcg10":    r.Metric.RetrievalMetrics.NDCG10,
		"mrr":       r.Metric.RetrievalMetrics.MRR,
		"map":       r.Metric.RetrievalMetrics.MAP,
		"bleu1":     r.Metric.GenerationMetrics.BLEU1,
		"bleu2":     r.Metric.GenerationMetrics.BLEU2,
		"bleu4":     r.Metric.GenerationMetrics.BLEU4,
		"rouge1":    r.Metric.GenerationMetrics.ROUGE1,
		"rouge2":    r.Metric.GenerationMetrics.ROUGE2,
		"rougel":    r.Metric.GenerationMetrics.ROUGEL,
	}
}

// EvaluationRequest contains the server-supported evaluation parameters.
type EvaluationRequest struct {
	DatasetID       string `json:"dataset_id,omitempty"`
	KnowledgeBaseID string `json:"knowledge_base_id,omitempty"`
	ChatModelID     string `json:"chat_id,omitempty"`
	RerankModelID   string `json:"rerank_id,omitempty"`

	// Deprecated: the evaluation API selects the embedding model through
	// KnowledgeBaseID. This field is retained for source compatibility and is
	// intentionally not serialized.
	EmbeddingModelID string `json:"-"`
}

// EvaluationTaskResponse is the response envelope returned when a run starts.
type EvaluationTaskResponse struct {
	Success bool             `json:"success"`
	Data    EvaluationResult `json:"data"`
}

// EvaluationResultResponse is the response envelope returned when polling a run.
type EvaluationResultResponse struct {
	Success bool             `json:"success"`
	Data    EvaluationResult `json:"data"`
}

// StartEvaluation starts an evaluation run and returns its task metadata.
func (c *Client) StartEvaluation(ctx context.Context, request *EvaluationRequest) (*EvaluationTask, error) {
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/evaluation", request, nil)
	if err != nil {
		return nil, err
	}

	var response EvaluationTaskResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}
	if response.Data.Task == nil {
		return nil, fmt.Errorf("evaluation response did not include task metadata")
	}
	response.Data.Task.hydrateCompatibilityFields()
	return response.Data.Task, nil
}

// GetEvaluationResult retrieves an evaluation run by task ID.
func (c *Client) GetEvaluationResult(ctx context.Context, taskID string) (*EvaluationResult, error) {
	queryParams := url.Values{}
	queryParams.Add("task_id", taskID)

	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/evaluation", nil, queryParams)
	if err != nil {
		return nil, err
	}

	var response EvaluationResultResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}
	response.Data.hydrateCompatibilityFields()
	return &response.Data, nil
}

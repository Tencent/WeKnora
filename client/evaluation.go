// Package client provides the implementation for interacting with the WeKnora API
// The Evaluation related interfaces are used for starting and retrieving model evaluation task results
// Evaluation tasks can be used to measure model performance and
// compare different embedding models, chat models, and reranking models
package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"time"
)

// EvaluationTask represents an evaluation task
// Contains basic information about a model evaluation task
type EvaluationTask struct {
	ID          string `json:"id"`           // Task unique identifier
	Status      string `json:"status"`       // Task status: pending, running, completed, failed
	Progress    int    `json:"progress"`     // Task progress, integer value 0-100
	DatasetID   string `json:"dataset_id"`   // Evaluation dataset ID
	EmbeddingID string `json:"embedding_id"` // Embedding model ID
	ChatID      string `json:"chat_id"`      // Chat model ID
	RerankID    string `json:"rerank_id"`    // Reranking model ID
	CreatedAt   string `json:"created_at"`   // Task creation time
	CompleteAt  string `json:"complete_at"`  // Task completion time
	ErrorMsg    string `json:"error_msg"`    // Error message, has value when task fails
}

// EvaluationResult represents the evaluation results
// Contains detailed evaluation result information
type EvaluationResult struct {
	TaskID       string                   `json:"task_id"`       // Associated task ID
	Status       string                   `json:"status"`        // Task status
	Progress     int                      `json:"progress"`      // Task progress
	TotalQueries int                      `json:"total_queries"` // Total number of queries
	TotalSamples int                      `json:"total_samples"` // Total number of samples
	Metrics      map[string]float64       `json:"metrics"`       // Evaluation metrics collection
	QueriesStat  []map[string]interface{} `json:"queries_stat"`  // Statistics for each query
	CreatedAt    string                   `json:"created_at"`    // Creation time
	CompleteAt   string                   `json:"complete_at"`   // Completion time
	ErrorMsg     string                   `json:"error_msg"`     // Error message
}

// EvaluationRequest represents an evaluation request
// Parameters used to start a new evaluation task
type EvaluationRequest struct {
	DatasetID        string `json:"dataset_id"`        // Dataset ID to evaluate
	KnowledgeBaseID  string `json:"knowledge_base_id"` // Knowledge base ID to evaluate
	EmbeddingModelID string `json:"embedding_id"`      // Embedding model ID
	ChatModelID      string `json:"chat_id"`           // Chat model ID
	RerankModelID    string `json:"rerank_id"`         // Reranking model ID
}

// EvaluationTaskResponse represents an evaluation task response
// API response structure for evaluation tasks
type EvaluationTaskResponse struct {
	Success bool           `json:"success"` // Whether operation was successful
	Data    EvaluationTask `json:"data"`    // Evaluation task data
}

// EvaluationResultResponse represents an evaluation result response
// API response structure for evaluation results
type EvaluationResultResponse struct {
	Success bool             `json:"success"` // Whether operation was successful
	Data    EvaluationResult `json:"data"`    // Evaluation result data
}

type legacyEvaluationTask struct {
	ID        string    `json:"id"`
	DatasetID string    `json:"dataset_id"`
	StartTime time.Time `json:"start_time"`
	Status    int       `json:"status"`
	ErrMsg    string    `json:"err_msg"`
	Total     int       `json:"total"`
	Finished  int       `json:"finished"`
}

type legacyEvaluationParams struct {
	ChatModelID   string `json:"chat_model_id"`
	RerankModelID string `json:"rerank_model_id"`
}

type legacyMetricResult struct {
	RetrievalMetrics  map[string]float64 `json:"retrieval_metrics"`
	GenerationMetrics map[string]float64 `json:"generation_metrics"`
}

func legacyStatusName(status int) string {
	switch status {
	case 1:
		return "running"
	case 2:
		return "completed"
	case 3:
		return "failed"
	default:
		return "pending"
	}
}

func legacyProgress(task legacyEvaluationTask) int {
	if task.Total <= 0 {
		return 0
	}
	return task.Finished * 100 / task.Total
}

func projectLegacyTask(task legacyEvaluationTask, params legacyEvaluationParams) EvaluationTask {
	return EvaluationTask{
		ID:        task.ID,
		Status:    legacyStatusName(task.Status),
		Progress:  legacyProgress(task),
		DatasetID: task.DatasetID,
		ChatID:    params.ChatModelID,
		RerankID:  params.RerankModelID,
		CreatedAt: task.StartTime.Format(time.RFC3339Nano),
		ErrorMsg:  task.ErrMsg,
	}
}

func (r *EvaluationTaskResponse) UnmarshalJSON(data []byte) error {
	type responseAlias EvaluationTaskResponse
	var envelope struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return err
	}
	var projection struct {
		Task   *legacyEvaluationTask  `json:"task"`
		Params legacyEvaluationParams `json:"params"`
	}
	if err := json.Unmarshal(envelope.Data, &projection); err != nil {
		return err
	}
	if projection.Task != nil {
		r.Success = envelope.Success
		r.Data = projectLegacyTask(*projection.Task, projection.Params)
		return nil
	}
	return json.Unmarshal(data, (*responseAlias)(r))
}

func (r *EvaluationResultResponse) UnmarshalJSON(data []byte) error {
	type responseAlias EvaluationResultResponse
	var envelope struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return err
	}
	var projection struct {
		Task   *legacyEvaluationTask `json:"task"`
		Metric legacyMetricResult    `json:"metric"`
	}
	if err := json.Unmarshal(envelope.Data, &projection); err != nil {
		return err
	}
	if projection.Task != nil {
		metrics := make(map[string]float64, len(projection.Metric.RetrievalMetrics)+len(projection.Metric.GenerationMetrics))
		for name, score := range projection.Metric.RetrievalMetrics {
			metrics[name] = score
		}
		for name, score := range projection.Metric.GenerationMetrics {
			metrics[name] = score
		}
		r.Success = envelope.Success
		r.Data = EvaluationResult{
			TaskID:       projection.Task.ID,
			Status:       legacyStatusName(projection.Task.Status),
			Progress:     legacyProgress(*projection.Task),
			TotalQueries: projection.Task.Total,
			TotalSamples: projection.Task.Total,
			Metrics:      metrics,
			CreatedAt:    projection.Task.StartTime.Format(time.RFC3339Nano),
			ErrorMsg:     projection.Task.ErrMsg,
		}
		return nil
	}
	return json.Unmarshal(data, (*responseAlias)(r))
}

// StartEvaluation starts an evaluation task.
//
// Deprecated: use the persistent Evaluation V2 dataset and run APIs under
// /api/v1/evaluation/datasets and /api/v1/evaluation/runs. This method is a
// compatibility projection and does not represent the V2 result model.
// Creates and starts a new evaluation task based on provided parameters
// Parameters:
//   - ctx: Context, used for passing request context information such as deadline, cancellation signals, etc.
//   - request: Evaluation request parameters, including dataset ID and model IDs
//
// Returns:
//   - *EvaluationTask: Created evaluation task information
//   - error: Error information if the request fails
func (c *Client) StartEvaluation(ctx context.Context, request *EvaluationRequest) (*EvaluationTask, error) {
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/evaluation", request, nil)
	if err != nil {
		return nil, err
	}

	var response EvaluationTaskResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}

	return &response.Data, nil
}

// GetEvaluationResult retrieves evaluation results.
//
// Deprecated: use GET /api/v1/evaluation/runs/:run_id and the run results API.
// Retrieves detailed results for an evaluation task by task ID
// Parameters:
//   - ctx: Context, used for passing request context information
//   - taskID: Evaluation task ID, used to identify the specific evaluation task to query
//
// Returns:
//   - *EvaluationResult: Detailed evaluation task results
//   - error: Error information if the request fails
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

	return &response.Data, nil
}

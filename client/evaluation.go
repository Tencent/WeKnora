// Package client provides the implementation for interacting with the WeKnora API
// The Evaluation related interfaces are used for starting and retrieving model evaluation task results
// Evaluation tasks can be used to measure model performance and
// compare different embedding models, chat models, and reranking models
package client

import (
	"context"
	"net/http"
	"net/url"
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
	DatasetID        string `json:"dataset_id"`   // Dataset ID to evaluate
	EmbeddingModelID string `json:"embedding_id"` // Embedding model ID
	ChatModelID      string `json:"chat_id"`      // Chat model ID
	RerankModelID    string `json:"rerank_id"`    // Reranking model ID
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

// StartEvaluation starts an evaluation task
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

// GetEvaluationResult retrieves evaluation results
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

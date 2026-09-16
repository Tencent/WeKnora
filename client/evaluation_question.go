package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// EvaluationRankedResult is one rank position with its provenance state.
type EvaluationRankedResult struct {
	Rank       int     `json:"rank"`
	PID        int     `json:"pid"`
	Score      float64 `json:"score"`
	Provenance string  `json:"provenance"`
}

// EvaluationQuestionResult is one immutable per-question fact row. Nullable
// runtime facts stay null instead of a fabricated zero.
type EvaluationQuestionResult struct {
	TenantID           uint64                   `json:"tenant_id"`
	TaskID             string                   `json:"task_id"`
	SampleIndex        int                      `json:"sample_index"`
	QID                string                   `json:"qid"`
	Question           string                   `json:"question"`
	ReferenceAnswer    string                   `json:"reference_answer"`
	GroundTruthPIDs    []int                    `json:"ground_truth_pids"`
	SearchResults      []EvaluationRankedResult `json:"search_results"`
	RerankResults      []EvaluationRankedResult `json:"rerank_results"`
	GenerationPIDs     []int                    `json:"generation_pids"`
	GeneratedText      string                   `json:"generated_text"`
	ErrorCode          string                   `json:"error_code,omitempty"`
	PerSampleMetrics   *EvaluationMetricResult  `json:"per_sample_metrics,omitempty"`
	MetricObservations json.RawMessage          `json:"metric_observations,omitempty"`
	RetrievalMs        *int64                   `json:"retrieval_ms,omitempty"`
	RerankMs           *int64                   `json:"rerank_ms,omitempty"`
	GenerationMs       *int64                   `json:"generation_ms,omitempty"`
	TotalMs            *int64                   `json:"total_ms,omitempty"`
	PromptTokens       *int                     `json:"prompt_tokens,omitempty"`
	CompletionTokens   *int                     `json:"completion_tokens,omitempty"`
	TotalTokens        *int                     `json:"total_tokens,omitempty"`
	UsageReported      bool                     `json:"usage_reported"`
	Status             string                   `json:"status"`
	ResultHash         string                   `json:"result_hash"`
	CreatedAt          time.Time                `json:"created_at"`
	UpdatedAt          time.Time                `json:"updated_at"`
}

// EvaluationQuestionResultPage is one keyset page in ascending sample_index.
type EvaluationQuestionResultPage struct {
	Items      []*EvaluationQuestionResult `json:"items"`
	NextCursor string                      `json:"next_cursor"`
}

// EvaluationQuestionResultPageResponse is the API envelope for one page.
type EvaluationQuestionResultPageResponse struct {
	Success bool                          `json:"success"`
	Data    *EvaluationQuestionResultPage `json:"data"`
}

// ListEvaluationQuestionResults reads one per-question page; pass an empty
// cursor for the first page and the returned NextCursor for the next one.
func (c *Client) ListEvaluationQuestionResults(
	ctx context.Context,
	taskID string,
	pageSize int,
	cursor string,
) (*EvaluationQuestionResultPage, error) {
	if taskID == "" {
		return nil, errors.New("evaluation task id is required")
	}
	query := url.Values{}
	if pageSize > 0 {
		query.Set("page_size", strconv.Itoa(pageSize))
	}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	resp, err := c.doRequest(
		ctx, http.MethodGet, "/api/v1/evaluation/tasks/"+taskID+"/questions", nil, query)
	if err != nil {
		return nil, err
	}
	var response EvaluationQuestionResultPageResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}
	if !response.Success || response.Data == nil {
		return nil, errors.New("evaluation question results response is missing data")
	}
	return response.Data, nil
}

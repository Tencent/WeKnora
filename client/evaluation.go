// Package client provides the implementation for interacting with the WeKnora API.
// Evaluation interfaces start an evaluation task and retrieve its result.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// EvaluationStatus is the numeric lifecycle status returned by the evaluation API.
type EvaluationStatus int

const (
	// EvaluationStatusPending indicates that the task is waiting to run.
	EvaluationStatusPending EvaluationStatus = iota
	// EvaluationStatusRunning indicates that the task is running.
	EvaluationStatusRunning
	// EvaluationStatusSuccess indicates that the task completed successfully.
	EvaluationStatusSuccess
	// EvaluationStatusFailed indicates that the task failed.
	EvaluationStatusFailed
	// EvaluationStatusTimedOut indicates that the task exceeded its deadline.
	EvaluationStatusTimedOut
	// EvaluationStatusInterrupted indicates that task execution was interrupted.
	EvaluationStatusInterrupted
	// EvaluationStatusCanceled indicates that the task was canceled by request.
	EvaluationStatusCanceled
)

// EvaluationTask contains the task state returned by the evaluation API.
type EvaluationTask struct {
	ID        string           `json:"id"`
	TenantID  uint64           `json:"tenant_id"`
	DatasetID string           `json:"dataset_id"`
	StartTime time.Time        `json:"start_time"`
	EndTime   *time.Time       `json:"end_time,omitempty"`
	Status    EvaluationStatus `json:"status"`
	ErrMsg    string           `json:"err_msg,omitempty"`

	CancelRequestedAt *time.Time `json:"cancel_requested_at,omitempty"`

	CleanupErrors []string `json:"cleanup_errors,omitempty"`
	Labels        []string `json:"labels"`

	DatasetVersionID   *string `json:"dataset_version_id,omitempty"`
	ProvenanceComplete bool    `json:"provenance_complete"`

	Total    int `json:"total,omitempty"`
	Finished int `json:"finished,omitempty"`
}

// UnmarshalJSON validates that an evaluation task contains a numeric status.
func (t *EvaluationTask) UnmarshalJSON(data []byte) error {
	type wireTask struct {
		ID        string            `json:"id"`
		TenantID  uint64            `json:"tenant_id"`
		DatasetID string            `json:"dataset_id"`
		StartTime time.Time         `json:"start_time"`
		EndTime   *time.Time        `json:"end_time,omitempty"`
		Status    *EvaluationStatus `json:"status"`
		ErrMsg    string            `json:"err_msg,omitempty"`

		CancelRequestedAt *time.Time `json:"cancel_requested_at,omitempty"`

		CleanupErrors []string `json:"cleanup_errors,omitempty"`
		Labels        []string `json:"labels"`

		DatasetVersionID   *string `json:"dataset_version_id,omitempty"`
		ProvenanceComplete bool    `json:"provenance_complete"`

		Total    int `json:"total,omitempty"`
		Finished int `json:"finished,omitempty"`
	}

	var wire wireTask
	if err := json.Unmarshal(data, &wire); err != nil {
		return fmt.Errorf("decode evaluation task: %w", err)
	}
	if wire.Status == nil {
		return errors.New("decode evaluation task: missing numeric status")
	}
	if !wire.Status.valid() {
		return fmt.Errorf("decode evaluation task: unknown numeric status %d", *wire.Status)
	}

	*t = EvaluationTask{
		ID:                 wire.ID,
		TenantID:           wire.TenantID,
		DatasetID:          wire.DatasetID,
		StartTime:          wire.StartTime,
		EndTime:            wire.EndTime,
		Status:             *wire.Status,
		ErrMsg:             wire.ErrMsg,
		CancelRequestedAt:  wire.CancelRequestedAt,
		CleanupErrors:      wire.CleanupErrors,
		Labels:             wire.Labels,
		DatasetVersionID:   wire.DatasetVersionID,
		ProvenanceComplete: wire.ProvenanceComplete,
		Total:              wire.Total,
		Finished:           wire.Finished,
	}
	return nil
}

func (s EvaluationStatus) valid() bool {
	return s >= EvaluationStatusPending && s <= EvaluationStatusCanceled
}

// EvaluationResult contains the task, request parameters, and optional metrics.
type EvaluationResult struct {
	Task           *EvaluationTask           `json:"task"`
	Params         json.RawMessage           `json:"params"`
	Metric         *EvaluationMetricResult   `json:"metric,omitempty"`
	RuntimeMetrics *EvaluationRuntimeMetrics `json:"runtime_metrics,omitempty"`

	// Experiment is the frozen schema-version-1 experiment manifest; null
	// for pre-M3 tasks. ProvenanceComplete distinguishes full provenance
	// from legacy tasks without a snapshot.
	Experiment         *EvaluationExperimentSnapshot `json:"experiment"`
	ProvenanceComplete bool                          `json:"provenance_complete"`
}

// EvaluationRuntimeMetrics is the task-level execution observability snapshot.
type EvaluationRuntimeMetrics struct {
	SchemaVersion int                        `json:"schema_version"`
	StartedAt     time.Time                  `json:"started_at"`
	EndedAt       *time.Time                 `json:"ended_at,omitempty"`
	Durations     EvaluationRuntimeDurations `json:"durations"`
	Samples       EvaluationRuntimeSamples   `json:"samples"`
	Failure       EvaluationRuntimeFailure   `json:"failure"`
	Tokens        EvaluationRuntimeTokens    `json:"tokens"`
	Cost          *EvaluationRuntimeCost     `json:"cost,omitempty"`
}

// EvaluationRuntimeCost preserves task-level accounting coverage and currency totals.
type EvaluationRuntimeCost struct {
	CallCount               int64            `json:"call_count"`
	AccountingCompleteCalls int64            `json:"accounting_complete_calls"`
	UnpricedCalls           int64            `json:"unpriced_calls"`
	UsageUnreportedCalls    int64            `json:"usage_unreported_calls"`
	StartedCalls            int64            `json:"started_calls"`
	Totals                  []ModelCostTotal `json:"totals"`
}

// EvaluationRuntimeDurations contains measured execution stages in milliseconds.
type EvaluationRuntimeDurations struct {
	DatasetLoadMs *int64 `json:"dataset_load_ms,omitempty"`
	IndexingMs    *int64 `json:"indexing_ms,omitempty"`
	ExecutionMs   *int64 `json:"execution_ms,omitempty"`
	PersistenceMs *int64 `json:"persistence_ms,omitempty"`
	CleanupMs     *int64 `json:"cleanup_ms,omitempty"`
	TotalMs       *int64 `json:"total_ms,omitempty"`
}

// EvaluationRuntimeSamples counts question outcomes, including unfinished work.
type EvaluationRuntimeSamples struct {
	Total       int `json:"total"`
	Started     int `json:"started"`
	Success     int `json:"success"`
	Failed      int `json:"failed"`
	Canceled    int `json:"canceled"`
	Interrupted int `json:"interrupted"`
	NotStarted  int `json:"not_started"`
}

// EvaluationRuntimeFailure preserves the numerator and denominator of a failure rate.
type EvaluationRuntimeFailure struct {
	Numerator   int `json:"numerator"`
	Denominator int `json:"denominator"`
}

// EvaluationRuntimeTokens reports observed token totals and usage coverage.
type EvaluationRuntimeTokens struct {
	PromptTokens      int `json:"prompt_tokens"`
	CompletionTokens  int `json:"completion_tokens"`
	TotalTokens       int `json:"total_tokens"`
	ReportedSamples   int `json:"reported_samples"`
	UnreportedSamples int `json:"unreported_samples"`
}

// EvaluationDatasetRef pins the dataset identity inside an experiment snapshot.
type EvaluationDatasetRef struct {
	DatasetID        string `json:"dataset_id"`
	DatasetVersionID string `json:"dataset_version_id"`
	VersionNumber    int    `json:"version_number"`
	ArtifactSHA256   string `json:"artifact_sha256"`
	ContentSHA256    string `json:"content_sha256"`
}

// EvaluationGenerationConfig is the resolved generation section of an
// experiment snapshot, including the seed visibility contract.
type EvaluationGenerationConfig struct {
	Seed         *int   `json:"seed"`
	SeedProvided bool   `json:"seed_provided"`
	SeedSupport  string `json:"seed_support"`
}

// EvaluationExperimentSnapshot is the SDK view of the frozen experiment
// manifest. Less frequently consumed sections stay as raw JSON so the wire
// schema can grow without breaking the SDK.
type EvaluationExperimentSnapshot struct {
	SchemaVersion         int                        `json:"schema_version"`
	Dataset               EvaluationDatasetRef       `json:"dataset"`
	SourceKnowledgeBaseID *string                    `json:"source_knowledge_base_id"`
	Models                json.RawMessage            `json:"models"`
	Configuration         EvaluationExperimentConfig `json:"configuration"`
	MetricPlan            json.RawMessage            `json:"metric_plan"`
	Code                  json.RawMessage            `json:"code"`
	Environment           json.RawMessage            `json:"environment"`
	Reproducibility       json.RawMessage            `json:"reproducibility"`
}

// EvaluationExperimentConfig carries the resolved parameter groups; the
// generation group is structured because seed semantics are part of the M3
// contract.
type EvaluationExperimentConfig struct {
	Retrieval  json.RawMessage            `json:"retrieval"`
	Rerank     json.RawMessage            `json:"rerank"`
	Generation EvaluationGenerationConfig `json:"generation"`
}

// EvaluationMetricResult contains retrieval and generation metrics.
type EvaluationMetricResult struct {
	RetrievalMetrics  EvaluationRetrievalMetrics       `json:"retrieval_metrics"`
	GenerationMetrics EvaluationGenerationMetrics      `json:"generation_metrics"`
	Scores            map[string]EvaluationMetricScore `json:"scores,omitempty"`
}

// EvaluationMetricScore is one dynamic registry score.
type EvaluationMetricScore struct {
	Value     *float64 `json:"value"`
	Status    string   `json:"status"`
	ErrorCode string   `json:"error_code,omitempty"`
}

// EvaluationRetrievalMetrics contains retrieval quality metrics.
type EvaluationRetrievalMetrics struct {
	Precision float64 `json:"precision"`
	Recall    float64 `json:"recall"`
	NDCG3     float64 `json:"ndcg3"`
	NDCG10    float64 `json:"ndcg10"`
	MRR       float64 `json:"mrr"`
	MAP       float64 `json:"map"`
}

// EvaluationGenerationMetrics contains answer generation quality metrics.
type EvaluationGenerationMetrics struct {
	BLEU1  float64 `json:"bleu1"`
	BLEU2  float64 `json:"bleu2"`
	BLEU4  float64 `json:"bleu4"`
	ROUGE1 float64 `json:"rouge1"`
	ROUGE2 float64 `json:"rouge2"`
	ROUGEL float64 `json:"rougel"`
}

// EvaluationRequest contains the parameters accepted by the evaluation API.
type EvaluationRequest struct {
	DatasetID       string `json:"dataset_id"`
	KnowledgeBaseID string `json:"knowledge_base_id"`
	ChatModelID     string `json:"chat_id"`
	RerankModelID   string `json:"rerank_id"`

	// DatasetVersionID optionally pins one immutable dataset version.
	DatasetVersionID string `json:"dataset_version_id,omitempty"`
	// Seed distinguishes "not provided" (nil) from an explicit seed=0.
	Seed *int `json:"seed,omitempty"`

	// EmbeddingModelID is retained for source compatibility.
	// Deprecated: use KnowledgeBaseID. A non-empty value returns an explicit error.
	EmbeddingModelID string `json:"-"`
}

// ErrEvaluationEmbeddingModelUnsupported reports use of the deprecated embedding model field.
var ErrEvaluationEmbeddingModelUnsupported = errors.New(
	"evaluation request EmbeddingModelID is unsupported; use KnowledgeBaseID",
)

// IsEvaluationSeedUnsupported reports whether err is the evaluation API's
// 422 rejection of an explicit seed against a provider without seed support.
// The task was not created; choosing a seed-capable provider or dropping the
// seed are the only recoveries.
func IsEvaluationSeedUnsupported(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusUnprocessableEntity
}

// MarshalJSON emits only fields accepted by the evaluation API.
func (r EvaluationRequest) MarshalJSON() ([]byte, error) {
	if r.EmbeddingModelID != "" {
		return nil, ErrEvaluationEmbeddingModelUnsupported
	}

	type wireRequest struct {
		DatasetID        string `json:"dataset_id"`
		KnowledgeBaseID  string `json:"knowledge_base_id"`
		ChatModelID      string `json:"chat_id"`
		RerankModelID    string `json:"rerank_id"`
		DatasetVersionID string `json:"dataset_version_id,omitempty"`
		Seed             *int   `json:"seed,omitempty"`
	}
	return json.Marshal(wireRequest{
		DatasetID:        r.DatasetID,
		KnowledgeBaseID:  r.KnowledgeBaseID,
		ChatModelID:      r.ChatModelID,
		RerankModelID:    r.RerankModelID,
		DatasetVersionID: r.DatasetVersionID,
		Seed:             r.Seed,
	})
}

// EvaluationTaskResponse is the API envelope returned when starting an evaluation.
type EvaluationTaskResponse struct {
	Success bool              `json:"success"`
	Data    *EvaluationResult `json:"data"`
}

// EvaluationResultResponse is the API envelope returned when retrieving an evaluation.
type EvaluationResultResponse struct {
	Success bool              `json:"success"`
	Data    *EvaluationResult `json:"data"`
}

// StartEvaluation starts an evaluation task and returns its nested task state.
func (c *Client) StartEvaluation(ctx context.Context, request *EvaluationRequest) (*EvaluationTask, error) {
	if request == nil {
		return nil, errors.New("evaluation request is required")
	}

	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/evaluation", request, nil)
	if err != nil {
		return nil, err
	}

	var response EvaluationTaskResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}
	if err := validateEvaluationResult(response.Data); err != nil {
		return nil, err
	}

	return response.Data.Task, nil
}

// GetEvaluationResult retrieves the nested details of an evaluation task.
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
	if err := validateEvaluationResult(response.Data); err != nil {
		return nil, err
	}

	return response.Data, nil
}

// DeleteEvaluation soft-deletes one terminal evaluation task. Missing and
// already deleted tasks succeed; the server rejects active tasks with 409.
func (c *Client) DeleteEvaluation(ctx context.Context, taskID string) error {
	if taskID == "" {
		return errors.New("evaluation task ID is required")
	}

	resp, err := c.doRequest(
		ctx,
		http.MethodDelete,
		"/api/v1/evaluation/"+url.PathEscape(taskID),
		nil,
		nil,
	)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// EvaluationListOptions carries the optional list filters: a numeric status,
// a bounded page size, and the opaque keyset cursor from the previous page.
type EvaluationListOptions struct {
	Status           *EvaluationStatus
	DatasetID        string
	DatasetVersionID string
	ModelID          string
	StartedFrom      *time.Time
	StartedTo        *time.Time
	Labels           []string
	PageSize         int
	Cursor           string
}

// EvaluationTaskPage contains one keyset page and the next cursor.
type EvaluationTaskPage struct {
	Items      []*EvaluationTask
	NextCursor string
}

type evaluationListData struct {
	Items      []*EvaluationTask `json:"items"`
	NextCursor string            `json:"next_cursor"`
}

// EvaluationListResponse is the API envelope returned when listing tasks.
type EvaluationListResponse struct {
	Success bool                `json:"success"`
	Data    *evaluationListData `json:"data"`
}

// ListEvaluations returns one keyset page of evaluation tasks ordered by
// (start_time DESC, id DESC). The response must contain the nested data
// object; a missing one is an error instead of a silent empty page.
func (c *Client) ListEvaluations(ctx context.Context, options *EvaluationListOptions) (*EvaluationTaskPage, error) {
	queryParams := url.Values{}
	if options != nil {
		if options.Status != nil {
			queryParams.Add("status", fmt.Sprintf("%d", *options.Status))
		}
		if options.DatasetID != "" {
			queryParams.Add("dataset_id", options.DatasetID)
		}
		if options.DatasetVersionID != "" {
			queryParams.Add("dataset_version_id", options.DatasetVersionID)
		}
		if options.ModelID != "" {
			queryParams.Add("model_id", options.ModelID)
		}
		if options.StartedFrom != nil {
			queryParams.Add("started_from", options.StartedFrom.UTC().Format(time.RFC3339Nano))
		}
		if options.StartedTo != nil {
			queryParams.Add("started_to", options.StartedTo.UTC().Format(time.RFC3339Nano))
		}
		for _, label := range options.Labels {
			queryParams.Add("label", label)
		}
		if options.PageSize > 0 {
			queryParams.Add("page_size", fmt.Sprintf("%d", options.PageSize))
		}
		if options.Cursor != "" {
			queryParams.Add("cursor", options.Cursor)
		}
	}

	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/evaluation/tasks", nil, queryParams)
	if err != nil {
		return nil, err
	}

	var response EvaluationListResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}
	if !response.Success {
		return nil, errors.New("evaluation list response is not successful")
	}
	if response.Data == nil {
		return nil, errors.New("evaluation list response is missing data")
	}
	items := response.Data.Items
	if items == nil {
		items = []*EvaluationTask{}
	}
	return &EvaluationTaskPage{
		Items:      items,
		NextCursor: response.Data.NextCursor,
	}, nil
}

type evaluationLabelsResponse struct {
	Success bool `json:"success"`
	Data    *struct {
		TaskID string   `json:"task_id"`
		Labels []string `json:"labels"`
	} `json:"data"`
}

// ReplaceEvaluationTaskLabels atomically replaces one task's labels.
func (c *Client) ReplaceEvaluationTaskLabels(
	ctx context.Context,
	taskID string,
	labels []string,
) ([]string, error) {
	if taskID == "" {
		return nil, errors.New("evaluation task ID is required")
	}
	resp, err := c.doRequest(
		ctx,
		http.MethodPut,
		"/api/v1/evaluation/tasks/"+url.PathEscape(taskID)+"/labels",
		struct {
			Labels []string `json:"labels"`
		}{Labels: labels},
		nil,
	)
	if err != nil {
		return nil, err
	}
	var response evaluationLabelsResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}
	if !response.Success {
		return nil, errors.New("evaluation label response is not successful")
	}
	if response.Data == nil {
		return nil, errors.New("evaluation label response is missing data")
	}
	if response.Data.Labels == nil {
		response.Data.Labels = []string{}
	}
	return response.Data.Labels, nil
}

// CancelEvaluation requests cancellation of an evaluation task and returns
// its nested state. Requesting cancel twice or on a terminal task returns the
// current task unchanged.
func (c *Client) CancelEvaluation(ctx context.Context, taskID string) (*EvaluationResult, error) {
	if taskID == "" {
		return nil, errors.New("evaluation task ID is required")
	}

	resp, err := c.doRequest(
		ctx,
		http.MethodPost,
		"/api/v1/evaluation/"+url.PathEscape(taskID)+"/cancel",
		nil,
		nil,
	)
	if err != nil {
		return nil, err
	}

	var response EvaluationResultResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}
	if err := validateEvaluationResult(response.Data); err != nil {
		return nil, err
	}

	return response.Data, nil
}

func validateEvaluationResult(result *EvaluationResult) error {
	if result == nil {
		return errors.New("evaluation response is missing data")
	}
	if result.Task == nil {
		return errors.New("evaluation response is missing data.task")
	}
	params := bytes.TrimSpace(result.Params)
	if len(params) == 0 || bytes.Equal(params, []byte("null")) {
		return errors.New("evaluation response is missing data.params")
	}
	return nil
}

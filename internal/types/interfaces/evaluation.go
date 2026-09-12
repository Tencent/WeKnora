package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// EvaluationService defines operations for evaluation tasks
type EvaluationService interface {
	// EvaluationMetricDefinitions lists the versioned metric registry catalog.
	EvaluationMetricDefinitions() []types.EvaluationMetricDefinition
	// Evaluation starts a new evaluation task
	Evaluation(ctx context.Context, datasetID string, knowledgeBaseID string,
		chatModelID string, rerankModelID string,
	) (*types.EvaluationDetail, error)
	// EvaluationWithOptions starts a new evaluation task with the M3 option
	// set: dataset version binding, configuration overrides, and seed.
	EvaluationWithOptions(ctx context.Context, options *types.EvaluationOptions) (*types.EvaluationDetail, error)
	// EvaluationResult retrieves evaluation result by task ID
	EvaluationResult(ctx context.Context, taskID string) (*types.EvaluationDetail, error)
	// CancelEvaluation persists a user cancel request and returns the current
	// task state. The first request time wins; terminal tasks are unchanged.
	CancelEvaluation(ctx context.Context, taskID string) (*types.EvaluationDetail, error)
	// ListEvaluations returns one keyset page of tenant tasks ordered by
	// (start_time DESC, id DESC).
	ListEvaluations(
		ctx context.Context,
		input types.EvaluationTaskListInput,
	) (*types.EvaluationTaskListPage, error)
	// ReplaceEvaluationTaskLabels atomically replaces the normalized labels
	// without changing task execution version or updated_at.
	ReplaceEvaluationTaskLabels(ctx context.Context, taskID string, labels []string) ([]string, error)
	// CompareEvaluations returns a read-only comparison over frozen successful runs.
	CompareEvaluations(
		ctx context.Context,
		request types.EvaluationComparisonRequest,
	) (*types.EvaluationComparisonResponse, error)
	// PrepareEvaluationExport writes a bounded, complete temporary artifact.
	PrepareEvaluationExport(
		ctx context.Context,
		taskID string,
		format string,
	) (*types.EvaluationPreparedExport, error)
	// DeleteEvaluation soft-deletes one terminal task. Missing and already
	// deleted tasks are idempotent successes; active tasks are rejected.
	DeleteEvaluation(ctx context.Context, taskID string) error
}

// Metrics defines interface for computing evaluation metrics
type Metrics interface {
	// Compute calculates metric score based on input data
	Compute(metricInput *types.MetricInput) float64
}

// EvalHook defines interface for evaluation process hooks
type EvalHook interface {
	// Handle processes evaluation state change
	Handle(ctx context.Context, state types.EvalState, index int, data interface{}) error
}

// DatasetService defines operations for dataset management
type DatasetService interface {
	// GetDatasetByID retrieves QA pairs from dataset by ID
	GetDatasetByID(ctx context.Context, datasetID string) ([]*types.QAPair, error)
}

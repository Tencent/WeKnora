package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// EvaluationService defines operations for evaluation tasks
type EvaluationService interface {
	// Evaluation starts a new evaluation task
	Evaluation(ctx context.Context, datasetID string, knowledgeBaseID string,
		chatModelID string, rerankModelID string,
	) (*types.EvaluationDetail, error)
	// EvaluationResult retrieves evaluation result by task ID
	EvaluationResult(ctx context.Context, taskID string) (*types.EvaluationDetail, error)
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

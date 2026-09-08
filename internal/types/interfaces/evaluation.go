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
	EvaluationHistory(ctx context.Context, limit int) ([]*types.EvaluationDetail, error)
	WikiCacheProbe(ctx context.Context, chatModelID, layout string, sample int) (*types.WikiCacheProbeResult, error)
}

// EvaluationRepository persists evaluation snapshots. Every operation is
// tenant scoped; callers cannot select a tenant via request parameters.
type EvaluationRepository interface {
	Create(ctx context.Context, detail *types.EvaluationDetail) error
	Update(ctx context.Context, detail *types.EvaluationDetail) error
	Get(ctx context.Context, tenantID uint64, taskID string) (*types.EvaluationDetail, error)
	List(ctx context.Context, tenantID uint64, limit int) ([]*types.EvaluationDetail, error)
	MarkRunningInterrupted(ctx context.Context) error
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

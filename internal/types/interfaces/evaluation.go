package interfaces

import (
	"context"
	"time"

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
	// EvaluationEvidence returns a deterministic, secret-free proof bundle.
	EvaluationEvidence(ctx context.Context, taskID string) (*types.EvaluationEvidenceReport, error)
	// ModelUsage returns tenant-scoped model-call aggregates from evaluation runs.
	ModelUsage(ctx context.Context, startTime, endTime *time.Time) ([]types.ModelUsageStat, error)
	// EvaluationDatasets lists datasets available to the evaluation UI.
	EvaluationDatasets(ctx context.Context) ([]types.EvaluationDataset, error)
	// EvaluationRuns lists tenant-scoped evaluation history.
	EvaluationRuns(ctx context.Context, limit, offset int) (*types.EvaluationRunPage, error)
	// WikiCacheBenchmark runs a controlled, prompt-free cold/warm Wiki replay.
	WikiCacheBenchmark(ctx context.Context, modelID string) (*types.WikiCacheBenchmarkEvidence, error)
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
	// ListDatasets returns manifest metadata and readiness for each dataset directory.
	ListDatasets(ctx context.Context) ([]types.EvaluationDataset, error)
}

package interfaces

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// EvaluationRunRepository persists the lifecycle and aggregate result of an
// evaluation. Every operation is tenant-scoped at the query boundary.
type EvaluationRunRepository interface {
	Create(ctx context.Context, run *types.EvaluationRun) error
	GetByTaskID(ctx context.Context, tenantID uint64, taskID string) (*types.EvaluationRun, error)
	MarkRunning(ctx context.Context, tenantID uint64, taskID string, startedAt time.Time) error
	UpdateTotal(ctx context.Context, tenantID uint64, taskID string, total int) error
	IncrementFinished(ctx context.Context, tenantID uint64, taskID string) error
	MarkSuccess(
		ctx context.Context, tenantID uint64, taskID string,
		metric *types.MetricResult, finishedAt time.Time,
	) error
	MarkFailed(
		ctx context.Context, tenantID uint64, taskID string,
		metric *types.MetricResult, message string, finishedAt time.Time,
	) error
}

// EvaluationService defines operations for evaluation tasks
type EvaluationService interface {
	// Evaluation starts a new evaluation task
	Evaluation(ctx context.Context, datasetID string, knowledgeBaseID string,
		chatModelID string, rerankModelID string,
	) (*types.EvaluationDetail, error)
	// EvaluationResult retrieves evaluation result by task ID
	EvaluationResult(ctx context.Context, taskID string) (*types.EvaluationDetail, error)
}

// BenchmarkResultService composes the unified internal Benchmark v1.1 result.
// HTTP/API presentation is intentionally outside this contract.
type BenchmarkResultService interface {
	GetBenchmarkResult(ctx context.Context, taskID string) (*types.BenchmarkResult, error)
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
	// GetDatasetByID retrieves the full candidate corpus, QA ground truth, and
	// stable semantic identity for a dataset.
	GetDatasetByID(ctx context.Context, datasetID string) (*types.EvaluationDataset, error)
}

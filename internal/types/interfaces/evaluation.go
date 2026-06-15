package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

type EvaluationRepository interface {
	CreateDataset(context.Context, *types.EvaluationDataset) error
	GetDataset(context.Context, uint64, string) (*types.EvaluationDataset, error)
	ListDatasets(context.Context, uint64, *types.Pagination) (*types.PageResult, error)
	UpdateDataset(context.Context, *types.EvaluationDataset) error
	DeleteDataset(context.Context, uint64, string) error
	CreateSample(context.Context, *types.EvaluationSample) error
	CreateSamples(context.Context, []*types.EvaluationSample) error
	GetSample(context.Context, uint64, string, string) (*types.EvaluationSample, error)
	ListSamples(context.Context, uint64, string, *types.Pagination) (*types.PageResult, error)
	ListAllSamples(context.Context, uint64, string) ([]*types.EvaluationSample, error)
	UpdateSample(context.Context, *types.EvaluationSample) error
	DeleteSample(context.Context, uint64, string, string) error
	CreateRunWithResults(context.Context, *types.EvaluationRun, []*types.EvaluationRunResult) error
	GetRun(context.Context, uint64, string) (*types.EvaluationRun, error)
	ListRuns(context.Context, uint64, *types.Pagination) (*types.PageResult, error)
	UpdateRun(context.Context, *types.EvaluationRun) error
	GetRunResult(context.Context, uint64, string, string) (*types.EvaluationRunResult, error)
	ListRunResults(context.Context, uint64, string, *types.Pagination) (*types.PageResult, error)
	ListAllRunResults(context.Context, uint64, string) ([]*types.EvaluationRunResult, error)
	UpdateRunResult(context.Context, *types.EvaluationRunResult) error
	MarkInterruptedRunsFailed(context.Context, string) error
}

type EvaluationService interface {
	ListMetrics(context.Context) []types.EvaluationMetricDefinition
	CreateDataset(context.Context, *types.CreateEvaluationDatasetRequest) (*types.EvaluationDataset, error)
	GetDataset(context.Context, string) (*types.EvaluationDataset, error)
	ListDatasets(context.Context, *types.Pagination) (*types.PageResult, error)
	UpdateDataset(context.Context, string, *types.UpdateEvaluationDatasetRequest) (*types.EvaluationDataset, error)
	DeleteDataset(context.Context, string) error
	CreateSample(context.Context, string, *types.CreateEvaluationSampleRequest) (*types.EvaluationSample, error)
	ListSamples(context.Context, string, *types.Pagination) (*types.PageResult, error)
	UpdateSample(context.Context, string, string, *types.UpdateEvaluationSampleRequest) (*types.EvaluationSample, error)
	DeleteSample(context.Context, string, string) error
	CreateRun(context.Context, *types.CreateEvaluationRunRequest) (*types.EvaluationRun, error)
	GetRun(context.Context, string) (*types.EvaluationRun, error)
	ListRuns(context.Context, *types.Pagination) (*types.PageResult, error)
	ListRunResults(context.Context, string, *types.Pagination) (*types.PageResult, error)
	CompareRuns(context.Context, string, string) (*types.EvaluationComparison, error)
	ReconcileInterruptedRuns(context.Context) error
	Evaluation(context.Context, string, string, string, string) (*types.EvaluationDetail, error)
	EvaluationResult(context.Context, string) (*types.EvaluationDetail, error)
}

type Metrics interface {
	Compute(*types.MetricInput) float64
}
type EvalHook interface {
	Handle(context.Context, types.EvalState, int, interface{}) error
}
type DatasetService interface {
	GetDatasetByID(context.Context, string) ([]*types.QAPair, error)
}

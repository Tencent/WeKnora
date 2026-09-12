package interfaces

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
)

var (
	// ErrEvaluationDatasetLimitExceeded reports a structured input beyond the
	// configured registry limits; HTTP maps it to 413.
	ErrEvaluationDatasetLimitExceeded = errors.New("evaluation dataset limit exceeded")
	// ErrEvaluationDatasetArtifactMismatch reports a tampered built-in bundle.
	ErrEvaluationDatasetArtifactMismatch = errors.New("evaluation dataset artifact hash mismatch")
)

// EvaluationDatasetRegistryService manages tenant and built-in evaluation datasets.
type EvaluationDatasetRegistryService interface {
	ImportDataset(ctx context.Context, tenantID uint64,
		input *types.EvaluationDatasetImportInput) (*types.EvaluationDatasetImportResult, error)
	CreateDataset(
		ctx context.Context,
		tenantID uint64,
		name string,
		description string,
	) (*types.EvaluationDataset, error)
	CreateVersion(
		ctx context.Context,
		tenantID uint64,
		datasetID string,
		content *types.EvaluationDatasetVersionInput,
	) (*types.EvaluationDatasetVersion, error)
	GetDataset(ctx context.Context, tenantID uint64, datasetID string) (*types.EvaluationDataset, error)
	ListDatasets(ctx context.Context, tenantID uint64) ([]*types.EvaluationDataset, error)
	ListVersions(ctx context.Context, tenantID uint64, datasetID string) ([]*types.EvaluationDatasetVersion, error)
	GetVersion(
		ctx context.Context,
		tenantID uint64,
		datasetVersionID string,
	) (*types.EvaluationDatasetVersion, error)
	GetVersionContent(
		ctx context.Context,
		tenantID uint64,
		datasetVersionID string,
	) (*types.EvaluationDatasetVersionContent, error)
	// RegisterBuiltinDataset imports a built-in bundle through the controlled
	// server-side registration flow. The bundle is identified by its expected
	// artifact SHA-256; a mismatch fails the registration.
	RegisterBuiltinDataset(
		ctx context.Context,
		registration *types.EvaluationBuiltinDatasetRegistration,
	) (*types.EvaluationDatasetVersion, error)
}

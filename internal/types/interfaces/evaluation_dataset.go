package interfaces

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
)

var (
	// ErrEvaluationDatasetNotFound hides whether a dataset belongs to another tenant.
	ErrEvaluationDatasetNotFound = errors.New("evaluation dataset not found")
	// ErrEvaluationDatasetVersionNotFound hides whether a version belongs to another tenant or dataset.
	ErrEvaluationDatasetVersionNotFound = errors.New("evaluation dataset version not found")
	// ErrEvaluationDatasetAlreadyExists reports a duplicate dataset identity.
	ErrEvaluationDatasetAlreadyExists = errors.New("evaluation dataset already exists")
	// ErrEvaluationDatasetVersionConflict reports a duplicate version_number or content hash.
	ErrEvaluationDatasetVersionConflict = errors.New("evaluation dataset version conflict")
	// ErrEvaluationDatasetInvalid reports inconsistent references, counts, or identifiers.
	ErrEvaluationDatasetInvalid = errors.New("evaluation dataset invalid")
	// ErrEvaluationDatasetImmutable reports an attempted mutation of a frozen version.
	ErrEvaluationDatasetImmutable = errors.New("evaluation dataset version immutable")
)

// EvaluationDatasetRepository persists the evaluation dataset registry.
// All read methods take an explicit tenant boundary: system datasets are
// visible to every tenant, tenant datasets only to their owner; cross-tenant
// and missing objects share the same NotFound sentinel.
type EvaluationDatasetRepository interface {
	// ImportDataset commits the identity and first version together. Conflicting
	// identities replay only when the immutable import manifest matches.
	ImportDataset(ctx context.Context, dataset *types.EvaluationDataset, version *types.EvaluationDatasetVersion,
		content *types.EvaluationDatasetVersionInput) (*types.EvaluationDatasetImportResult, error)
	CreateDataset(ctx context.Context, dataset *types.EvaluationDataset) error
	GetDataset(ctx context.Context, tenantID uint64, datasetID string) (*types.EvaluationDataset, error)
	ListDatasets(ctx context.Context, tenantID uint64) ([]*types.EvaluationDataset, error)

	// CreateVersion atomically validates and persists one immutable version
	// with all passages, questions, and relevance rows in a single transaction.
	CreateVersion(
		ctx context.Context,
		version *types.EvaluationDatasetVersion,
		content *types.EvaluationDatasetVersionInput,
	) error
	GetVersion(ctx context.Context, tenantID uint64, datasetVersionID string) (*types.EvaluationDatasetVersion, error)
	ListVersions(ctx context.Context, tenantID uint64, datasetID string) ([]*types.EvaluationDatasetVersion, error)
	// GetVersionContent returns the full ordered content of one version:
	// passages by PID, questions by sample_index, relevance by QID/PID.
	GetVersionContent(
		ctx context.Context,
		tenantID uint64,
		datasetVersionID string,
	) (*types.EvaluationDatasetVersionContent, error)
}

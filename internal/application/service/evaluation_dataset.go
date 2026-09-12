package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

// EvaluationDatasetRegistryService implements the dataset registry use cases:
// tenant dataset creation, immutable version creation with canonical hashes,
// and the controlled built-in bundle registration flow.
type EvaluationDatasetRegistryService struct {
	repo   interfaces.EvaluationDatasetRepository
	limits config.EvaluationDatasetLimits
}

// NewEvaluationDatasetRegistryService constructs the registry service.
func NewEvaluationDatasetRegistryService(
	cfg *config.Config,
	repo interfaces.EvaluationDatasetRepository,
) interfaces.EvaluationDatasetRegistryService {
	return &EvaluationDatasetRegistryService{
		repo:   repo,
		limits: config.EvaluationDatasetLimitsOrDefault(cfg),
	}
}

// CreateDataset registers one tenant-scoped dataset identity.
func (s *EvaluationDatasetRegistryService) CreateDataset(
	ctx context.Context,
	tenantID uint64,
	name string,
	description string,
) (*types.EvaluationDataset, error) {
	if tenantID == 0 {
		return nil, fmt.Errorf("create evaluation dataset: tenant is required: %w",
			interfaces.ErrEvaluationDatasetInvalid)
	}
	if name == "" {
		return nil, fmt.Errorf("create evaluation dataset: name is required: %w",
			interfaces.ErrEvaluationDatasetInvalid)
	}
	dataset := &types.EvaluationDataset{
		ID:            uuid.New().String(),
		Scope:         types.EvaluationDatasetScopeTenant,
		OwnerTenantID: &tenantID,
		Name:          name,
		Description:   description,
	}
	if err := s.repo.CreateDataset(ctx, dataset); err != nil {
		return nil, err
	}
	logger.Infof(ctx, "Created evaluation dataset %s for tenant %d", dataset.ID, tenantID)
	return dataset, nil
}

// CreateVersion freezes one immutable version from structured input. Limits
// are validated before the transactional write; unknown datasets fail
// explicitly and never fall back to a default dataset.
func (s *EvaluationDatasetRegistryService) CreateVersion(
	ctx context.Context,
	tenantID uint64,
	datasetID string,
	content *types.EvaluationDatasetVersionInput,
) (*types.EvaluationDatasetVersion, error) {
	if content == nil {
		return nil, fmt.Errorf("create evaluation dataset version: content is required: %w",
			interfaces.ErrEvaluationDatasetInvalid)
	}
	dataset, err := s.repo.GetDataset(ctx, tenantID, datasetID)
	if err != nil {
		return nil, err
	}
	// Tenant APIs may only extend their own tenant datasets; system datasets
	// are versioned exclusively through the controlled registration flow.
	if dataset.Scope != types.EvaluationDatasetScopeTenant {
		return nil, fmt.Errorf("create evaluation dataset version %s: %w",
			datasetID, interfaces.ErrEvaluationDatasetNotFound)
	}
	if err := s.validateLimits(content); err != nil {
		return nil, err
	}
	if err := validateEvaluationDatasetFields(content); err != nil {
		return nil, err
	}

	versions, err := s.repo.ListVersions(ctx, tenantID, datasetID)
	if err != nil {
		return nil, err
	}
	nextNumber := 1
	for _, existing := range versions {
		if existing.VersionNumber >= nextNumber {
			nextNumber = existing.VersionNumber + 1
		}
	}

	version := &types.EvaluationDatasetVersion{
		ID:             uuid.New().String(),
		DatasetID:      datasetID,
		VersionNumber:  nextNumber,
		SchemaVersion:  types.EvaluationDatasetSchemaVersion,
		ArtifactSHA256: types.EvaluationDatasetArtifactSHA256(canonicalEvaluationDatasetPayload(content)),
		ContentSHA256:  types.CanonicalEvaluationDatasetContentSHA256(content),
		Manifest:       types.JSON(`{"source":"api"}`),
		PassageCount:   len(content.Passages),
		QuestionCount:  len(content.Questions),
		RelevanceCount: len(content.Relevance),
	}
	if err := s.repo.CreateVersion(ctx, version, content); err != nil {
		return nil, err
	}
	logger.Infof(ctx, "Created evaluation dataset version %s (dataset %s, number %d, content %s)",
		version.ID, datasetID, version.VersionNumber, version.ContentSHA256[:12])
	return version, nil
}

// GetDataset returns one dataset visible to the tenant.
func (s *EvaluationDatasetRegistryService) GetDataset(
	ctx context.Context,
	tenantID uint64,
	datasetID string,
) (*types.EvaluationDataset, error) {
	return s.repo.GetDataset(ctx, tenantID, datasetID)
}

// ListDatasets returns every dataset visible to the tenant.
func (s *EvaluationDatasetRegistryService) ListDatasets(
	ctx context.Context,
	tenantID uint64,
) ([]*types.EvaluationDataset, error) {
	return s.repo.ListDatasets(ctx, tenantID)
}

// ListVersions returns all versions of one visible dataset.
func (s *EvaluationDatasetRegistryService) ListVersions(
	ctx context.Context,
	tenantID uint64,
	datasetID string,
) ([]*types.EvaluationDatasetVersion, error) {
	return s.repo.ListVersions(ctx, tenantID, datasetID)
}

// GetVersion returns one visible immutable version; unknown versions fail
// explicitly and never fall back to a default dataset.
func (s *EvaluationDatasetRegistryService) GetVersion(
	ctx context.Context,
	tenantID uint64,
	datasetVersionID string,
) (*types.EvaluationDatasetVersion, error) {
	return s.repo.GetVersion(ctx, tenantID, datasetVersionID)
}

// GetVersionContent returns the full ordered content of one visible version.
func (s *EvaluationDatasetRegistryService) GetVersionContent(
	ctx context.Context,
	tenantID uint64,
	datasetVersionID string,
) (*types.EvaluationDatasetVersionContent, error) {
	return s.repo.GetVersionContent(ctx, tenantID, datasetVersionID)
}

// RegisterBuiltinDataset imports a built-in bundle through the controlled
// server-side flow. The bundle artifact hash must match the pinned
// expectation; a tampered bundle fails before any write.
func (s *EvaluationDatasetRegistryService) RegisterBuiltinDataset(
	ctx context.Context,
	registration *types.EvaluationBuiltinDatasetRegistration,
) (*types.EvaluationDatasetVersion, error) {
	if registration == nil || registration.Content == nil || registration.DatasetID == "" {
		return nil, fmt.Errorf(
			"register builtin evaluation dataset: registration, dataset id and content are required: %w",
			interfaces.ErrEvaluationDatasetInvalid)
	}
	if len(registration.ExpectedArtifactSHA256) != 64 {
		return nil, fmt.Errorf("register builtin evaluation dataset %s: expected artifact SHA-256 is required: %w",
			registration.DatasetID, interfaces.ErrEvaluationDatasetInvalid)
	}
	artifactHash := types.EvaluationDatasetArtifactSHA256(registration.ArtifactBytes)
	if artifactHash != registration.ExpectedArtifactSHA256 {
		return nil, fmt.Errorf("register builtin evaluation dataset %s: artifact %s != pinned %s: %w",
			registration.DatasetID, artifactHash, registration.ExpectedArtifactSHA256,
			interfaces.ErrEvaluationDatasetArtifactMismatch)
	}
	if err := s.validateLimits(registration.Content); err != nil {
		return nil, err
	}

	dataset, err := s.repo.GetDataset(ctx, 0, registration.DatasetID)
	if err != nil {
		if err != interfaces.ErrEvaluationDatasetNotFound {
			return nil, err
		}
		dataset = &types.EvaluationDataset{
			ID:          registration.DatasetID,
			Scope:       types.EvaluationDatasetScopeSystem,
			Name:        registration.Name,
			Description: registration.Description,
		}
		if err := s.repo.CreateDataset(ctx, dataset); err != nil {
			return nil, err
		}
	}
	if dataset.Scope != types.EvaluationDatasetScopeSystem {
		return nil, fmt.Errorf("register builtin evaluation dataset %s: dataset is not system scoped: %w",
			registration.DatasetID, interfaces.ErrEvaluationDatasetInvalid)
	}

	// Built-in bundles register exactly one immutable version per content;
	// re-registration of identical content returns the existing version.
	contentHash := types.CanonicalEvaluationDatasetContentSHA256(registration.Content)
	// System datasets are visible to every tenant; any tenant id resolves them.
	versions, err := s.repo.ListVersions(ctx, 0, registration.DatasetID)
	if err != nil {
		return nil, err
	}
	for _, existing := range versions {
		if existing.ContentSHA256 == contentHash {
			return existing, nil
		}
	}
	nextNumber := 1
	for _, existing := range versions {
		if existing.VersionNumber >= nextNumber {
			nextNumber = existing.VersionNumber + 1
		}
	}

	manifest := registration.Manifest
	if len(manifest) == 0 {
		manifest = types.JSON(`{"source":"builtin"}`)
	}
	version := &types.EvaluationDatasetVersion{
		ID:             uuid.New().String(),
		DatasetID:      registration.DatasetID,
		VersionNumber:  nextNumber,
		SchemaVersion:  types.EvaluationDatasetSchemaVersion,
		ArtifactSHA256: artifactHash,
		ContentSHA256:  contentHash,
		Manifest:       manifest,
		PassageCount:   len(registration.Content.Passages),
		QuestionCount:  len(registration.Content.Questions),
		RelevanceCount: len(registration.Content.Relevance),
	}
	if err := s.repo.CreateVersion(ctx, version, registration.Content); err != nil {
		return nil, err
	}
	logger.Infof(ctx, "Registered builtin evaluation dataset %s version %s (content %s)",
		registration.DatasetID, version.ID, contentHash[:12])
	return version, nil
}

func (s *EvaluationDatasetRegistryService) validateLimits(content *types.EvaluationDatasetVersionInput) error {
	if len(content.Passages) > s.limits.MaxPassages {
		return fmt.Errorf("passages %d exceed limit %d: %w",
			len(content.Passages), s.limits.MaxPassages, interfaces.ErrEvaluationDatasetLimitExceeded)
	}
	if len(content.Questions) > s.limits.MaxQuestions {
		return fmt.Errorf("questions %d exceed limit %d: %w",
			len(content.Questions), s.limits.MaxQuestions, interfaces.ErrEvaluationDatasetLimitExceeded)
	}
	if len(content.Relevance) > s.limits.MaxRelevance {
		return fmt.Errorf("relevance %d exceed limit %d: %w",
			len(content.Relevance), s.limits.MaxRelevance, interfaces.ErrEvaluationDatasetLimitExceeded)
	}
	for _, passage := range content.Passages {
		if len(passage.Metadata) > s.limits.MaxPassageBytes {
			return fmt.Errorf("passage %q metadata exceeds max_passage_bytes: %w",
				passage.PID, interfaces.ErrEvaluationDatasetLimitExceeded)
		}
		if len(passage.Content) > s.limits.MaxPassageBytes {
			return fmt.Errorf("passage %q content %d bytes exceed limit %d: %w",
				passage.PID, len(passage.Content), s.limits.MaxPassageBytes,
				interfaces.ErrEvaluationDatasetLimitExceeded)
		}
	}
	for _, question := range content.Questions {
		if len(question.Question) > s.limits.MaxQuestionBytes {
			return fmt.Errorf("question %q %d bytes exceed limit %d: %w",
				question.QID, len(question.Question), s.limits.MaxQuestionBytes,
				interfaces.ErrEvaluationDatasetLimitExceeded)
		}
		if len(question.Answer) > s.limits.MaxPassageBytes {
			return fmt.Errorf("question %q reference answer %d bytes exceed limit %d: %w",
				question.QID, len(question.Answer), s.limits.MaxPassageBytes,
				interfaces.ErrEvaluationDatasetLimitExceeded)
		}
	}
	return nil
}

// canonicalEvaluationDatasetPayload encodes the structured input with fixed
// field order and HTML escaping disabled; the artifact hash covers exactly
// these bytes for API-created versions.
func canonicalEvaluationDatasetPayload(content *types.EvaluationDatasetVersionInput) []byte {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(content); err != nil {
		panic("evaluation dataset canonical payload: " + err.Error())
	}
	return buffer.Bytes()
}

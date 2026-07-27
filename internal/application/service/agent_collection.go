package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type agentCollectionService struct {
	repo interfaces.AgentCollectionRepository
}

func NewAgentCollectionService(repo interfaces.AgentCollectionRepository) interfaces.AgentCollectionService {
	return &agentCollectionService{repo: repo}
}

func (s *agentCollectionService) Prepare(
	ctx context.Context,
	input types.PrepareCollectionInput,
) (*types.PreparedCollection, error) {
	config, err := normalizedCollectionConfig(input.Config)
	if err != nil {
		return nil, err
	}
	if !config.CollectionEnabled {
		return &types.PreparedCollection{}, nil
	}
	input.Config = config
	profile, err := s.repo.GetProfile(ctx, input.TenantID, input.AgentID, input.UserID)
	if errors.Is(err, repository.ErrAgentCollectionProfileNotFound) {
		profile, err = s.repo.ApplyChanges(ctx, collectionApplyInput(input, nil, 0, 0))
	}
	if err != nil {
		return nil, err
	}
	return s.reconcile(ctx, input, profile)
}

func (s *agentCollectionService) ApplyStructuredAnswer(
	ctx context.Context,
	input types.StructuredCollectionAnswerInput,
) (*types.AgentCollectionProfile, error) {
	prepared, err := s.Prepare(ctx, input.PrepareCollectionInput)
	if err != nil {
		return nil, err
	}
	if input.SchemaVersion != input.Config.CollectionSchemaVersion {
		return nil, fmt.Errorf("collection schema version changed")
	}
	field, ok := collectionFieldByKey(prepared.VisibleFields, input.FieldKey)
	if !ok {
		return nil, fmt.Errorf("collection field %q is not currently visible", input.FieldKey)
	}
	if err := types.ValidateCollectionValue(field, input.Value); err != nil {
		return nil, fmt.Errorf("collection field %q: %w", input.FieldKey, err)
	}
	now := time.Now().UTC()
	change := types.AgentCollectionValueChange{
		FieldKey: input.FieldKey, Value: input.Value, Source: types.CollectionSourceStructuredAnswer,
		Confidence: collectionConfidence(1), SourceMessageID: input.SourceMessageID, SourceMessageAt: &now,
	}
	if _, err := s.repo.ApplyChanges(ctx, collectionApplyInput(
		input.PrepareCollectionInput, []types.AgentCollectionValueChange{change},
		prepared.Profile.RequiredTotal, prepared.Profile.CompletedRequired,
	)); err != nil {
		return nil, err
	}
	return s.preparedProfile(ctx, input.PrepareCollectionInput)
}

func (s *agentCollectionService) ApplyExtractedValues(
	ctx context.Context,
	input types.ExtractedCollectionValuesInput,
) (*types.AgentCollectionProfile, error) {
	prepared, err := s.Prepare(ctx, input.PrepareCollectionInput)
	if err != nil {
		return nil, err
	}
	if !input.Config.CollectionExtractFromMessages {
		return prepared.Profile, nil
	}
	fields := enabledCollectionFields(input.Config.CollectionFields)
	changes := make([]types.AgentCollectionValueChange, 0, len(input.Values))
	for _, extracted := range input.Values {
		field, ok := fields[extracted.FieldKey]
		if !ok || extracted.Confidence < input.Config.CollectionExtractionThreshold {
			continue
		}
		if err := types.ValidateCollectionValue(field, extracted.Value); err != nil {
			continue
		}
		changes = append(changes, types.AgentCollectionValueChange{
			FieldKey: extracted.FieldKey, Value: extracted.Value, Source: types.CollectionSourceMessageExtraction,
			Confidence: collectionConfidence(extracted.Confidence), SourceMessageID: input.SourceMessageID,
			SourceMessageAt: input.SourceMessageAt,
		})
	}
	if len(changes) == 0 {
		return prepared.Profile, nil
	}
	if _, err := s.repo.ApplyChanges(ctx, collectionApplyInput(
		input.PrepareCollectionInput, changes, prepared.Profile.RequiredTotal, prepared.Profile.CompletedRequired,
	)); err != nil {
		return nil, err
	}
	return s.preparedProfile(ctx, input.PrepareCollectionInput)
}

func (s *agentCollectionService) UpdateAsSystemAdmin(
	ctx context.Context,
	input types.SystemAdminCollectionUpdateInput,
) (*types.AgentCollectionProfile, error) {
	if strings.TrimSpace(input.ActorUserID) == "" || strings.TrimSpace(input.ChangeReason) == "" {
		return nil, fmt.Errorf("admin actor and change reason are required")
	}
	config, err := normalizedCollectionConfig(input.Config)
	if err != nil {
		return nil, err
	}
	field, ok := collectionFieldByKey(config.CollectionFields, input.FieldKey)
	if !ok || !field.Enabled {
		return nil, fmt.Errorf("collection field %q is not enabled", input.FieldKey)
	}
	if err := types.ValidateCollectionValue(field, input.Value); err != nil {
		return nil, err
	}
	profile, err := s.repo.GetProfileByID(ctx, input.ProfileID)
	if err != nil {
		return nil, err
	}
	change := types.AgentCollectionValueChange{
		FieldKey: input.FieldKey, Value: input.Value, Source: types.CollectionSourceSystemAdmin,
		Confidence: collectionConfidence(1), ActorUserID: input.ActorUserID, ChangeReason: input.ChangeReason,
	}
	prepareInput := types.PrepareCollectionInput{
		TenantID: profile.TenantID, AgentTenantID: profile.AgentTenantID,
		AgentID: profile.AgentID, UserID: profile.UserID, Config: config,
	}
	if _, err := s.repo.ApplyChanges(ctx, collectionApplyInput(
		prepareInput, []types.AgentCollectionValueChange{change}, profile.RequiredTotal, profile.CompletedRequired,
	)); err != nil {
		return nil, err
	}
	return s.preparedProfile(ctx, prepareInput)
}

func (s *agentCollectionService) preparedProfile(
	ctx context.Context,
	input types.PrepareCollectionInput,
) (*types.AgentCollectionProfile, error) {
	prepared, err := s.Prepare(ctx, input)
	if err != nil {
		return nil, err
	}
	return prepared.Profile, nil
}

func (s *agentCollectionService) ListProfiles(
	ctx context.Context,
	filter types.AgentCollectionProfileFilter,
) (*types.AgentCollectionProfilePage, error) {
	return s.repo.ListProfiles(ctx, filter)
}

func (s *agentCollectionService) ListProfilesForExport(
	ctx context.Context,
	filter types.AgentCollectionProfileFilter,
	limit int,
) ([]*types.AgentCollectionProfile, error) {
	return s.repo.ListProfilesForExport(ctx, filter, limit)
}

func (s *agentCollectionService) SummarizeProfiles(
	ctx context.Context,
	filter types.AgentCollectionProfileFilter,
) (*types.AgentCollectionSummary, error) {
	return s.repo.SummarizeProfiles(ctx, filter)
}

func (s *agentCollectionService) GetProfileByID(
	ctx context.Context,
	profileID string,
) (*types.AgentCollectionProfile, error) {
	return s.repo.GetProfileByID(ctx, profileID)
}

func (s *agentCollectionService) ListHistory(
	ctx context.Context,
	profileID string,
	page, pageSize int,
) (*types.AgentCollectionHistoryPage, error) {
	return s.repo.ListHistory(ctx, profileID, page, pageSize)
}

func (s *agentCollectionService) PurgeProfile(ctx context.Context, profileID string) error {
	return s.repo.PurgeProfile(ctx, profileID)
}

func (s *agentCollectionService) CreateExport(ctx context.Context, export *types.AgentCollectionExport) error {
	return s.repo.CreateExport(ctx, export)
}

func (s *agentCollectionService) UpdateExport(ctx context.Context, export *types.AgentCollectionExport) error {
	return s.repo.UpdateExport(ctx, export)
}

func (s *agentCollectionService) GetExport(
	ctx context.Context,
	exportID string,
) (*types.AgentCollectionExport, error) {
	return s.repo.GetExport(ctx, exportID)
}

func normalizedCollectionConfig(config types.CustomAgentConfig) (types.CustomAgentConfig, error) {
	types.NormalizeAgentCollectionConfig(&config)
	if err := types.ValidateAgentCollectionConfig(config); err != nil {
		return config, err
	}
	return config, nil
}

func collectionConfidence(value float64) *float64 { return &value }

func collectionFieldByKey(fields []types.AgentCollectionField, key string) (types.AgentCollectionField, bool) {
	for _, field := range fields {
		if field.Key == key {
			return field, true
		}
	}
	return types.AgentCollectionField{}, false
}

func enabledCollectionFields(fields []types.AgentCollectionField) map[string]types.AgentCollectionField {
	result := make(map[string]types.AgentCollectionField, len(fields))
	for _, field := range fields {
		if field.Enabled {
			result[field.Key] = field
		}
	}
	return result
}

package service

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/application/repository"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	metadataNameMaxLength        = 128
	metadataDescriptionMaxLength = 2000
	metadataOptionLabelMaxLength = 128
)

var reservedMetadataNames = map[string]struct{}{
	"title":      {},
	"source":     {},
	"created_at": {},
	"updated_at": {},
}

type metadataService struct {
	repo              interfaces.KnowledgeMetadataRepository
	knowledgeRepo     interfaces.KnowledgeRepository
	knowledgeBaseRepo interfaces.KnowledgeBaseRepository
}

var _ interfaces.KnowledgeMetadataService = (*metadataService)(nil)

func NewKnowledgeMetadataService(
	repo interfaces.KnowledgeMetadataRepository,
	knowledgeRepo interfaces.KnowledgeRepository,
	knowledgeBaseRepo interfaces.KnowledgeBaseRepository,
) *metadataService {
	return &metadataService{
		repo: repo, knowledgeRepo: knowledgeRepo, knowledgeBaseRepo: knowledgeBaseRepo,
	}
}

func (s *metadataService) ReadSchema(ctx context.Context, knowledgeBaseID string) (*types.MetadataSchema, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		return nil, apperrors.NewUnauthorizedError("tenant ID not found in context")
	}
	knowledgeBaseID = strings.TrimSpace(knowledgeBaseID)
	if knowledgeBaseID == "" {
		return nil, apperrors.NewBadRequestError("knowledge base ID is required")
	}
	if err := s.ensureDocumentKnowledgeBase(ctx, tenantID, knowledgeBaseID); err != nil {
		return nil, err
	}

	definitions, err := s.repo.ListDefinitions(ctx, tenantID, knowledgeBaseID, false)
	if err != nil {
		return nil, err
	}
	for _, definition := range definitions {
		definition.TypeLocked, err = s.repo.DefinitionHasUsage(
			ctx,
			tenantID,
			knowledgeBaseID,
			definition.ID,
		)
		if err != nil {
			return nil, err
		}
	}
	return &types.MetadataSchema{
		KnowledgeBaseID: knowledgeBaseID,
		Definitions:     definitions,
	}, nil
}

func (s *metadataService) ConfigureDefinition(
	ctx context.Context,
	command types.ConfigureMetadataDefinition,
) (*types.MetadataDefinition, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		return nil, apperrors.NewUnauthorizedError("tenant ID not found in context")
	}
	if command.TenantID != 0 && command.TenantID != tenantID {
		return nil, apperrors.NewForbiddenError("metadata tenant does not match request tenant")
	}

	definition, err := definitionFromCommand(tenantID, command)
	if err != nil {
		return nil, err
	}
	if err := s.ensureDocumentKnowledgeBase(ctx, tenantID, definition.KnowledgeBaseID); err != nil {
		return nil, err
	}
	if command.DefinitionID == "" {
		for _, option := range definition.Options {
			if option.ID != "" {
				return nil, apperrors.NewBadRequestError(
					"metadata option ID is only accepted when updating a definition",
				)
			}
		}
	}
	definitions, err := s.repo.ListDefinitions(ctx, tenantID, definition.KnowledgeBaseID, true)
	if err != nil {
		return nil, err
	}
	for _, existing := range definitions {
		if existing.NormalizedName == definition.NormalizedName && existing.ID != command.DefinitionID {
			return nil, apperrors.NewConflictError("metadata definition name already exists")
		}
	}
	if command.DefinitionID != "" {
		existing, err := s.repo.GetDefinition(
			ctx,
			tenantID,
			definition.KnowledgeBaseID,
			command.DefinitionID,
		)
		if err != nil {
			return nil, err
		}
		if existing.ValueType != definition.ValueType {
			inUse, err := s.repo.DefinitionHasUsage(
				ctx,
				tenantID,
				definition.KnowledgeBaseID,
				command.DefinitionID,
			)
			if err != nil {
				return nil, err
			}
			if inUse {
				return nil, apperrors.NewBadRequestError(
					"metadata value type cannot change after values or rules exist",
				)
			}
		}
		definition.ID = existing.ID
		definition.Status = existing.Status
		if err := s.repo.UpdateDefinition(ctx, definition); err != nil {
			return nil, err
		}
		configured, err := s.repo.GetDefinition(ctx, tenantID, definition.KnowledgeBaseID, definition.ID)
		if err != nil {
			return nil, err
		}
		configured.TypeLocked, err = s.repo.DefinitionHasUsage(
			ctx,
			tenantID,
			definition.KnowledgeBaseID,
			definition.ID,
		)
		return configured, err
	}

	if err := s.repo.CreateDefinition(ctx, definition); err != nil {
		return nil, err
	}
	return s.repo.GetDefinition(ctx, tenantID, definition.KnowledgeBaseID, definition.ID)
}

func (s *metadataService) ArchiveDefinition(ctx context.Context, knowledgeBaseID, definitionID string) error {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		return apperrors.NewUnauthorizedError("tenant ID not found in context")
	}
	if err := s.ensureDocumentKnowledgeBase(ctx, tenantID, knowledgeBaseID); err != nil {
		return err
	}
	if _, err := s.repo.GetDefinition(ctx, tenantID, knowledgeBaseID, definitionID); err != nil {
		return err
	}
	return s.repo.ArchiveDefinition(ctx, tenantID, knowledgeBaseID, definitionID)
}

func (s *metadataService) ConfigureAutoRule(
	ctx context.Context,
	command types.ConfigureMetadataAutoRule,
) (*types.MetadataAutoRule, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		return nil, apperrors.NewUnauthorizedError("tenant ID not found in context")
	}
	if command.TenantID != 0 && command.TenantID != tenantID {
		return nil, apperrors.NewForbiddenError("metadata tenant does not match request tenant")
	}
	knowledgeBaseID := strings.TrimSpace(command.KnowledgeBaseID)
	definitionID := strings.TrimSpace(command.DefinitionID)
	if knowledgeBaseID == "" || definitionID == "" {
		return nil, apperrors.NewBadRequestError(
			"knowledge base ID and metadata definition ID are required",
		)
	}
	if err := s.ensureDocumentKnowledgeBase(ctx, tenantID, knowledgeBaseID); err != nil {
		return nil, err
	}
	if _, err := s.repo.GetDefinition(ctx, tenantID, knowledgeBaseID, definitionID); err != nil {
		return nil, err
	}
	config, err := validateMetadataAutoRule(command.Strategy, command.Config)
	if err != nil {
		return nil, err
	}

	rule, err := s.repo.GetAutoRule(ctx, tenantID, knowledgeBaseID, definitionID, false)
	if err != nil && !errors.Is(err, repository.ErrMetadataAutoRuleNotFound) {
		return nil, err
	}
	if errors.Is(err, repository.ErrMetadataAutoRuleNotFound) {
		rule = &types.MetadataAutoRule{
			TenantID:             tenantID,
			KnowledgeBaseID:      knowledgeBaseID,
			MetadataDefinitionID: definitionID,
			Revision:             1,
		}
	} else {
		rule.Revision++
	}
	rule.Strategy = command.Strategy
	rule.Config = config
	rule.Enabled = true
	if err := s.repo.SaveAutoRule(ctx, rule); err != nil {
		return nil, err
	}
	return s.repo.GetAutoRule(ctx, tenantID, knowledgeBaseID, definitionID, true)
}

func (s *metadataService) DeleteAutoRule(ctx context.Context, knowledgeBaseID, definitionID string) error {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		return apperrors.NewUnauthorizedError("tenant ID not found in context")
	}
	if err := s.ensureDocumentKnowledgeBase(ctx, tenantID, knowledgeBaseID); err != nil {
		return err
	}
	rule, err := s.repo.GetAutoRule(ctx, tenantID, knowledgeBaseID, definitionID, true)
	if errors.Is(err, repository.ErrMetadataAutoRuleNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	rule.Revision++
	rule.Enabled = false
	return s.repo.SaveAutoRule(ctx, rule)
}

func (s *metadataService) ReadDocumentMetadata(
	ctx context.Context,
	knowledgeIDs []string,
) ([]*types.DocumentMetadata, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		return nil, apperrors.NewUnauthorizedError("tenant ID not found in context")
	}
	if len(knowledgeIDs) == 0 {
		return []*types.DocumentMetadata{}, nil
	}
	knowledges, err := s.knowledgeRepo.GetKnowledgeBatch(ctx, tenantID, knowledgeIDs)
	if err != nil {
		return nil, err
	}
	knowledgeByID := make(map[string]*types.Knowledge, len(knowledges))
	knowledgeIDsByKB := make(map[string][]string)
	for _, knowledge := range knowledges {
		if knowledge == nil {
			continue
		}
		knowledgeByID[knowledge.ID] = knowledge
		knowledgeIDsByKB[knowledge.KnowledgeBaseID] = append(
			knowledgeIDsByKB[knowledge.KnowledgeBaseID],
			knowledge.ID,
		)
	}

	metadataByKnowledgeID := make(map[string]*types.DocumentMetadata, len(knowledges))
	for knowledgeBaseID, ids := range knowledgeIDsByKB {
		if err := s.ensureDocumentKnowledgeBase(ctx, tenantID, knowledgeBaseID); err != nil {
			return nil, err
		}
		definitions, err := s.repo.ListDefinitions(ctx, tenantID, knowledgeBaseID, true)
		if err != nil {
			return nil, err
		}
		values, err := s.repo.ListDocumentValues(ctx, tenantID, knowledgeBaseID, ids)
		if err != nil {
			return nil, err
		}
		valuesByKnowledge := make(map[string]map[string]*types.MetadataValue, len(ids))
		for _, value := range values {
			if valuesByKnowledge[value.KnowledgeID] == nil {
				valuesByKnowledge[value.KnowledgeID] = make(map[string]*types.MetadataValue)
			}
			valuesByKnowledge[value.KnowledgeID][value.MetadataDefinitionID] = value
		}
		for _, knowledgeID := range ids {
			metadataByKnowledgeID[knowledgeID] = buildDocumentMetadata(
				knowledgeID,
				definitions,
				valuesByKnowledge[knowledgeID],
			)
		}
	}

	result := make([]*types.DocumentMetadata, 0, len(knowledgeByID))
	for _, knowledgeID := range knowledgeIDs {
		if metadata := metadataByKnowledgeID[knowledgeID]; metadata != nil {
			result = append(result, metadata)
		}
	}
	return result, nil
}

func (s *metadataService) ValidateDocumentMetadataChanges(
	ctx context.Context,
	knowledgeBaseID string,
	changes []types.MetadataValueChange,
) error {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		return apperrors.NewUnauthorizedError("tenant ID not found in context")
	}
	knowledgeBaseID = strings.TrimSpace(knowledgeBaseID)
	if knowledgeBaseID == "" {
		return apperrors.NewBadRequestError("knowledge base ID is required")
	}
	if err := s.ensureDocumentKnowledgeBase(ctx, tenantID, knowledgeBaseID); err != nil {
		return err
	}
	for _, change := range changes {
		if _, _, err := validateMetadataValueChange(ctx, s.repo, tenantID, knowledgeBaseID, change); err != nil {
			return err
		}
	}
	return nil
}

func (s *metadataService) ChangeDocumentMetadata(
	ctx context.Context,
	command types.ChangeDocumentMetadata,
) (*types.DocumentMetadata, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		return nil, apperrors.NewUnauthorizedError("tenant ID not found in context")
	}
	if command.TenantID != 0 && command.TenantID != tenantID {
		return nil, apperrors.NewForbiddenError("metadata tenant does not match request tenant")
	}
	knowledge, err := s.knowledgeRepo.GetKnowledgeByID(ctx, tenantID, command.KnowledgeID)
	if err != nil {
		return nil, err
	}
	if err := s.ensureDocumentKnowledgeBase(ctx, tenantID, knowledge.KnowledgeBaseID); err != nil {
		return nil, err
	}
	if len(command.Changes) == 0 {
		return nil, apperrors.NewBadRequestError("metadata changes are required")
	}

	err = s.repo.WithTransaction(ctx, func(ctx context.Context, repo interfaces.KnowledgeMetadataRepository) error {
		for _, change := range command.Changes {
			value, expectedVersion, err := prepareDocumentMetadataSave(
				ctx,
				repo,
				tenantID,
				knowledge,
				change,
				command.UpdatedBy,
			)
			if err != nil {
				return err
			}
			if err := repo.SaveDocumentValue(ctx, value, expectedVersion); err != nil {
				if errors.Is(err, repository.ErrMetadataVersionConflict) {
					latest, _ := repo.GetDocumentValue(
						ctx,
						tenantID,
						knowledge.KnowledgeBaseID,
						knowledge.ID,
						value.MetadataDefinitionID,
					)
					return apperrors.NewConflictError("metadata value version conflict").WithDetails(latest)
				}
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return s.readSingleDocumentMetadata(ctx, knowledge.ID)
}

func (s *metadataService) ConfirmDocumentMetadata(
	ctx context.Context,
	command types.ConfirmDocumentMetadata,
) (*types.DocumentMetadata, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		return nil, apperrors.NewUnauthorizedError("tenant ID not found in context")
	}
	if command.TenantID != 0 && command.TenantID != tenantID {
		return nil, apperrors.NewForbiddenError("metadata tenant does not match request tenant")
	}
	knowledge, err := s.knowledgeRepo.GetKnowledgeByID(ctx, tenantID, command.KnowledgeID)
	if err != nil {
		return nil, err
	}
	if err := s.ensureDocumentKnowledgeBase(ctx, tenantID, knowledge.KnowledgeBaseID); err != nil {
		return nil, err
	}
	values, err := s.repo.ListDocumentValues(
		ctx,
		tenantID,
		knowledge.KnowledgeBaseID,
		[]string{knowledge.ID},
	)
	if err != nil {
		return nil, err
	}
	requested := make(map[string]struct{}, len(command.MetadataDefinitionIDs))
	for _, definitionID := range command.MetadataDefinitionIDs {
		requested[definitionID] = struct{}{}
	}
	err = s.repo.WithTransaction(ctx, func(ctx context.Context, repo interfaces.KnowledgeMetadataRepository) error {
		for _, stored := range values {
			if len(requested) > 0 {
				if _, ok := requested[stored.MetadataDefinitionID]; !ok {
					continue
				}
			}
			if stored.Source != types.MetadataValueSourceAutomatic ||
				stored.ReviewStatus != types.MetadataReviewStatusPending {
				continue
			}
			value := cloneMetadataValue(stored)
			expectedVersion := stored.Version
			value.ReviewStatus = types.MetadataReviewStatusConfirmed
			value.Version++
			if userID, ok := types.UserIDFromContext(ctx); ok && userID != "" {
				value.UpdatedBy = &userID
			}
			if err := repo.SaveDocumentValue(ctx, value, &expectedVersion); err != nil {
				if errors.Is(err, repository.ErrMetadataVersionConflict) {
					return apperrors.NewConflictError("metadata value version conflict")
				}
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return s.readSingleDocumentMetadata(ctx, knowledge.ID)
}

func (s *metadataService) ApplyAutomaticResults(
	ctx context.Context,
	command types.ApplyAutomaticMetadataResults,
) (*types.ApplyAutomaticMetadataReport, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		return nil, apperrors.NewUnauthorizedError("tenant ID not found in context")
	}
	if command.TenantID != 0 && command.TenantID != tenantID {
		return nil, apperrors.NewForbiddenError("metadata tenant does not match request tenant")
	}
	knowledge, err := s.knowledgeRepo.GetKnowledgeByID(ctx, tenantID, command.KnowledgeID)
	if err != nil {
		return nil, err
	}
	if err := s.ensureDocumentKnowledgeBase(ctx, tenantID, knowledge.KnowledgeBaseID); err != nil {
		return nil, err
	}
	if knowledge.KnowledgeBaseID != command.KnowledgeBaseID {
		return nil, apperrors.NewBadRequestError("knowledge does not belong to metadata knowledge base")
	}

	report := &types.ApplyAutomaticMetadataReport{}
	for _, result := range command.Results {
		definition, err := s.repo.GetDefinition(
			ctx,
			tenantID,
			knowledge.KnowledgeBaseID,
			result.MetadataDefinitionID,
		)
		if err != nil || definition.Status != types.MetadataStatusActive {
			report.Invalid++
			continue
		}
		candidate := &types.MetadataValue{
			TenantID:             tenantID,
			KnowledgeBaseID:      knowledge.KnowledgeBaseID,
			KnowledgeID:          knowledge.ID,
			MetadataDefinitionID: definition.ID,
		}
		if err := candidate.SetTypedValue(definition.ValueType, result.Value); err != nil ||
			!candidate.HasValue(definition.ValueType) {
			report.Invalid++
			continue
		}
		if err := validateMetadataValueOptions(definition, candidate.OptionIDs); err != nil {
			report.Invalid++
			continue
		}

		outcome, err := s.applyAutomaticValue(ctx, definition, candidate, result)
		if err != nil {
			return nil, err
		}
		switch outcome {
		case automaticApplyApplied:
			report.Applied++
		case automaticApplySkipped:
			report.Skipped++
		default:
			report.Invalid++
		}
	}
	return report, nil
}

func (s *metadataService) ResolveDocumentScope(
	ctx context.Context,
	query types.MetadataScopeQuery,
) (types.DocumentScope, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		return types.DocumentScope{}, apperrors.NewUnauthorizedError("tenant ID not found in context")
	}
	if query.TenantID != 0 && query.TenantID != tenantID {
		return types.DocumentScope{}, apperrors.NewForbiddenError("metadata tenant does not match request tenant")
	}
	if strings.TrimSpace(query.KnowledgeBaseID) == "" {
		return types.DocumentScope{}, apperrors.NewBadRequestError("knowledge base ID is required")
	}
	query.TenantID = tenantID
	if err := s.ensureDocumentKnowledgeBase(ctx, tenantID, query.KnowledgeBaseID); err != nil {
		return types.DocumentScope{}, err
	}

	for index := range query.Conditions {
		condition := &query.Conditions[index]
		definition, err := s.repo.GetDefinition(
			ctx,
			tenantID,
			query.KnowledgeBaseID,
			condition.MetadataDefinitionID,
		)
		if err != nil {
			return types.DocumentScope{}, err
		}
		if definition.Status != types.MetadataStatusActive {
			return types.DocumentScope{}, apperrors.NewBadRequestError("metadata definition is archived")
		}
		if !definition.Filterable {
			return types.DocumentScope{}, apperrors.NewBadRequestError("metadata definition is not filterable")
		}
		if err := validateMetadataCondition(definition, condition); err != nil {
			return types.DocumentScope{}, err
		}
	}
	return s.repo.ResolveDocumentScope(ctx, query)
}

func (s *metadataService) ensureDocumentKnowledgeBase(
	ctx context.Context,
	tenantID uint64,
	knowledgeBaseID string,
) error {
	knowledgeBase, err := s.knowledgeBaseRepo.GetKnowledgeBaseByIDAndTenant(ctx, knowledgeBaseID, tenantID)
	if errors.Is(err, repository.ErrKnowledgeBaseNotFound) {
		return apperrors.NewNotFoundError("knowledge base not found")
	}
	if err != nil {
		return err
	}
	if knowledgeBase.Type != types.KnowledgeBaseTypeDocument {
		return apperrors.NewBadRequestError("custom metadata is only available for document knowledge bases")
	}
	return nil
}

func validateMetadataCondition(
	definition *types.MetadataDefinition,
	condition *types.MetadataCondition,
) error {
	if condition.Operator == types.MetadataOperatorIsEmpty ||
		condition.Operator == types.MetadataOperatorIsNotEmpty {
		if len(condition.Values) != 0 {
			return apperrors.NewBadRequestError("empty metadata operators do not accept values")
		}
		return nil
	}

	valueCount := len(condition.Values)
	switch definition.ValueType {
	case types.MetadataValueTypeText:
		if condition.Operator != types.MetadataOperatorEquals &&
			condition.Operator != types.MetadataOperatorContains {
			return invalidMetadataOperator(definition.ValueType)
		}
		if valueCount == 0 || !allMetadataStrings(condition.Values) {
			return apperrors.NewBadRequestError("text metadata filter requires string values")
		}
	case types.MetadataValueTypeSingleSelect:
		if condition.Operator != types.MetadataOperatorIn {
			return invalidMetadataOperator(definition.ValueType)
		}
		if err := validateMetadataFilterOptions(definition, condition.Values); err != nil {
			return err
		}
	case types.MetadataValueTypeMultiSelect:
		if condition.Operator != types.MetadataOperatorContainsAny &&
			condition.Operator != types.MetadataOperatorContainsAll {
			return invalidMetadataOperator(definition.ValueType)
		}
		if err := validateMetadataFilterOptions(definition, condition.Values); err != nil {
			return err
		}
	case types.MetadataValueTypeNumber:
		if !metadataOperatorIn(condition.Operator,
			types.MetadataOperatorEqual,
			types.MetadataOperatorGreaterThan,
			types.MetadataOperatorGTE,
			types.MetadataOperatorLessThan,
			types.MetadataOperatorLTE,
			types.MetadataOperatorBetween,
		) {
			return invalidMetadataOperator(definition.ValueType)
		}
		if (condition.Operator == types.MetadataOperatorBetween && valueCount != 2) ||
			(condition.Operator != types.MetadataOperatorBetween && valueCount == 0) {
			return apperrors.NewBadRequestError("number metadata filter has invalid value count")
		}
		for _, raw := range condition.Values {
			probe := &types.MetadataValue{}
			if err := probe.SetTypedValue(types.MetadataValueTypeNumber, raw); err != nil {
				return apperrors.NewBadRequestError("number metadata filter requires numeric values")
			}
		}
	case types.MetadataValueTypeDate:
		if !metadataOperatorIn(condition.Operator,
			types.MetadataOperatorOn,
			types.MetadataOperatorBefore,
			types.MetadataOperatorAfter,
			types.MetadataOperatorBetween,
		) {
			return invalidMetadataOperator(definition.ValueType)
		}
		if (condition.Operator == types.MetadataOperatorBetween && valueCount != 2) ||
			(condition.Operator != types.MetadataOperatorBetween && valueCount == 0) {
			return apperrors.NewBadRequestError("date metadata filter has invalid value count")
		}
		for _, raw := range condition.Values {
			probe := &types.MetadataValue{}
			if err := probe.SetTypedValue(types.MetadataValueTypeDate, raw); err != nil {
				return apperrors.NewBadRequestError("date metadata filter requires YYYY-MM-DD values")
			}
		}
	case types.MetadataValueTypeBoolean:
		if condition.Operator != types.MetadataOperatorEqual || valueCount == 0 {
			return invalidMetadataOperator(definition.ValueType)
		}
		for _, raw := range condition.Values {
			if _, ok := raw.(bool); !ok {
				return apperrors.NewBadRequestError("boolean metadata filter requires boolean values")
			}
		}
	default:
		return invalidMetadataOperator(definition.ValueType)
	}
	return nil
}

func validateMetadataFilterOptions(
	definition *types.MetadataDefinition,
	values []any,
) error {
	if len(values) == 0 || !allMetadataStrings(values) {
		return apperrors.NewBadRequestError("select metadata filter requires option IDs")
	}
	activeOptions := make(map[string]struct{}, len(definition.Options))
	for _, option := range definition.Options {
		if option.Status == types.MetadataStatusActive {
			activeOptions[option.ID] = struct{}{}
		}
	}
	for _, raw := range values {
		if _, ok := activeOptions[raw.(string)]; !ok {
			return apperrors.NewBadRequestError("metadata filter option is not active")
		}
	}
	return nil
}

func allMetadataStrings(values []any) bool {
	for _, value := range values {
		if _, ok := value.(string); !ok {
			return false
		}
	}
	return true
}

func metadataOperatorIn(operator types.MetadataOperator, allowed ...types.MetadataOperator) bool {
	for _, candidate := range allowed {
		if operator == candidate {
			return true
		}
	}
	return false
}

func invalidMetadataOperator(valueType types.MetadataValueType) error {
	return apperrors.NewBadRequestError("metadata operator is invalid for type " + string(valueType))
}

type automaticApplyOutcome int

const (
	automaticApplyInvalid automaticApplyOutcome = iota
	automaticApplyApplied
	automaticApplySkipped
)

func (s *metadataService) applyAutomaticValue(
	ctx context.Context,
	definition *types.MetadataDefinition,
	candidate *types.MetadataValue,
	result types.AutomaticMetadataResult,
) (automaticApplyOutcome, error) {
	existing, err := s.repo.GetDocumentValue(
		ctx,
		candidate.TenantID,
		candidate.KnowledgeBaseID,
		candidate.KnowledgeID,
		definition.ID,
	)
	if err != nil && !errors.Is(err, repository.ErrMetadataValueNotFound) {
		return automaticApplyInvalid, err
	}
	if errors.Is(err, repository.ErrMetadataValueNotFound) {
		existing = nil
	}
	if existing != nil && !existing.AllowAutoOverwrite {
		return automaticApplySkipped, nil
	}
	if existing != nil && existing.Source == types.MetadataValueSourceAutomatic &&
		automaticResultMatches(existing, candidate, result, definition.ValueType) {
		return automaticApplySkipped, nil
	}

	value := cloneMetadataValue(candidate)
	var expectedVersion *int
	if existing == nil {
		value.Version = 1
	} else {
		value.ID = existing.ID
		value.CreatedAt = existing.CreatedAt
		currentVersion := existing.Version
		expectedVersion = &currentVersion
		value.Version = currentVersion + 1
	}
	value.Source = types.MetadataValueSourceAutomatic
	value.ReviewStatus = types.MetadataReviewStatusPending
	value.AllowAutoOverwrite = true
	value.UpdatedBy = nil
	if result.AutoRuleID != "" {
		autoRuleID := result.AutoRuleID
		value.AutoRuleID = &autoRuleID
	}
	if result.AutoRuleRevision > 0 {
		autoRuleRevision := result.AutoRuleRevision
		value.AutoRuleRevision = &autoRuleRevision
	}

	if err := s.repo.SaveDocumentValue(ctx, value, expectedVersion); err != nil {
		if !errors.Is(err, repository.ErrMetadataVersionConflict) {
			return automaticApplyInvalid, err
		}
		latest, readErr := s.repo.GetDocumentValue(
			ctx,
			candidate.TenantID,
			candidate.KnowledgeBaseID,
			candidate.KnowledgeID,
			definition.ID,
		)
		if readErr != nil {
			return automaticApplyInvalid, readErr
		}
		if !latest.AllowAutoOverwrite {
			return automaticApplySkipped, nil
		}
		value.ID = latest.ID
		value.Version = latest.Version + 1
		latestVersion := latest.Version
		if retryErr := s.repo.SaveDocumentValue(ctx, value, &latestVersion); retryErr != nil {
			if errors.Is(retryErr, repository.ErrMetadataVersionConflict) {
				return automaticApplySkipped, nil
			}
			return automaticApplyInvalid, retryErr
		}
	}
	return automaticApplyApplied, nil
}

func automaticResultMatches(
	existing *types.MetadataValue,
	candidate *types.MetadataValue,
	result types.AutomaticMetadataResult,
	valueType types.MetadataValueType,
) bool {
	if !reflect.DeepEqual(existing.TypedValue(valueType), candidate.TypedValue(valueType)) {
		return false
	}
	if result.AutoRuleID != "" && (existing.AutoRuleID == nil || *existing.AutoRuleID != result.AutoRuleID) {
		return false
	}
	return result.AutoRuleRevision <= 0 ||
		(existing.AutoRuleRevision != nil && *existing.AutoRuleRevision == result.AutoRuleRevision)
}

func buildDocumentMetadata(
	knowledgeID string,
	definitions []*types.MetadataDefinition,
	values map[string]*types.MetadataValue,
) *types.DocumentMetadata {
	result := &types.DocumentMetadata{
		KnowledgeID: knowledgeID,
		Values:      make([]types.DocumentMetadataField, 0, len(definitions)),
	}
	for _, definition := range definitions {
		value := values[definition.ID]
		if definition.Status != types.MetadataStatusActive && value == nil {
			continue
		}
		if value != nil {
			value.Value = value.TypedValue(definition.ValueType)
		}
		completion := types.MetadataCompletionStatusEmptyOptional
		if value != nil && value.HasValue(definition.ValueType) {
			completion = types.MetadataCompletionStatusFilled
		} else if definition.Status == types.MetadataStatusActive && definition.Required {
			completion = types.MetadataCompletionStatusIncomplete
			result.IncompleteCount++
		}
		result.Values = append(result.Values, types.DocumentMetadataField{
			Definition:       definition,
			Value:            value,
			CompletionStatus: completion,
		})
	}
	return result
}

func (s *metadataService) readSingleDocumentMetadata(
	ctx context.Context,
	knowledgeID string,
) (*types.DocumentMetadata, error) {
	metadata, err := s.ReadDocumentMetadata(ctx, []string{knowledgeID})
	if err != nil {
		return nil, err
	}
	if len(metadata) == 0 {
		return nil, apperrors.NewNotFoundError("knowledge not found")
	}
	return metadata[0], nil
}

func validateMetadataValueChange(
	ctx context.Context,
	repo interfaces.KnowledgeMetadataRepository,
	tenantID uint64,
	knowledgeBaseID string,
	change types.MetadataValueChange,
) (*types.MetadataDefinition, *types.MetadataValue, error) {
	definition, err := repo.GetDefinition(ctx, tenantID, knowledgeBaseID, change.MetadataDefinitionID)
	if err != nil {
		return nil, nil, err
	}
	if definition.Status != types.MetadataStatusActive {
		return nil, nil, apperrors.NewBadRequestError("metadata definition is archived")
	}
	probe := &types.MetadataValue{}
	if change.ValueSet {
		if err := probe.SetTypedValue(definition.ValueType, change.Value); err != nil {
			return nil, nil, apperrors.NewBadRequestError(err.Error())
		}
		if err := validateMetadataValueOptions(definition, probe.OptionIDs); err != nil {
			return nil, nil, err
		}
	} else if change.AllowAutoOverwrite == nil {
		return nil, nil, apperrors.NewBadRequestError("metadata value or overwrite policy is required")
	}
	return definition, probe, nil
}

func prepareDocumentMetadataSave(
	ctx context.Context,
	repo interfaces.KnowledgeMetadataRepository,
	tenantID uint64,
	knowledge *types.Knowledge,
	change types.MetadataValueChange,
	updatedBy string,
) (*types.MetadataValue, *int, error) {
	definition, _, err := validateMetadataValueChange(
		ctx,
		repo,
		tenantID,
		knowledge.KnowledgeBaseID,
		change,
	)
	if err != nil {
		return nil, nil, err
	}
	existing, err := repo.GetDocumentValue(
		ctx,
		tenantID,
		knowledge.KnowledgeBaseID,
		knowledge.ID,
		definition.ID,
	)
	if err != nil && !errors.Is(err, repository.ErrMetadataValueNotFound) {
		return nil, nil, err
	}
	if errors.Is(err, repository.ErrMetadataValueNotFound) {
		existing = nil
	}
	if existing == nil && !change.ValueSet {
		return nil, nil, apperrors.NewBadRequestError("cannot change overwrite policy before a metadata value exists")
	}
	if existing != nil {
		if change.ExpectedVersion == nil || *change.ExpectedVersion != existing.Version {
			return nil, nil, apperrors.NewConflictError("metadata value version conflict").WithDetails(existing)
		}
	} else if change.ExpectedVersion != nil && *change.ExpectedVersion != 0 {
		return nil, nil, apperrors.NewConflictError("metadata value version conflict")
	}

	value := cloneMetadataValue(existing)
	if value == nil {
		value = &types.MetadataValue{
			TenantID:             tenantID,
			KnowledgeBaseID:      knowledge.KnowledgeBaseID,
			KnowledgeID:          knowledge.ID,
			MetadataDefinitionID: definition.ID,
		}
	}
	if change.ValueSet {
		if err := value.SetTypedValue(definition.ValueType, change.Value); err != nil {
			return nil, nil, apperrors.NewBadRequestError(err.Error())
		}
		if err := validateMetadataValueOptions(definition, value.OptionIDs); err != nil {
			return nil, nil, err
		}
		value.Source = types.MetadataValueSourceManual
		value.ReviewStatus = types.MetadataReviewStatusConfirmed
		value.AllowAutoOverwrite = false
		value.AutoRuleID = nil
		value.AutoRuleRevision = nil
	}
	if change.AllowAutoOverwrite != nil {
		value.AllowAutoOverwrite = *change.AllowAutoOverwrite
	}
	if updatedBy != "" {
		updated := updatedBy
		value.UpdatedBy = &updated
	}
	var expectedVersion *int
	if existing == nil {
		value.Version = 1
		zero := 0
		if change.ExpectedVersion != nil {
			expectedVersion = change.ExpectedVersion
		} else {
			expectedVersion = &zero
		}
	} else {
		expectedVersion = change.ExpectedVersion
		value.Version = existing.Version + 1
	}
	return value, expectedVersion, nil
}

func validateMetadataValueOptions(definition *types.MetadataDefinition, optionIDs []string) error {
	if definition.ValueType == types.MetadataValueTypeSingleSelect && len(optionIDs) > 1 {
		return apperrors.NewBadRequestError("single_select accepts at most one option")
	}
	if definition.ValueType != types.MetadataValueTypeSingleSelect &&
		definition.ValueType != types.MetadataValueTypeMultiSelect {
		if len(optionIDs) > 0 {
			return apperrors.NewBadRequestError("metadata options do not match definition type")
		}
		return nil
	}
	activeOptions := make(map[string]struct{}, len(definition.Options))
	for _, option := range definition.Options {
		if option.Status == types.MetadataStatusActive {
			activeOptions[option.ID] = struct{}{}
		}
	}
	seen := make(map[string]struct{}, len(optionIDs))
	for _, optionID := range optionIDs {
		if _, ok := activeOptions[optionID]; !ok {
			return apperrors.NewBadRequestError("metadata option is not active for this definition")
		}
		if _, duplicate := seen[optionID]; duplicate {
			return apperrors.NewBadRequestError("metadata option is duplicated")
		}
		seen[optionID] = struct{}{}
	}
	return nil
}

func cloneMetadataValue(value *types.MetadataValue) *types.MetadataValue {
	if value == nil {
		return nil
	}
	clone := *value
	clone.OptionIDs = append([]string(nil), value.OptionIDs...)
	return &clone
}

func validateMetadataAutoRule(
	strategy types.MetadataRuleStrategy,
	config types.JSONMap,
) (types.JSONMap, error) {
	result := make(types.JSONMap, len(config))
	for key, value := range config {
		result[key] = value
	}
	switch strategy {
	case types.MetadataRuleStrategySourceMapping:
		sourceKey, ok := result["source_key"].(string)
		sourceKey = strings.TrimSpace(sourceKey)
		if !ok || sourceKey == "" {
			return nil, apperrors.NewBadRequestError("source_mapping requires source_key")
		}
		result["source_key"] = sourceKey
	case types.MetadataRuleStrategyLLMExtract:
		instruction, ok := result["instruction"].(string)
		instruction = strings.TrimSpace(instruction)
		if !ok || instruction == "" {
			return nil, apperrors.NewBadRequestError("llm_extract requires instruction")
		}
		result["instruction"] = instruction
		if rawModelID, exists := result["model_id"]; exists {
			modelID, ok := rawModelID.(string)
			if !ok {
				return nil, apperrors.NewBadRequestError("llm_extract model_id must be a string")
			}
			result["model_id"] = strings.TrimSpace(modelID)
		}
	default:
		return nil, apperrors.NewBadRequestError("metadata auto rule strategy is invalid")
	}
	return result, nil
}

func definitionFromCommand(
	tenantID uint64,
	command types.ConfigureMetadataDefinition,
) (*types.MetadataDefinition, error) {
	name := strings.TrimSpace(command.Name)
	normalizedName := strings.ToLower(name)
	if strings.TrimSpace(command.KnowledgeBaseID) == "" {
		return nil, apperrors.NewBadRequestError("knowledge base ID is required")
	}
	if name == "" || utf8.RuneCountInString(name) > metadataNameMaxLength {
		return nil, apperrors.NewBadRequestError("metadata definition name is invalid")
	}
	if _, reserved := reservedMetadataNames[normalizedName]; reserved {
		return nil, apperrors.NewBadRequestError("metadata definition name is reserved")
	}
	if utf8.RuneCountInString(command.Description) > metadataDescriptionMaxLength {
		return nil, apperrors.NewBadRequestError("metadata definition description is too long")
	}
	if !validMetadataValueType(command.ValueType) {
		return nil, apperrors.NewBadRequestError("metadata value type is invalid")
	}

	options, err := optionsFromCommand(command.ValueType, command.Options)
	if err != nil {
		return nil, err
	}
	return &types.MetadataDefinition{
		ID:              command.DefinitionID,
		TenantID:        tenantID,
		KnowledgeBaseID: strings.TrimSpace(command.KnowledgeBaseID),
		Name:            name,
		NormalizedName:  normalizedName,
		Description:     strings.TrimSpace(command.Description),
		ValueType:       command.ValueType,
		Required:        command.Required,
		Filterable:      command.Filterable,
		Status:          types.MetadataStatusActive,
		SortOrder:       command.SortOrder,
		Options:         options,
	}, nil
}

func optionsFromCommand(
	valueType types.MetadataValueType,
	commands []types.ConfigureMetadataOption,
) ([]types.MetadataOption, error) {
	isSelect := valueType == types.MetadataValueTypeSingleSelect || valueType == types.MetadataValueTypeMultiSelect
	if !isSelect && len(commands) > 0 {
		return nil, apperrors.NewBadRequestError("metadata options are only valid for select types")
	}
	if !isSelect {
		return nil, nil
	}

	options := make([]types.MetadataOption, 0, len(commands))
	normalizedLabels := make(map[string]struct{}, len(commands))
	activeCount := 0
	for _, command := range commands {
		label := strings.TrimSpace(command.Label)
		normalizedLabel := strings.ToLower(label)
		if label == "" || utf8.RuneCountInString(label) > metadataOptionLabelMaxLength {
			return nil, apperrors.NewBadRequestError("metadata option label is invalid")
		}
		if _, duplicate := normalizedLabels[normalizedLabel]; duplicate {
			return nil, apperrors.NewConflictError("metadata option label already exists")
		}
		normalizedLabels[normalizedLabel] = struct{}{}

		status := command.Status
		if status == "" {
			status = types.MetadataStatusActive
		}
		if status != types.MetadataStatusActive && status != types.MetadataStatusArchived {
			return nil, apperrors.NewBadRequestError("metadata option status is invalid")
		}
		if status == types.MetadataStatusActive {
			activeCount++
		}
		options = append(options, types.MetadataOption{
			ID:              command.ID,
			Label:           label,
			NormalizedLabel: normalizedLabel,
			Status:          status,
			SortOrder:       command.SortOrder,
		})
	}
	if activeCount == 0 {
		return nil, apperrors.NewBadRequestError("select metadata requires at least one active option")
	}
	return options, nil
}

func validMetadataValueType(valueType types.MetadataValueType) bool {
	switch valueType {
	case types.MetadataValueTypeText,
		types.MetadataValueTypeSingleSelect,
		types.MetadataValueTypeMultiSelect,
		types.MetadataValueTypeNumber,
		types.MetadataValueTypeDate,
		types.MetadataValueTypeBoolean:
		return true
	default:
		return false
	}
}

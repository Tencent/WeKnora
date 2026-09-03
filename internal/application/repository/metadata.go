package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

var (
	ErrMetadataDefinitionNotFound    = errors.New("metadata definition not found")
	ErrMetadataOptionNotInDefinition = errors.New("metadata option does not belong to definition")
	ErrMetadataAutoRuleNotFound      = errors.New("metadata auto rule not found")
	ErrMetadataValueNotFound         = errors.New("metadata value not found")
	ErrMetadataVersionConflict       = errors.New("metadata value version conflict")
	ErrUnsupportedMetadataOperator   = errors.New("unsupported metadata operator")
)

type knowledgeMetadataRepository struct {
	db *gorm.DB
}

func NewKnowledgeMetadataRepository(db *gorm.DB) interfaces.KnowledgeMetadataRepository {
	return &knowledgeMetadataRepository{db: db}
}

func (r *knowledgeMetadataRepository) CreateDefinition(
	ctx context.Context,
	definition *types.MetadataDefinition,
) error {
	return r.db.WithContext(ctx).Create(definition).Error
}

func (r *knowledgeMetadataRepository) GetDefinition(
	ctx context.Context,
	tenantID uint64,
	knowledgeBaseID string,
	definitionID string,
) (*types.MetadataDefinition, error) {
	var definition types.MetadataDefinition
	err := r.db.WithContext(ctx).
		Preload("Options", func(options *gorm.DB) *gorm.DB {
			return options.Order("sort_order ASC, created_at ASC, id ASC")
		}).
		Preload("AutoRule", "enabled = ?", true).
		Where(
			"tenant_id = ? AND knowledge_base_id = ? AND id = ?",
			tenantID,
			knowledgeBaseID,
			definitionID,
		).
		First(&definition).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrMetadataDefinitionNotFound
	}
	return &definition, err
}

func (r *knowledgeMetadataRepository) ListDefinitions(
	ctx context.Context,
	tenantID uint64,
	knowledgeBaseID string,
	includeArchived bool,
) ([]*types.MetadataDefinition, error) {
	query := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, knowledgeBaseID)
	if !includeArchived {
		query = query.Where("status = ?", types.MetadataStatusActive)
	}

	var definitions []*types.MetadataDefinition
	err := query.
		Preload("Options", func(options *gorm.DB) *gorm.DB {
			return options.Order("sort_order ASC, created_at ASC, id ASC")
		}).
		Preload("AutoRule", "enabled = ?", true).
		Order("sort_order ASC, created_at ASC, id ASC").
		Find(&definitions).Error
	return definitions, err
}

func (r *knowledgeMetadataRepository) UpdateDefinition(
	ctx context.Context,
	definition *types.MetadataDefinition,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.
			Model(&types.MetadataDefinition{}).
			Where(
				"tenant_id = ? AND knowledge_base_id = ? AND id = ?",
				definition.TenantID,
				definition.KnowledgeBaseID,
				definition.ID,
			).
			Updates(map[string]any{
				"name":            definition.Name,
				"normalized_name": definition.NormalizedName,
				"description":     definition.Description,
				"value_type":      definition.ValueType,
				"required":        definition.Required,
				"filterable":      definition.Filterable,
				"sort_order":      definition.SortOrder,
				"updated_at":      gorm.Expr("CURRENT_TIMESTAMP"),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrMetadataDefinitionNotFound
		}
		if definition.Options == nil {
			return nil
		}

		var existing []types.MetadataOption
		if err := tx.Where("metadata_definition_id = ?", definition.ID).Find(&existing).Error; err != nil {
			return err
		}
		existingIDs := make(map[string]struct{}, len(existing))
		for _, option := range existing {
			existingIDs[option.ID] = struct{}{}
		}
		retainedIDs := make([]string, 0, len(definition.Options))
		for index := range definition.Options {
			option := &definition.Options[index]
			option.MetadataDefinitionID = definition.ID
			if option.ID == "" {
				if err := tx.Create(option).Error; err != nil {
					return err
				}
				retainedIDs = append(retainedIDs, option.ID)
				continue
			}
			if _, ok := existingIDs[option.ID]; !ok {
				return ErrMetadataOptionNotInDefinition
			}
			if err := tx.Model(&types.MetadataOption{}).
				Where("metadata_definition_id = ? AND id = ?", definition.ID, option.ID).
				Updates(map[string]any{
					"label":            option.Label,
					"normalized_label": option.NormalizedLabel,
					"status":           option.Status,
					"sort_order":       option.SortOrder,
					"updated_at":       gorm.Expr("CURRENT_TIMESTAMP"),
				}).Error; err != nil {
				return err
			}
			retainedIDs = append(retainedIDs, option.ID)
		}

		archiveQuery := tx.Model(&types.MetadataOption{}).
			Where("metadata_definition_id = ?", definition.ID)
		if len(retainedIDs) > 0 {
			archiveQuery = archiveQuery.Where("id NOT IN ?", retainedIDs)
		}
		return archiveQuery.Updates(map[string]any{
			"status":     types.MetadataStatusArchived,
			"updated_at": gorm.Expr("CURRENT_TIMESTAMP"),
		}).Error
	})
}

func (r *knowledgeMetadataRepository) ArchiveDefinition(
	ctx context.Context,
	tenantID uint64,
	knowledgeBaseID string,
	definitionID string,
) error {
	result := r.db.WithContext(ctx).
		Model(&types.MetadataDefinition{}).
		Where(
			"tenant_id = ? AND knowledge_base_id = ? AND id = ?",
			tenantID,
			knowledgeBaseID,
			definitionID,
		).
		Updates(map[string]any{
			"status":     types.MetadataStatusArchived,
			"updated_at": gorm.Expr("CURRENT_TIMESTAMP"),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrMetadataDefinitionNotFound
	}
	return nil
}

func (r *knowledgeMetadataRepository) DefinitionHasUsage(
	ctx context.Context,
	tenantID uint64,
	knowledgeBaseID string,
	definitionID string,
) (bool, error) {
	var valueCount int64
	if err := r.db.WithContext(ctx).
		Model(&types.MetadataValue{}).
		Where(
			"tenant_id = ? AND knowledge_base_id = ? AND metadata_definition_id = ?",
			tenantID,
			knowledgeBaseID,
			definitionID,
		).
		Count(&valueCount).Error; err != nil {
		return false, err
	}
	if valueCount > 0 {
		return true, nil
	}

	var ruleCount int64
	err := r.db.WithContext(ctx).
		Model(&types.MetadataAutoRule{}).
		Where(
			"tenant_id = ? AND knowledge_base_id = ? AND metadata_definition_id = ?",
			tenantID,
			knowledgeBaseID,
			definitionID,
		).
		Count(&ruleCount).Error
	return ruleCount > 0, err
}

func (r *knowledgeMetadataRepository) GetAutoRule(
	ctx context.Context,
	tenantID uint64,
	knowledgeBaseID string,
	definitionID string,
	enabledOnly bool,
) (*types.MetadataAutoRule, error) {
	query := r.db.WithContext(ctx).
		Where(
			"tenant_id = ? AND knowledge_base_id = ? AND metadata_definition_id = ?",
			tenantID,
			knowledgeBaseID,
			definitionID,
		)
	if enabledOnly {
		query = query.Where("enabled = ?", true)
	}

	var rule types.MetadataAutoRule
	err := query.Order("revision DESC, updated_at DESC").First(&rule).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrMetadataAutoRuleNotFound
	}
	return &rule, err
}

func (r *knowledgeMetadataRepository) SaveAutoRule(
	ctx context.Context,
	rule *types.MetadataAutoRule,
) error {
	if rule.ID == "" {
		return r.db.WithContext(ctx).Create(rule).Error
	}

	result := r.db.WithContext(ctx).
		Model(&types.MetadataAutoRule{}).
		Where(
			"id = ? AND tenant_id = ? AND knowledge_base_id = ? AND metadata_definition_id = ?",
			rule.ID,
			rule.TenantID,
			rule.KnowledgeBaseID,
			rule.MetadataDefinitionID,
		).
		Updates(map[string]any{
			"strategy":   rule.Strategy,
			"config":     rule.Config,
			"revision":   rule.Revision,
			"enabled":    rule.Enabled,
			"updated_at": gorm.Expr("CURRENT_TIMESTAMP"),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrMetadataAutoRuleNotFound
	}
	return nil
}

func (r *knowledgeMetadataRepository) CreateDocumentValue(
	ctx context.Context,
	value *types.MetadataValue,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(value.OptionIDs) > 0 {
			var optionCount int64
			if err := tx.Model(&types.MetadataOption{}).
				Where("metadata_definition_id = ? AND id IN ?", value.MetadataDefinitionID, value.OptionIDs).
				Count(&optionCount).Error; err != nil {
				return err
			}
			if optionCount != int64(len(value.OptionIDs)) {
				return ErrMetadataOptionNotInDefinition
			}
		}

		if err := tx.Create(value).Error; err != nil {
			return err
		}
		if len(value.OptionIDs) == 0 {
			return nil
		}

		links := make([]types.MetadataValueOption, 0, len(value.OptionIDs))
		for index, optionID := range value.OptionIDs {
			links = append(links, types.MetadataValueOption{
				MetadataValueID: value.ID,
				OptionID:        optionID,
				SortOrder:       index,
			})
		}
		return tx.Create(&links).Error
	})
}

func (r *knowledgeMetadataRepository) GetDocumentValue(
	ctx context.Context,
	tenantID uint64,
	knowledgeBaseID string,
	knowledgeID string,
	definitionID string,
) (*types.MetadataValue, error) {
	var value types.MetadataValue
	err := r.db.WithContext(ctx).
		Where(
			"tenant_id = ? AND knowledge_base_id = ? AND knowledge_id = ? AND metadata_definition_id = ?",
			tenantID,
			knowledgeBaseID,
			knowledgeID,
			definitionID,
		).
		First(&value).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrMetadataValueNotFound
	}
	if err != nil {
		return nil, err
	}

	var links []types.MetadataValueOption
	if err := r.db.WithContext(ctx).
		Where("metadata_value_id = ?", value.ID).
		Order("sort_order ASC").
		Find(&links).Error; err != nil {
		return nil, err
	}
	for _, link := range links {
		value.OptionIDs = append(value.OptionIDs, link.OptionID)
	}
	return &value, nil
}

func (r *knowledgeMetadataRepository) WithTransaction(
	ctx context.Context,
	fn func(ctx context.Context, repo interfaces.KnowledgeMetadataRepository) error,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(ctx, &knowledgeMetadataRepository{db: tx})
	})
}

func (r *knowledgeMetadataRepository) SaveDocumentValue(
	ctx context.Context,
	value *types.MetadataValue,
	expectedVersion *int,
) error {
	if value.ID == "" {
		if expectedVersion != nil && *expectedVersion != 0 {
			return ErrMetadataVersionConflict
		}
		return r.CreateDocumentValue(ctx, value)
	}
	if expectedVersion == nil {
		return ErrMetadataVersionConflict
	}

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := validateMetadataOptionMembership(tx, value); err != nil {
			return err
		}
		result := tx.Model(&types.MetadataValue{}).
			Where(
				"id = ? AND tenant_id = ? AND knowledge_base_id = ? AND knowledge_id = ? "+
					"AND metadata_definition_id = ? AND version = ?",
				value.ID,
				value.TenantID,
				value.KnowledgeBaseID,
				value.KnowledgeID,
				value.MetadataDefinitionID,
				*expectedVersion,
			).
			Updates(map[string]any{
				"text_value":           value.TextValue,
				"number_value":         value.NumberValue,
				"date_value":           value.DateValue,
				"bool_value":           value.BoolValue,
				"source":               value.Source,
				"review_status":        value.ReviewStatus,
				"allow_auto_overwrite": value.AllowAutoOverwrite,
				"version":              value.Version,
				"auto_rule_id":         value.AutoRuleID,
				"auto_rule_revision":   value.AutoRuleRevision,
				"updated_by":           value.UpdatedBy,
				"updated_at":           gorm.Expr("CURRENT_TIMESTAMP"),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrMetadataVersionConflict
		}
		if err := tx.Where("metadata_value_id = ?", value.ID).
			Delete(&types.MetadataValueOption{}).Error; err != nil {
			return err
		}
		if len(value.OptionIDs) == 0 {
			return nil
		}
		links := make([]types.MetadataValueOption, 0, len(value.OptionIDs))
		for index, optionID := range value.OptionIDs {
			links = append(links, types.MetadataValueOption{
				MetadataValueID: value.ID,
				OptionID:        optionID,
				SortOrder:       index,
			})
		}
		return tx.Create(&links).Error
	})
}

func validateMetadataOptionMembership(tx *gorm.DB, value *types.MetadataValue) error {
	if len(value.OptionIDs) == 0 {
		return nil
	}
	var optionCount int64
	if err := tx.Model(&types.MetadataOption{}).
		Where("metadata_definition_id = ? AND id IN ?", value.MetadataDefinitionID, value.OptionIDs).
		Count(&optionCount).Error; err != nil {
		return err
	}
	if optionCount != int64(len(value.OptionIDs)) {
		return ErrMetadataOptionNotInDefinition
	}
	return nil
}

func (r *knowledgeMetadataRepository) ListDocumentValues(
	ctx context.Context,
	tenantID uint64,
	knowledgeBaseID string,
	knowledgeIDs []string,
) ([]*types.MetadataValue, error) {
	if len(knowledgeIDs) == 0 {
		return []*types.MetadataValue{}, nil
	}

	var values []*types.MetadataValue
	err := r.db.WithContext(ctx).
		Where(
			"tenant_id = ? AND knowledge_base_id = ? AND knowledge_id IN ?",
			tenantID,
			knowledgeBaseID,
			knowledgeIDs,
		).
		Order("knowledge_id ASC, metadata_definition_id ASC").
		Find(&values).Error
	if err != nil || len(values) == 0 {
		return values, err
	}

	valueIDs := make([]string, 0, len(values))
	valueByID := make(map[string]*types.MetadataValue, len(values))
	for _, value := range values {
		valueIDs = append(valueIDs, value.ID)
		valueByID[value.ID] = value
	}

	var links []types.MetadataValueOption
	if err := r.db.WithContext(ctx).
		Where("metadata_value_id IN ?", valueIDs).
		Order("metadata_value_id ASC, sort_order ASC").
		Find(&links).Error; err != nil {
		return nil, err
	}
	for _, link := range links {
		valueByID[link.MetadataValueID].OptionIDs = append(
			valueByID[link.MetadataValueID].OptionIDs,
			link.OptionID,
		)
	}
	return values, nil
}

func (r *knowledgeMetadataRepository) DeleteDocumentMetadata(
	ctx context.Context,
	tenantID uint64,
	knowledgeIDs []string,
) error {
	if len(knowledgeIDs) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		valueIDs := tx.Model(&types.MetadataValue{}).
			Select("id").
			Where("tenant_id = ? AND knowledge_id IN ?", tenantID, knowledgeIDs)
		if err := tx.Where("metadata_value_id IN (?)", valueIDs).
			Delete(&types.MetadataValueOption{}).Error; err != nil {
			return err
		}
		return tx.Where("tenant_id = ? AND knowledge_id IN ?", tenantID, knowledgeIDs).
			Delete(&types.MetadataValue{}).Error
	})
}

func (r *knowledgeMetadataRepository) DeleteKnowledgeBaseMetadata(
	ctx context.Context,
	tenantID uint64,
	knowledgeBaseID string,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		definitionIDs := tx.Model(&types.MetadataDefinition{}).
			Select("id").
			Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, knowledgeBaseID)
		valueIDs := tx.Model(&types.MetadataValue{}).
			Select("id").
			Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, knowledgeBaseID)
		if err := tx.Where("metadata_value_id IN (?)", valueIDs).
			Delete(&types.MetadataValueOption{}).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, knowledgeBaseID).
			Delete(&types.MetadataValue{}).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, knowledgeBaseID).
			Delete(&types.MetadataAutoRule{}).Error; err != nil {
			return err
		}
		if err := tx.Where("metadata_definition_id IN (?)", definitionIDs).
			Delete(&types.MetadataOption{}).Error; err != nil {
			return err
		}
		return tx.Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, knowledgeBaseID).
			Delete(&types.MetadataDefinition{}).Error
	})
}

func (r *knowledgeMetadataRepository) ResolveDocumentScope(
	ctx context.Context,
	scopeQuery types.MetadataScopeQuery,
) (types.DocumentScope, error) {
	if len(scopeQuery.Conditions) == 0 && scopeQuery.ExplicitKnowledgeIDs == nil {
		return types.DocumentScope{Mode: types.DocumentScopeModeAll}, nil
	}
	if scopeQuery.ExplicitKnowledgeIDs != nil && len(scopeQuery.ExplicitKnowledgeIDs) == 0 {
		return types.DocumentScope{Mode: types.DocumentScopeModeNone}, nil
	}

	query := r.db.WithContext(ctx).
		Model(&types.Knowledge{}).
		Where(
			"knowledges.tenant_id = ? AND knowledges.knowledge_base_id = ?",
			scopeQuery.TenantID,
			scopeQuery.KnowledgeBaseID,
		)
	if scopeQuery.ExplicitKnowledgeIDs != nil {
		query = query.Where("knowledges.id IN ?", scopeQuery.ExplicitKnowledgeIDs)
	}

	for _, condition := range scopeQuery.Conditions {
		valueQuery := r.db.WithContext(ctx).
			Table("knowledge_metadata_values AS metadata_value").
			Select("1").
			Where("metadata_value.tenant_id = knowledges.tenant_id").
			Where("metadata_value.knowledge_base_id = knowledges.knowledge_base_id").
			Where("metadata_value.knowledge_id = knowledges.id").
			Where("metadata_value.metadata_definition_id = ?", condition.MetadataDefinitionID)

		var err error
		query, err = applyMetadataCondition(query, valueQuery, condition)
		if err != nil {
			return types.DocumentScope{}, err
		}
	}

	var ids []string
	if err := query.Order("knowledges.id ASC").Pluck("knowledges.id", &ids).Error; err != nil {
		return types.DocumentScope{}, err
	}
	if len(ids) == 0 {
		return types.DocumentScope{Mode: types.DocumentScopeModeNone}, nil
	}
	return types.DocumentScope{Mode: types.DocumentScopeModeIDs, IDs: ids}, nil
}

func applyMetadataCondition(
	knowledgeQuery *gorm.DB,
	valueQuery *gorm.DB,
	condition types.MetadataCondition,
) (*gorm.DB, error) {
	switch condition.Operator {
	case types.MetadataOperatorIsEmpty, types.MetadataOperatorIsNotEmpty:
		nonEmpty := `(
			NULLIF(TRIM(metadata_value.text_value), '') IS NOT NULL
			OR metadata_value.number_value IS NOT NULL
			OR metadata_value.date_value IS NOT NULL
			OR metadata_value.bool_value IS NOT NULL
			OR EXISTS (
				SELECT 1 FROM knowledge_metadata_value_options metadata_value_option
				WHERE metadata_value_option.metadata_value_id = metadata_value.id
			)
		)`
		valueQuery = valueQuery.Where(nonEmpty)
		if condition.Operator == types.MetadataOperatorIsEmpty {
			return knowledgeQuery.Where("NOT EXISTS (?)", valueQuery), nil
		}
		return knowledgeQuery.Where("EXISTS (?)", valueQuery), nil

	case types.MetadataOperatorEquals:
		if len(condition.Values) == 0 {
			return knowledgeQuery, ErrUnsupportedMetadataOperator
		}
		return knowledgeQuery.Where(
			"EXISTS (?)",
			valueQuery.Where("metadata_value.text_value IN ?", condition.Values),
		), nil

	case types.MetadataOperatorContains:
		values, err := metadataConditionStrings(condition.Values)
		if err != nil || len(values) == 0 {
			return knowledgeQuery, ErrUnsupportedMetadataOperator
		}
		parts := make([]string, 0, len(values))
		args := make([]any, 0, len(values))
		for _, value := range values {
			parts = append(parts, "LOWER(metadata_value.text_value) LIKE ?")
			args = append(args, "%"+strings.ToLower(value)+"%")
		}
		return knowledgeQuery.Where(
			"EXISTS (?)",
			valueQuery.Where("("+strings.Join(parts, " OR ")+")", args...),
		), nil

	case types.MetadataOperatorIn, types.MetadataOperatorContainsAny:
		values, err := metadataConditionStrings(condition.Values)
		if err != nil || len(values) == 0 {
			return knowledgeQuery, ErrUnsupportedMetadataOperator
		}
		valueQuery = valueQuery.
			Joins(
				"JOIN knowledge_metadata_value_options metadata_value_option "+
					"ON metadata_value_option.metadata_value_id = metadata_value.id",
			).
			Where("metadata_value_option.option_id IN ?", values)
		return knowledgeQuery.Where("EXISTS (?)", valueQuery), nil

	case types.MetadataOperatorContainsAll:
		values, err := metadataConditionStrings(condition.Values)
		if err != nil || len(values) == 0 {
			return knowledgeQuery, ErrUnsupportedMetadataOperator
		}
		values = distinctMetadataStrings(values)
		valueQuery = valueQuery.
			Joins(
				"JOIN knowledge_metadata_value_options metadata_value_option "+
					"ON metadata_value_option.metadata_value_id = metadata_value.id",
			).
			Where("metadata_value_option.option_id IN ?", values).
			Group("metadata_value.id").
			Having("COUNT(DISTINCT metadata_value_option.option_id) = ?", len(values))
		return knowledgeQuery.Where("EXISTS (?)", valueQuery), nil

	case types.MetadataOperatorEqual:
		if len(condition.Values) == 0 {
			return knowledgeQuery, ErrUnsupportedMetadataOperator
		}
		column := "metadata_value.number_value"
		if _, ok := condition.Values[0].(bool); ok {
			column = "metadata_value.bool_value"
		}
		return knowledgeQuery.Where(
			"EXISTS (?)",
			valueQuery.Where(column+" IN ?", condition.Values),
		), nil

	case types.MetadataOperatorGreaterThan,
		types.MetadataOperatorGTE,
		types.MetadataOperatorLessThan,
		types.MetadataOperatorLTE:
		if len(condition.Values) == 0 {
			return knowledgeQuery, ErrUnsupportedMetadataOperator
		}
		operator := map[types.MetadataOperator]string{
			types.MetadataOperatorGreaterThan: ">",
			types.MetadataOperatorGTE:         ">=",
			types.MetadataOperatorLessThan:    "<",
			types.MetadataOperatorLTE:         "<=",
		}[condition.Operator]
		parts := make([]string, 0, len(condition.Values))
		args := make([]any, 0, len(condition.Values))
		for _, value := range condition.Values {
			parts = append(parts, "metadata_value.number_value "+operator+" ?")
			args = append(args, value)
		}
		return knowledgeQuery.Where("EXISTS (?)", valueQuery.Where("("+strings.Join(parts, " OR ")+")", args...)), nil

	case types.MetadataOperatorBetween:
		if len(condition.Values) != 2 {
			return knowledgeQuery, ErrUnsupportedMetadataOperator
		}
		column := "metadata_value.number_value"
		if _, ok := condition.Values[0].(string); ok {
			column = "DATE(metadata_value.date_value)"
		}
		return knowledgeQuery.Where(
			"EXISTS (?)",
			valueQuery.Where(
				column+" BETWEEN ? AND ?",
				condition.Values[0],
				condition.Values[1],
			),
		), nil

	case types.MetadataOperatorOn, types.MetadataOperatorBefore, types.MetadataOperatorAfter:
		if len(condition.Values) == 0 {
			return knowledgeQuery, ErrUnsupportedMetadataOperator
		}
		operator := map[types.MetadataOperator]string{
			types.MetadataOperatorOn:     "=",
			types.MetadataOperatorBefore: "<",
			types.MetadataOperatorAfter:  ">",
		}[condition.Operator]
		parts := make([]string, 0, len(condition.Values))
		args := make([]any, 0, len(condition.Values))
		for _, value := range condition.Values {
			parts = append(parts, "DATE(metadata_value.date_value) "+operator+" ?")
			args = append(args, value)
		}
		return knowledgeQuery.Where("EXISTS (?)", valueQuery.Where("("+strings.Join(parts, " OR ")+")", args...)), nil
	default:
		return knowledgeQuery, fmt.Errorf("%w: %s", ErrUnsupportedMetadataOperator, condition.Operator)
	}
}

func metadataConditionStrings(values []any) ([]string, error) {
	result := make([]string, 0, len(values))
	for _, raw := range values {
		value, ok := raw.(string)
		if !ok {
			return nil, ErrUnsupportedMetadataOperator
		}
		result = append(result, value)
	}
	return result, nil
}

func distinctMetadataStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

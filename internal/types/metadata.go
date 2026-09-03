package types

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

var ErrInvalidMetadataValue = errors.New("invalid metadata value")

type MetadataValueType string

const (
	MetadataValueTypeText         MetadataValueType = "text"
	MetadataValueTypeSingleSelect MetadataValueType = "single_select"
	MetadataValueTypeMultiSelect  MetadataValueType = "multi_select"
	MetadataValueTypeNumber       MetadataValueType = "number"
	MetadataValueTypeDate         MetadataValueType = "date"
	MetadataValueTypeBoolean      MetadataValueType = "boolean"
)

type MetadataStatus string

const (
	MetadataStatusActive   MetadataStatus = "active"
	MetadataStatusArchived MetadataStatus = "archived"
)

type MetadataValueSource string

const (
	MetadataValueSourceAutomatic MetadataValueSource = "automatic"
	MetadataValueSourceManual    MetadataValueSource = "manual"
)

type MetadataReviewStatus string

const (
	MetadataReviewStatusPending   MetadataReviewStatus = "pending"
	MetadataReviewStatusConfirmed MetadataReviewStatus = "confirmed"
)

type MetadataCompletionStatus string

const (
	MetadataCompletionStatusIncomplete    MetadataCompletionStatus = "incomplete"
	MetadataCompletionStatusFilled        MetadataCompletionStatus = "filled"
	MetadataCompletionStatusEmptyOptional MetadataCompletionStatus = "empty_optional"
)

type MetadataRuleStrategy string

const (
	MetadataRuleStrategySourceMapping MetadataRuleStrategy = "source_mapping"
	MetadataRuleStrategyLLMExtract    MetadataRuleStrategy = "llm_extract"
)

type MetadataOperator string

const (
	MetadataOperatorEquals      MetadataOperator = "equals"
	MetadataOperatorContains    MetadataOperator = "contains"
	MetadataOperatorIn          MetadataOperator = "in"
	MetadataOperatorContainsAny MetadataOperator = "contains_any"
	MetadataOperatorContainsAll MetadataOperator = "contains_all"
	MetadataOperatorEqual       MetadataOperator = "eq"
	MetadataOperatorGreaterThan MetadataOperator = "gt"
	MetadataOperatorGTE         MetadataOperator = "gte"
	MetadataOperatorLessThan    MetadataOperator = "lt"
	MetadataOperatorLTE         MetadataOperator = "lte"
	MetadataOperatorBetween     MetadataOperator = "between"
	MetadataOperatorOn          MetadataOperator = "on"
	MetadataOperatorBefore      MetadataOperator = "before"
	MetadataOperatorAfter       MetadataOperator = "after"
	MetadataOperatorIsEmpty     MetadataOperator = "is_empty"
	MetadataOperatorIsNotEmpty  MetadataOperator = "is_not_empty"
)

type DocumentScopeMode string

const (
	DocumentScopeModeAll  DocumentScopeMode = "all"
	DocumentScopeModeNone DocumentScopeMode = "none"
	DocumentScopeModeIDs  DocumentScopeMode = "ids"
)

type ConfigureMetadataOption struct {
	ID        string
	Label     string
	Status    MetadataStatus
	SortOrder int
}

type ConfigureMetadataDefinition struct {
	TenantID        uint64
	KnowledgeBaseID string
	DefinitionID    string
	Name            string
	Description     string
	ValueType       MetadataValueType
	Required        bool
	Filterable      bool
	SortOrder       int
	Options         []ConfigureMetadataOption
}

type ConfigureMetadataAutoRule struct {
	TenantID        uint64
	KnowledgeBaseID string
	DefinitionID    string
	Strategy        MetadataRuleStrategy
	Config          JSONMap
}

type MetadataValueChange struct {
	MetadataDefinitionID string
	Value                any
	ValueSet             bool
	AllowAutoOverwrite   *bool
	ExpectedVersion      *int
}

type ChangeDocumentMetadata struct {
	TenantID    uint64
	KnowledgeID string
	UpdatedBy   string
	Changes     []MetadataValueChange
}

type ConfirmDocumentMetadata struct {
	TenantID              uint64
	KnowledgeID           string
	MetadataDefinitionIDs []string
}

type AutomaticMetadataResult struct {
	MetadataDefinitionID string
	Value                any
	AutoRuleID           string
	AutoRuleRevision     int
	ExpectedVersion      *int
}

type ApplyAutomaticMetadataResults struct {
	TenantID        uint64
	KnowledgeBaseID string
	KnowledgeID     string
	Results         []AutomaticMetadataResult
}

type ApplyAutomaticMetadataReport struct {
	Applied int `json:"applied"`
	Skipped int `json:"skipped"`
	Invalid int `json:"invalid"`
}

type MetadataCondition struct {
	MetadataDefinitionID string           `json:"metadata_definition_id"`
	Operator             MetadataOperator `json:"operator"`
	Values               []any            `json:"values"`
}

type KBMetadataFilter struct {
	KnowledgeBaseID string              `json:"knowledge_base_id"`
	Conditions      []MetadataCondition `json:"conditions"`
}

type MetadataScopeQuery struct {
	TenantID             uint64
	KnowledgeBaseID      string
	Conditions           []MetadataCondition
	ExplicitKnowledgeIDs []string
}

type DocumentScope struct {
	Mode DocumentScopeMode `json:"mode"`
	IDs  []string          `json:"ids,omitempty"`
}

type MetadataSchema struct {
	KnowledgeBaseID string                `json:"knowledge_base_id"`
	Definitions     []*MetadataDefinition `json:"definitions"`
}

type DocumentMetadataField struct {
	Definition       *MetadataDefinition      `json:"definition"`
	Value            *MetadataValue           `json:"value,omitempty"`
	CompletionStatus MetadataCompletionStatus `json:"completion_status"`
}

type DocumentMetadata struct {
	KnowledgeID     string                  `json:"knowledge_id"`
	Values          []DocumentMetadataField `json:"values"`
	IncompleteCount int                     `json:"incomplete_count"`
}

type MetadataDefinition struct {
	ID              string            `json:"id"                gorm:"type:varchar(36);primaryKey"`
	TenantID        uint64            `json:"tenant_id"         gorm:"not null;uniqueIndex:idx_metadata_definitions_kb_name,priority:1;index:idx_metadata_definitions_kb_status_sort,priority:1"`
	KnowledgeBaseID string            `json:"knowledge_base_id" gorm:"type:varchar(36);not null;uniqueIndex:idx_metadata_definitions_kb_name,priority:2;index:idx_metadata_definitions_kb_status_sort,priority:2"`
	Name            string            `json:"name"              gorm:"type:varchar(128);not null"`
	NormalizedName  string            `json:"-"                 gorm:"type:varchar(128);not null;uniqueIndex:idx_metadata_definitions_kb_name,priority:3"`
	Description     string            `json:"desc"              gorm:"type:text"`
	ValueType       MetadataValueType `json:"value_type"        gorm:"type:varchar(32);not null"`
	Required        bool              `json:"required"          gorm:"not null;default:false"`
	Filterable      bool              `json:"filterable"        gorm:"not null;default:false"`
	Status          MetadataStatus    `json:"status"            gorm:"type:varchar(16);not null;default:active;index:idx_metadata_definitions_kb_status_sort,priority:3"`
	SortOrder       int               `json:"sort_order"        gorm:"not null;default:0;index:idx_metadata_definitions_kb_status_sort,priority:4"`
	Options         []MetadataOption  `json:"options,omitempty" gorm:"foreignKey:MetadataDefinitionID"`
	AutoRule        *MetadataAutoRule `json:"auto_rule,omitempty" gorm:"foreignKey:MetadataDefinitionID"`
	TypeLocked      bool              `json:"type_locked"         gorm:"-"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

func (MetadataDefinition) TableName() string { return "knowledge_metadata_definitions" }

func (d *MetadataDefinition) BeforeCreate(_ *gorm.DB) error {
	if d.ID == "" {
		d.ID = uuid.New().String()
	}
	return nil
}

type MetadataOption struct {
	ID                   string         `json:"id"                     gorm:"type:varchar(36);primaryKey"`
	MetadataDefinitionID string         `json:"metadata_definition_id" gorm:"type:varchar(36);not null;uniqueIndex:idx_metadata_options_definition_label,priority:1;index"`
	Label                string         `json:"label"                  gorm:"type:varchar(128);not null"`
	NormalizedLabel      string         `json:"-"                      gorm:"type:varchar(128);not null;uniqueIndex:idx_metadata_options_definition_label,priority:2"`
	Status               MetadataStatus `json:"status"                 gorm:"type:varchar(16);not null;default:active"`
	SortOrder            int            `json:"sort_order"             gorm:"not null;default:0"`
	CreatedAt            time.Time      `json:"created_at"`
	UpdatedAt            time.Time      `json:"updated_at"`
}

func (MetadataOption) TableName() string { return "knowledge_metadata_options" }

func (o *MetadataOption) BeforeCreate(_ *gorm.DB) error {
	if o.ID == "" {
		o.ID = uuid.New().String()
	}
	return nil
}

type MetadataValue struct {
	ID                   string               `json:"id"                     gorm:"type:varchar(36);primaryKey"`
	TenantID             uint64               `json:"tenant_id"              gorm:"not null;index:idx_metadata_values_scope,priority:1"`
	KnowledgeBaseID      string               `json:"knowledge_base_id"      gorm:"type:varchar(36);not null;index:idx_metadata_values_scope,priority:2"`
	KnowledgeID          string               `json:"knowledge_id"           gorm:"type:varchar(36);not null;uniqueIndex:idx_metadata_values_knowledge_definition,priority:1;index:idx_metadata_values_scope,priority:3"`
	MetadataDefinitionID string               `json:"metadata_definition_id" gorm:"type:varchar(36);not null;uniqueIndex:idx_metadata_values_knowledge_definition,priority:2;index"`
	TextValue            *string              `json:"text_value,omitempty"   gorm:"type:text"`
	NumberValue          *float64             `json:"number_value,omitempty"`
	DateValue            *time.Time           `json:"date_value,omitempty"   gorm:"type:date"`
	BoolValue            *bool                `json:"bool_value,omitempty"`
	Source               MetadataValueSource  `json:"source"                 gorm:"type:varchar(16);not null"`
	ReviewStatus         MetadataReviewStatus `json:"review_status"          gorm:"type:varchar(16);not null"`
	AllowAutoOverwrite   bool                 `json:"allow_auto_overwrite"   gorm:"not null;default:false"`
	Version              int                  `json:"version"                gorm:"not null;default:1"`
	AutoRuleID           *string              `json:"auto_rule_id,omitempty" gorm:"type:varchar(36)"`
	AutoRuleRevision     *int                 `json:"auto_rule_revision,omitempty"`
	UpdatedBy            *string              `json:"updated_by,omitempty"   gorm:"type:varchar(36)"`
	OptionIDs            []string             `json:"option_ids,omitempty"   gorm:"-"`
	Value                any                  `json:"value"                  gorm:"-"`
	CreatedAt            time.Time            `json:"created_at"`
	UpdatedAt            time.Time            `json:"updated_at"`
}

func (MetadataValue) TableName() string { return "knowledge_metadata_values" }

func (v *MetadataValue) BeforeCreate(_ *gorm.DB) error {
	if v.ID == "" {
		v.ID = uuid.New().String()
	}
	if v.Version == 0 {
		v.Version = 1
	}
	return nil
}

func (v *MetadataValue) SetTypedValue(valueType MetadataValueType, input any) error {
	if input == nil {
		v.clearTypedValue()
		return nil
	}

	next := MetadataValue{}
	switch valueType {
	case MetadataValueTypeText:
		value, ok := input.(string)
		if !ok {
			return invalidMetadataValue(valueType)
		}
		next.TextValue = &value
	case MetadataValueTypeSingleSelect:
		value, ok := input.(string)
		if !ok {
			return invalidMetadataValue(valueType)
		}
		next.OptionIDs = []string{value}
	case MetadataValueTypeMultiSelect:
		values, err := metadataStringSlice(input)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidMetadataValue, err)
		}
		next.OptionIDs = values
	case MetadataValueTypeNumber:
		value, ok := metadataNumber(input)
		if !ok {
			return invalidMetadataValue(valueType)
		}
		next.NumberValue = &value
	case MetadataValueTypeDate:
		raw, ok := input.(string)
		if !ok {
			return invalidMetadataValue(valueType)
		}
		value, err := time.Parse(time.DateOnly, raw)
		if err != nil || value.Format(time.DateOnly) != raw {
			return invalidMetadataValue(valueType)
		}
		next.DateValue = &value
	case MetadataValueTypeBoolean:
		value, ok := input.(bool)
		if !ok {
			return invalidMetadataValue(valueType)
		}
		next.BoolValue = &value
	default:
		return invalidMetadataValue(valueType)
	}

	v.TextValue = next.TextValue
	v.NumberValue = next.NumberValue
	v.DateValue = next.DateValue
	v.BoolValue = next.BoolValue
	v.OptionIDs = next.OptionIDs
	return nil
}

func (v *MetadataValue) TypedValue(valueType MetadataValueType) any {
	switch valueType {
	case MetadataValueTypeText:
		if v.TextValue != nil {
			return *v.TextValue
		}
	case MetadataValueTypeSingleSelect:
		if len(v.OptionIDs) > 0 {
			return v.OptionIDs[0]
		}
	case MetadataValueTypeMultiSelect:
		if len(v.OptionIDs) > 0 {
			return append([]string(nil), v.OptionIDs...)
		}
	case MetadataValueTypeNumber:
		if v.NumberValue != nil {
			return *v.NumberValue
		}
	case MetadataValueTypeDate:
		if v.DateValue != nil {
			return v.DateValue.Format(time.DateOnly)
		}
	case MetadataValueTypeBoolean:
		if v.BoolValue != nil {
			return *v.BoolValue
		}
	}
	return nil
}

func (v *MetadataValue) HasValue(valueType MetadataValueType) bool {
	switch valueType {
	case MetadataValueTypeText:
		return v.TextValue != nil && strings.TrimSpace(*v.TextValue) != ""
	case MetadataValueTypeSingleSelect, MetadataValueTypeMultiSelect:
		return len(v.OptionIDs) > 0
	case MetadataValueTypeNumber:
		return v.NumberValue != nil
	case MetadataValueTypeDate:
		return v.DateValue != nil
	case MetadataValueTypeBoolean:
		return v.BoolValue != nil
	default:
		return false
	}
}

func (v *MetadataValue) HasAnyScalarValue() bool {
	return v.TextValue != nil || v.NumberValue != nil || v.DateValue != nil || v.BoolValue != nil
}

func (v *MetadataValue) clearTypedValue() {
	v.TextValue = nil
	v.NumberValue = nil
	v.DateValue = nil
	v.BoolValue = nil
	v.OptionIDs = nil
}

func invalidMetadataValue(valueType MetadataValueType) error {
	return fmt.Errorf("%w for type %q", ErrInvalidMetadataValue, valueType)
}

func metadataStringSlice(input any) ([]string, error) {
	switch values := input.(type) {
	case []string:
		return append([]string(nil), values...), nil
	case []any:
		result := make([]string, 0, len(values))
		for _, item := range values {
			value, ok := item.(string)
			if !ok {
				return nil, errors.New("multi_select values must be strings")
			}
			result = append(result, value)
		}
		return result, nil
	default:
		return nil, errors.New("multi_select value must be an array")
	}
}

func metadataNumber(input any) (float64, bool) {
	switch value := input.(type) {
	case float64:
		return value, true
	case float32:
		return float64(value), true
	case int:
		return float64(value), true
	case int8:
		return float64(value), true
	case int16:
		return float64(value), true
	case int32:
		return float64(value), true
	case int64:
		return float64(value), true
	case uint:
		return float64(value), true
	case uint8:
		return float64(value), true
	case uint16:
		return float64(value), true
	case uint32:
		return float64(value), true
	case uint64:
		return float64(value), true
	case json.Number:
		result, err := value.Float64()
		return result, err == nil
	default:
		return 0, false
	}
}

type MetadataValueOption struct {
	MetadataValueID string `json:"metadata_value_id" gorm:"type:varchar(36);primaryKey"`
	OptionID        string `json:"option_id"         gorm:"type:varchar(36);primaryKey;index"`
	SortOrder       int    `json:"sort_order"       gorm:"not null;default:0"`
}

func (MetadataValueOption) TableName() string { return "knowledge_metadata_value_options" }

type MetadataAutoRule struct {
	ID                   string               `json:"id"                     gorm:"type:varchar(36);primaryKey"`
	TenantID             uint64               `json:"tenant_id"              gorm:"not null;index:idx_metadata_auto_rules_scope,priority:1"`
	KnowledgeBaseID      string               `json:"knowledge_base_id"      gorm:"type:varchar(36);not null;index:idx_metadata_auto_rules_scope,priority:2"`
	MetadataDefinitionID string               `json:"metadata_definition_id" gorm:"type:varchar(36);not null;index;uniqueIndex:idx_metadata_auto_rules_enabled_definition,where:enabled = true"`
	Strategy             MetadataRuleStrategy `json:"strategy"               gorm:"type:varchar(32);not null"`
	Config               JSONMap              `json:"config"                 gorm:"type:json"`
	Revision             int                  `json:"revision"               gorm:"not null;default:1"`
	Enabled              bool                 `json:"enabled"                gorm:"not null;default:true"`
	CreatedAt            time.Time            `json:"created_at"`
	UpdatedAt            time.Time            `json:"updated_at"`
}

func (MetadataAutoRule) TableName() string { return "knowledge_metadata_auto_rules" }

func (r *MetadataAutoRule) BeforeCreate(_ *gorm.DB) error {
	if r.ID == "" {
		r.ID = uuid.New().String()
	}
	if r.Revision == 0 {
		r.Revision = 1
	}
	return nil
}

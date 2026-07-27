package types

import (
	"reflect"
	"sort"
	"strings"
)

type AgentCollectionFieldType string

const (
	AgentCollectionSingleChoice   AgentCollectionFieldType = "single_choice"
	AgentCollectionMultipleChoice AgentCollectionFieldType = "multiple_choice"
	AgentCollectionShortText      AgentCollectionFieldType = "short_text"
	AgentCollectionLongText       AgentCollectionFieldType = "long_text"
	AgentCollectionNumber         AgentCollectionFieldType = "number"
	AgentCollectionDate           AgentCollectionFieldType = "date"

	DefaultCollectionExtractionThreshold = 0.85
	MaxAgentCollectionFields             = 100
	MaxAgentCollectionOptions            = 50
)

type AgentCollectionOption struct {
	ID    string `json:"id" yaml:"id"`
	Label string `json:"label" yaml:"label"`
}

type AgentCollectionValidation struct {
	MinLength *int     `json:"min_length,omitempty" yaml:"min_length,omitempty"`
	MaxLength *int     `json:"max_length,omitempty" yaml:"max_length,omitempty"`
	MinNumber *float64 `json:"min_number,omitempty" yaml:"min_number,omitempty"`
	MaxNumber *float64 `json:"max_number,omitempty" yaml:"max_number,omitempty"`
	MinDate   string   `json:"min_date,omitempty" yaml:"min_date,omitempty"`
	MaxDate   string   `json:"max_date,omitempty" yaml:"max_date,omitempty"`
}

type AgentCollectionCondition struct {
	Field    string `json:"field" yaml:"field"`
	Operator string `json:"operator" yaml:"operator"`
	Value    any    `json:"value,omitempty" yaml:"value,omitempty"`
}

type AgentCollectionField struct {
	Key         string                    `json:"key" yaml:"key"`
	Label       string                    `json:"label" yaml:"label"`
	Description string                    `json:"description,omitempty" yaml:"description,omitempty"`
	Type        AgentCollectionFieldType  `json:"type" yaml:"type"`
	Required    bool                      `json:"required" yaml:"required"`
	Enabled     bool                      `json:"enabled" yaml:"enabled"`
	Order       int                       `json:"order" yaml:"order"`
	Options     []AgentCollectionOption   `json:"options,omitempty" yaml:"options,omitempty"`
	Validation  AgentCollectionValidation `json:"validation,omitempty" yaml:"validation,omitempty"`
	VisibleWhen *AgentCollectionCondition `json:"visible_when,omitempty" yaml:"visible_when,omitempty"`
}

func VisibleCollectionFields(fields []AgentCollectionField, values JSONMap) []AgentCollectionField {
	result := make([]AgentCollectionField, 0, len(fields))
	for _, field := range fields {
		if field.Enabled && collectionConditionMatches(field.VisibleWhen, values) {
			result = append(result, field)
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].Order < result[j].Order })
	return result
}

func collectionConditionMatches(condition *AgentCollectionCondition, values JSONMap) bool {
	if condition == nil {
		return true
	}
	actual, exists := collectionRawValue(values[condition.Field])
	switch condition.Operator {
	case "equals":
		return exists && reflect.DeepEqual(actual, condition.Value)
	case "not_equals":
		return !exists || !reflect.DeepEqual(actual, condition.Value)
	case "contains":
		return exists && collectionContains(actual, condition.Value)
	case "not_empty":
		return exists && !collectionValueEmpty(actual)
	case "empty":
		return !exists || collectionValueEmpty(actual)
	default:
		return false
	}
}

func collectionRawValue(value any) (any, bool) {
	if value == nil {
		return nil, false
	}
	if entry, ok := value.(map[string]any); ok {
		value, ok = entry["value"]
		return value, ok && value != nil
	}
	return value, true
}

func collectionContains(actual, expected any) bool {
	switch value := actual.(type) {
	case string:
		text, ok := expected.(string)
		return ok && strings.Contains(value, text)
	case []string:
		for _, item := range value {
			if reflect.DeepEqual(item, expected) {
				return true
			}
		}
	case []any:
		for _, item := range value {
			if reflect.DeepEqual(item, expected) {
				return true
			}
		}
	}
	return false
}

func collectionValueEmpty(value any) bool {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed) == ""
	case []string:
		return len(typed) == 0
	case []any:
		return len(typed) == 0
	}
	return value == nil
}

package types

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	collectionKeyPattern       = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)
	collectionSensitivePattern = regexp.MustCompile(`(?i)(password|passcode|token|secret|private[_ -]?key|api[_ -]?key|otp|密码|口令|令牌|私钥|验证码|密钥)`)
)

func NormalizeAgentCollectionConfig(cfg *CustomAgentConfig) {
	if cfg == nil {
		return
	}
	cfg.CollectionGoal = strings.TrimSpace(cfg.CollectionGoal)
	if cfg.CollectionExtractionThreshold == 0 {
		cfg.CollectionExtractionThreshold = DefaultCollectionExtractionThreshold
	}
	for index := range cfg.CollectionFields {
		field := &cfg.CollectionFields[index]
		field.Key = strings.TrimSpace(field.Key)
		field.Label = strings.TrimSpace(field.Label)
		field.Description = strings.TrimSpace(field.Description)
		field.Validation.MinDate = strings.TrimSpace(field.Validation.MinDate)
		field.Validation.MaxDate = strings.TrimSpace(field.Validation.MaxDate)
		for optionIndex := range field.Options {
			field.Options[optionIndex].ID = strings.TrimSpace(field.Options[optionIndex].ID)
			field.Options[optionIndex].Label = strings.TrimSpace(field.Options[optionIndex].Label)
		}
		if field.VisibleWhen != nil {
			field.VisibleWhen.Field = strings.TrimSpace(field.VisibleWhen.Field)
			field.VisibleWhen.Operator = strings.ToLower(strings.TrimSpace(field.VisibleWhen.Operator))
		}
	}
}

func ValidateAgentCollectionConfig(cfg CustomAgentConfig) error {
	if cfg.CollectionExtractionThreshold < 0 || cfg.CollectionExtractionThreshold > 1 ||
		(cfg.CollectionEnabled && cfg.CollectionExtractionThreshold == 0) {
		return fmt.Errorf("collection extraction threshold must be greater than 0 and at most 1")
	}
	if cfg.CollectionSchemaVersion < 0 {
		return fmt.Errorf("collection schema version cannot be negative")
	}
	if err := validateCollectionFieldCount(cfg.CollectionFields); err != nil {
		return err
	}
	fields := make(map[string]AgentCollectionField, len(cfg.CollectionFields))
	for _, field := range cfg.CollectionFields {
		if _, exists := fields[field.Key]; exists {
			return fmt.Errorf("collection field keys must be unique: %q", field.Key)
		}
		if err := validateCollectionField(field); err != nil {
			return fmt.Errorf("collection field %q: %w", field.Key, err)
		}
		fields[field.Key] = field
	}
	for _, field := range cfg.CollectionFields {
		if err := validateCollectionCondition(field, fields); err != nil {
			return err
		}
	}
	return nil
}

func validateCollectionFieldCount(fields []AgentCollectionField) error {
	enabled := 0
	for _, field := range fields {
		if field.Enabled {
			enabled++
		}
	}
	if enabled > MaxAgentCollectionFields {
		return fmt.Errorf("collection fields cannot contain more than %d enabled entries", MaxAgentCollectionFields)
	}
	return nil
}

func validateCollectionField(field AgentCollectionField) error {
	if !collectionKeyPattern.MatchString(field.Key) {
		return fmt.Errorf("key must be an ASCII identifier with at most 64 characters")
	}
	if collectionSensitivePattern.MatchString(field.Key + " " + field.Label + " " + field.Description) {
		return fmt.Errorf("key, label, or description contains a sensitive credential term")
	}
	if field.Label == "" || utf8.RuneCountInString(field.Label) > 500 {
		return fmt.Errorf("label must contain 1 to 500 characters")
	}
	if utf8.RuneCountInString(field.Description) > 1000 {
		return fmt.Errorf("description cannot exceed 1000 characters")
	}
	if !validCollectionFieldType(field.Type) {
		return fmt.Errorf("unsupported type %q", field.Type)
	}
	if err := validateCollectionOptions(field); err != nil {
		return err
	}
	return validateCollectionRules(field)
}

func validCollectionFieldType(fieldType AgentCollectionFieldType) bool {
	switch fieldType {
	case AgentCollectionSingleChoice, AgentCollectionMultipleChoice, AgentCollectionShortText,
		AgentCollectionLongText, AgentCollectionNumber, AgentCollectionDate:
		return true
	default:
		return false
	}
}

func validateCollectionOptions(field AgentCollectionField) error {
	isChoice := field.Type == AgentCollectionSingleChoice || field.Type == AgentCollectionMultipleChoice
	if !isChoice && len(field.Options) > 0 {
		return fmt.Errorf("options are only allowed for choice fields")
	}
	if isChoice && (len(field.Options) < 2 || len(field.Options) > MaxAgentCollectionOptions) {
		return fmt.Errorf("options must contain between 2 and %d entries", MaxAgentCollectionOptions)
	}
	seen := make(map[string]struct{}, len(field.Options))
	for _, option := range field.Options {
		if !collectionKeyPattern.MatchString(option.ID) || option.Label == "" {
			return fmt.Errorf("option ids and labels must be valid")
		}
		if _, exists := seen[option.ID]; exists {
			return fmt.Errorf("option ids must be unique")
		}
		seen[option.ID] = struct{}{}
	}
	return nil
}

func validateCollectionRules(field AgentCollectionField) error {
	rules := field.Validation
	if rules.MinLength != nil && (*rules.MinLength < 0 || rules.MaxLength != nil && *rules.MinLength > *rules.MaxLength) {
		return fmt.Errorf("invalid text length range")
	}
	if rules.MaxLength != nil && *rules.MaxLength < 0 {
		return fmt.Errorf("invalid text length range")
	}
	if rules.MinNumber != nil && rules.MaxNumber != nil && *rules.MinNumber > *rules.MaxNumber {
		return fmt.Errorf("invalid number range")
	}
	minDate, err := parseOptionalCollectionDate(rules.MinDate)
	if err != nil {
		return fmt.Errorf("invalid minimum date")
	}
	maxDate, err := parseOptionalCollectionDate(rules.MaxDate)
	if err != nil {
		return fmt.Errorf("invalid maximum date")
	}
	if !minDate.IsZero() && !maxDate.IsZero() && minDate.After(maxDate) {
		return fmt.Errorf("invalid date range")
	}
	return nil
}

func validateCollectionCondition(field AgentCollectionField, fields map[string]AgentCollectionField) error {
	condition := field.VisibleWhen
	if condition == nil {
		return nil
	}
	referenced, exists := fields[condition.Field]
	if !exists {
		return fmt.Errorf("collection field %q references unknown field %q", field.Key, condition.Field)
	}
	if condition.Field == field.Key {
		return fmt.Errorf("collection field %q cannot reference itself", field.Key)
	}
	if referenced.Order >= field.Order {
		return fmt.Errorf("collection field %q must reference an earlier field", field.Key)
	}
	switch condition.Operator {
	case "equals", "not_equals", "contains", "not_empty", "empty":
	default:
		return fmt.Errorf("collection field %q has unsupported condition operator %q", field.Key, condition.Operator)
	}
	if condition.Operator != "empty" && condition.Operator != "not_empty" {
		if err := ValidateCollectionValue(referenced, condition.Value); err != nil {
			return fmt.Errorf("collection field %q has invalid condition value: %w", field.Key, err)
		}
	}
	return nil
}

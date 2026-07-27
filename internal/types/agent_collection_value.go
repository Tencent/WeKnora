package types

import (
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"
)

func ValidateCollectionValue(field AgentCollectionField, value any) error {
	switch field.Type {
	case AgentCollectionSingleChoice:
		text, ok := value.(string)
		if !ok || !collectionOptionAllowed(field.Options, text) {
			return fmt.Errorf("value must be a configured option")
		}
	case AgentCollectionMultipleChoice:
		if err := validateCollectionChoices(field, value); err != nil {
			return err
		}
	case AgentCollectionShortText, AgentCollectionLongText:
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("value must be text")
		}
		if err := validateCollectionTextLength(field, utf8.RuneCountInString(strings.TrimSpace(text))); err != nil {
			return err
		}
	case AgentCollectionNumber:
		if err := validateCollectionNumber(field, value); err != nil {
			return err
		}
	case AgentCollectionDate:
		if err := validateCollectionDate(field, value); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported collection field type %q", field.Type)
	}
	return nil
}

func validateCollectionChoices(field AgentCollectionField, value any) error {
	values, ok := collectionStringSlice(value)
	if !ok || len(values) == 0 {
		return fmt.Errorf("value must contain configured options")
	}
	seen := make(map[string]struct{}, len(values))
	for _, item := range values {
		if !collectionOptionAllowed(field.Options, item) {
			return fmt.Errorf("value contains an unknown option")
		}
		if _, exists := seen[item]; exists {
			return fmt.Errorf("value contains duplicate options")
		}
		seen[item] = struct{}{}
	}
	return nil
}

func validateCollectionTextLength(field AgentCollectionField, length int) error {
	if length == 0 {
		return fmt.Errorf("value cannot be empty")
	}
	if field.Validation.MinLength != nil && length < *field.Validation.MinLength {
		return fmt.Errorf("value is shorter than minimum")
	}
	if field.Validation.MaxLength != nil && length > *field.Validation.MaxLength {
		return fmt.Errorf("value exceeds maximum length")
	}
	hardMax := 500
	if field.Type == AgentCollectionLongText {
		hardMax = 5000
	}
	if length > hardMax {
		return fmt.Errorf("value exceeds hard maximum length")
	}
	return nil
}

func validateCollectionNumber(field AgentCollectionField, value any) error {
	number, ok := collectionNumber(value)
	if !ok || math.IsInf(number, 0) || math.IsNaN(number) {
		return fmt.Errorf("value must be a finite number")
	}
	if field.Validation.MinNumber != nil && number < *field.Validation.MinNumber {
		return fmt.Errorf("value is below minimum")
	}
	if field.Validation.MaxNumber != nil && number > *field.Validation.MaxNumber {
		return fmt.Errorf("value exceeds maximum")
	}
	return nil
}

func validateCollectionDate(field AgentCollectionField, value any) error {
	text, ok := value.(string)
	if !ok {
		return fmt.Errorf("value must be an ISO date")
	}
	date, err := time.Parse("2006-01-02", text)
	if err != nil {
		return fmt.Errorf("value must be an ISO date")
	}
	minDate, _ := parseOptionalCollectionDate(field.Validation.MinDate)
	maxDate, _ := parseOptionalCollectionDate(field.Validation.MaxDate)
	if !minDate.IsZero() && date.Before(minDate) {
		return fmt.Errorf("value is before minimum date")
	}
	if !maxDate.IsZero() && date.After(maxDate) {
		return fmt.Errorf("value is after maximum date")
	}
	return nil
}

func parseOptionalCollectionDate(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	return time.Parse("2006-01-02", value)
}

func collectionOptionAllowed(options []AgentCollectionOption, value string) bool {
	for _, option := range options {
		if option.ID == value {
			return true
		}
	}
	return false
}

func collectionStringSlice(value any) ([]string, bool) {
	if values, ok := value.([]string); ok {
		return values, true
	}
	items, ok := value.([]any)
	if !ok {
		return nil, false
	}
	values := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			return nil, false
		}
		values = append(values, text)
	}
	return values, true
}

func collectionNumber(value any) (float64, bool) {
	switch number := value.(type) {
	case float64:
		return number, true
	case float32:
		return float64(number), true
	case int:
		return float64(number), true
	case int64:
		return float64(number), true
	case int32:
		return float64(number), true
	default:
		return 0, false
	}
}

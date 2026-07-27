package userinput

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
)

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// ValidateQuestion enforces both the legacy ask_user and collection contracts.
func ValidateQuestion(q Question) error {
	question := strings.TrimSpace(q.Text)
	if question == "" || utf8.RuneCountInString(question) > 500 {
		return fmt.Errorf("question must contain 1 to 500 characters")
	}
	if !validQuestionMode(q.Mode) {
		return fmt.Errorf("mode is not supported")
	}
	if !identifierPattern.MatchString(q.GroupID) {
		return fmt.Errorf("question_group_id must be a valid identifier")
	}
	if q.Total < 1 || q.Total > types.MaxAgentCollectionFields {
		return fmt.Errorf("question_total must be between 1 and %d", types.MaxAgentCollectionFields)
	}
	if q.Index < 1 || q.Index > q.Total {
		return fmt.Errorf("question_index must be between 1 and question_total")
	}
	if q.CompletedCount < 0 || q.RemainingCount < 0 {
		return fmt.Errorf("question progress cannot be negative")
	}
	if err := validateQuestionFieldMetadata(q); err != nil {
		return err
	}
	if questionChoiceMode(q.Mode) {
		maxOptions := 8
		if q.FieldKey != "" {
			maxOptions = types.MaxAgentCollectionOptions
		}
		return validateQuestionOptions(q.Options, maxOptions)
	}
	if len(q.Options) != 0 || q.AllowOther {
		return fmt.Errorf("non-choice questions cannot contain options or allow other_text")
	}
	return nil
}

func validQuestionMode(mode Mode) bool {
	switch mode {
	case ModeSingle, ModeMultiple, ModeShortText, ModeLongText, ModeNumber, ModeDate:
		return true
	default:
		return false
	}
}

func questionChoiceMode(mode Mode) bool { return mode == ModeSingle || mode == ModeMultiple }

func validateQuestionFieldMetadata(q Question) error {
	if q.FieldKey == "" && q.SchemaVersion == 0 {
		return nil
	}
	if !identifierPattern.MatchString(q.FieldKey) {
		return fmt.Errorf("field_key must be a valid identifier")
	}
	if q.SchemaVersion < 1 {
		return fmt.Errorf("schema_version must be positive for collection questions")
	}
	return nil
}

func validateQuestionOptions(options []Option, maxOptions int) error {
	if len(options) < 2 || len(options) > maxOptions {
		return fmt.Errorf("options must contain between 2 and %d entries", maxOptions)
	}
	seen := make(map[string]struct{}, len(options))
	for _, option := range options {
		if !identifierPattern.MatchString(option.ID) {
			return fmt.Errorf("option id %q must be a valid identifier", option.ID)
		}
		if _, exists := seen[option.ID]; exists {
			return fmt.Errorf("option ids must be unique")
		}
		seen[option.ID] = struct{}{}
		label := strings.TrimSpace(option.Label)
		if label == "" || utf8.RuneCountInString(label) > 120 {
			return fmt.Errorf("option label must contain 1 to 120 characters")
		}
		if utf8.RuneCountInString(option.Description) > 300 {
			return fmt.Errorf("option description cannot exceed 300 characters")
		}
	}
	return nil
}

// ValidateAnswer checks a submitted answer against trusted pending metadata.
func ValidateAnswer(q Question, answer Answer) error {
	if err := validateAnswerMetadata(q, answer); err != nil {
		return err
	}
	other := strings.TrimSpace(answer.OtherText)
	if answer.Skipped {
		if !q.AllowSkip {
			return fmt.Errorf("skip is not allowed for this question")
		}
		if len(answer.SelectedOptionIDs) > 0 || other != "" || answer.Value != nil {
			return fmt.Errorf("skip cannot be combined with a selection, value, or other_text")
		}
		return nil
	}
	if questionChoiceMode(q.Mode) {
		if answer.Value != nil {
			return fmt.Errorf("choice answers cannot contain value")
		}
		return validateChoiceAnswer(q, answer, other)
	}
	if len(answer.SelectedOptionIDs) > 0 || other != "" {
		return fmt.Errorf("non-choice answers cannot contain a selection or other_text")
	}
	if answer.Value == nil {
		return fmt.Errorf("answer must include a value or skip")
	}
	return validateTypedAnswer(q, answer.Value)
}

func validateAnswerMetadata(q Question, answer Answer) error {
	if q.FieldKey == "" {
		if answer.FieldKey != "" || answer.SchemaVersion != 0 {
			return fmt.Errorf("legacy answers cannot contain field metadata")
		}
		return nil
	}
	if answer.FieldKey != q.FieldKey {
		return fmt.Errorf("field_key does not match pending question")
	}
	if answer.SchemaVersion != q.SchemaVersion {
		return fmt.Errorf("schema_version does not match pending question")
	}
	return nil
}

func validateChoiceAnswer(q Question, answer Answer, other string) error {
	if other != "" {
		if !q.AllowOther {
			return fmt.Errorf("other_text is not allowed for this question")
		}
		if utf8.RuneCountInString(other) > 1000 {
			return fmt.Errorf("other_text cannot exceed 1000 characters")
		}
	}
	if len(answer.SelectedOptionIDs) == 0 && other == "" {
		return fmt.Errorf("answer must include a selection, other_text, or skip")
	}
	allowed := make(map[string]struct{}, len(q.Options))
	for _, option := range q.Options {
		allowed[option.ID] = struct{}{}
	}
	selected := make(map[string]struct{}, len(answer.SelectedOptionIDs))
	for _, id := range answer.SelectedOptionIDs {
		if _, exists := allowed[id]; !exists {
			return fmt.Errorf("unknown option id %q", id)
		}
		if _, exists := selected[id]; exists {
			return fmt.Errorf("duplicate selected option id %q", id)
		}
		selected[id] = struct{}{}
	}
	if q.Mode == ModeSingle && len(answer.SelectedOptionIDs) > 1 {
		return fmt.Errorf("single_choice accepts at most one selected option")
	}
	if q.Mode == ModeSingle && len(answer.SelectedOptionIDs) == 1 && other != "" {
		return fmt.Errorf("single_choice cannot combine a predefined option with other_text")
	}
	return nil
}

func validateTypedAnswer(q Question, value any) error {
	fieldType := map[Mode]types.AgentCollectionFieldType{
		ModeShortText: types.AgentCollectionShortText, ModeLongText: types.AgentCollectionLongText,
		ModeNumber: types.AgentCollectionNumber, ModeDate: types.AgentCollectionDate,
	}[q.Mode]
	field := types.AgentCollectionField{Key: q.FieldKey, Type: fieldType, Validation: q.Validation}
	return types.ValidateCollectionValue(field, value)
}

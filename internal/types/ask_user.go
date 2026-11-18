package types

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"
)

// Limits on an ask_user question. The model writes the question, so its size
// is bounded to keep the card readable and the stored row small.
const (
	AskUserMaxQuestionLen = 2000
	AskUserMaxOptions     = 10
	AskUserMaxOptionLen   = 200
	AskUserMaxFields      = 8
	AskUserMaxNameLen     = 64
	AskUserMaxTextLen     = 4000
)

// Field types an ask_user form may contain.
const (
	AskUserFieldText        = "text"
	AskUserFieldNumber      = "number"
	AskUserFieldSelect      = "select"
	AskUserFieldMultiSelect = "multiselect"
)

// AskUserField is one input of an ask_user form.
type AskUserField struct {
	Name     string   `json:"name"`
	Label    string   `json:"label,omitempty"`
	Type     string   `json:"type"`
	Required bool     `json:"required,omitempty"`
	Options  []string `json:"options,omitempty"`
}

// AskUserPromptRequest is the Request of an AgentPromptKindAskUser: a
// question with either choices, a small form, or neither (free text).
type AskUserPromptRequest struct {
	Question      string         `json:"question"`
	Options       []string       `json:"options,omitempty"`
	MultiSelect   bool           `json:"multi_select,omitempty"`
	Fields        []AskUserField `json:"fields,omitempty"`
	AllowFreeText bool           `json:"allow_free_text,omitempty"`
}

// AskUserPromptResponse is the Response of an AgentPromptKindAskUser.
// Declined means the user chose not to answer.
type AskUserPromptResponse struct {
	Selected []string       `json:"selected,omitempty"`
	Values   map[string]any `json:"values,omitempty"`
	Text     string         `json:"text,omitempty"`
	Declined bool           `json:"declined,omitempty"`
}

// FreeTextOnly reports whether the question is answered by text alone.
func (r *AskUserPromptRequest) FreeTextOnly() bool {
	return len(r.Options) == 0 && len(r.Fields) == 0
}

// Validate checks a question against the ask_user limits.
func (r *AskUserPromptRequest) Validate() error {
	r.Question = strings.TrimSpace(r.Question)
	if r.Question == "" {
		return errors.New("question is required")
	}
	if utf8.RuneCountInString(r.Question) > AskUserMaxQuestionLen {
		return fmt.Errorf("question exceeds %d characters", AskUserMaxQuestionLen)
	}
	if len(r.Options) > 0 && len(r.Fields) > 0 {
		return errors.New("use either options or fields, not both")
	}
	if err := validateAskUserOptions(r.Options); err != nil {
		return err
	}
	if len(r.Fields) > AskUserMaxFields {
		return fmt.Errorf("at most %d fields", AskUserMaxFields)
	}
	names := make(map[string]bool, len(r.Fields))
	for i := range r.Fields {
		f := &r.Fields[i]
		f.Name = strings.TrimSpace(f.Name)
		if f.Name == "" || utf8.RuneCountInString(f.Name) > AskUserMaxNameLen {
			return fmt.Errorf("field %d needs a name of at most %d characters", i, AskUserMaxNameLen)
		}
		if names[f.Name] {
			return fmt.Errorf("duplicate field %q", f.Name)
		}
		names[f.Name] = true
		if utf8.RuneCountInString(f.Label) > AskUserMaxOptionLen {
			return fmt.Errorf("label of field %q exceeds %d characters", f.Name, AskUserMaxOptionLen)
		}
		switch f.Type {
		case AskUserFieldText, AskUserFieldNumber:
			if len(f.Options) > 0 {
				return fmt.Errorf("field %q of type %s takes no options", f.Name, f.Type)
			}
		case AskUserFieldSelect, AskUserFieldMultiSelect:
			if len(f.Options) == 0 {
				return fmt.Errorf("field %q needs options", f.Name)
			}
			if err := validateAskUserOptions(f.Options); err != nil {
				return fmt.Errorf("field %q: %w", f.Name, err)
			}
		default:
			return fmt.Errorf("field %q has unsupported type %q", f.Name, f.Type)
		}
	}
	return nil
}

func validateAskUserOptions(options []string) error {
	if len(options) > AskUserMaxOptions {
		return fmt.Errorf("at most %d options", AskUserMaxOptions)
	}
	seen := make(map[string]bool, len(options))
	for _, o := range options {
		// A model may put a serialized choice list inside a one-element array.
		// Do not display that list as a single choice, even if its JSON is valid.
		trimmed := strings.TrimSpace(o)
		if strings.HasPrefix(trimmed, "[") {
			inner := strings.TrimSpace(strings.TrimPrefix(trimmed, "["))
			if strings.HasPrefix(inner, "\"") || inner == "]" {
				return errors.New("options must contain one choice per string, not an encoded array; " +
					`use "options": ["first choice", "second choice"]`)
			}
		}
		if strings.TrimSpace(o) == "" || utf8.RuneCountInString(o) > AskUserMaxOptionLen {
			return fmt.Errorf("options must be non-empty and at most %d characters", AskUserMaxOptionLen)
		}
		if seen[o] {
			return fmt.Errorf("duplicate option %q", o)
		}
		seen[o] = true
	}
	return nil
}

// ValidateAnswer checks a response against the question it answers.
func (r *AskUserPromptRequest) ValidateAnswer(a *AskUserPromptResponse) error {
	if a.Declined {
		if len(a.Selected) > 0 || len(a.Values) > 0 || a.Text != "" {
			return errors.New("a declined answer carries no content")
		}
		return nil
	}
	if utf8.RuneCountInString(a.Text) > AskUserMaxTextLen {
		return fmt.Errorf("text exceeds %d characters", AskUserMaxTextLen)
	}
	hasText := strings.TrimSpace(a.Text) != ""
	if hasText && !r.AllowFreeText && !r.FreeTextOnly() {
		return errors.New("this question does not accept free text")
	}
	if len(a.Selected) > 0 && len(r.Options) == 0 {
		return errors.New("this question has no options")
	}
	if err := checkChoices(a.Selected, r.Options, r.MultiSelect); err != nil {
		return err
	}
	if len(a.Values) > 0 && len(r.Fields) == 0 {
		return errors.New("this question has no fields")
	}
	fields := make(map[string]AskUserField, len(r.Fields))
	for _, f := range r.Fields {
		fields[f.Name] = f
	}
	for name, v := range a.Values {
		f, ok := fields[name]
		if !ok {
			return fmt.Errorf("unknown field %q", name)
		}
		if err := checkFieldValue(f, v); err != nil {
			return err
		}
	}
	for _, f := range r.Fields {
		if v, ok := a.Values[f.Name]; f.Required && (!ok || isEmptyAnswer(v)) {
			return fmt.Errorf("field %q is required", f.Name)
		}
	}
	switch {
	case r.FreeTextOnly():
		if !hasText {
			return errors.New("an answer is required")
		}
	case len(r.Options) > 0:
		if len(a.Selected) == 0 && !hasText {
			return errors.New("choose an option")
		}
	}
	return nil
}

func checkChoices(selected, options []string, multi bool) error {
	if !multi && len(selected) > 1 {
		return errors.New("only one option may be chosen")
	}
	allowed := make(map[string]bool, len(options))
	for _, o := range options {
		allowed[o] = true
	}
	seen := make(map[string]bool, len(selected))
	for _, s := range selected {
		if !allowed[s] {
			return fmt.Errorf("%q is not an option", s)
		}
		if seen[s] {
			return fmt.Errorf("%q chosen twice", s)
		}
		seen[s] = true
	}
	return nil
}

func checkFieldValue(f AskUserField, v any) error {
	switch f.Type {
	case AskUserFieldText:
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("field %q expects text", f.Name)
		}
		if utf8.RuneCountInString(s) > AskUserMaxTextLen {
			return fmt.Errorf("field %q exceeds %d characters", f.Name, AskUserMaxTextLen)
		}
	case AskUserFieldNumber:
		n, ok := v.(float64)
		if !ok || math.IsNaN(n) || math.IsInf(n, 0) {
			return fmt.Errorf("field %q expects a number", f.Name)
		}
	case AskUserFieldSelect:
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("field %q expects one option", f.Name)
		}
		if s != "" {
			return checkChoices([]string{s}, f.Options, false)
		}
	case AskUserFieldMultiSelect:
		list, ok := v.([]any)
		if !ok {
			return fmt.Errorf("field %q expects a list of options", f.Name)
		}
		chosen := make([]string, 0, len(list))
		for _, item := range list {
			s, ok := item.(string)
			if !ok {
				return fmt.Errorf("field %q expects a list of options", f.Name)
			}
			chosen = append(chosen, s)
		}
		return checkChoices(chosen, f.Options, true)
	}
	return nil
}

func isEmptyAnswer(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(x) == ""
	case []any:
		return len(x) == 0
	}
	return false
}

// ValidateAskUserResponse is the broker validator of AgentPromptKindAskUser.
func ValidateAskUserResponse(request, response json.RawMessage) error {
	var q AskUserPromptRequest
	if err := json.Unmarshal(request, &q); err != nil {
		return fmt.Errorf("unreadable question: %w", err)
	}
	var a AskUserPromptResponse
	dec := json.NewDecoder(strings.NewReader(string(response)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil {
		return err
	}
	return q.ValidateAnswer(&a)
}

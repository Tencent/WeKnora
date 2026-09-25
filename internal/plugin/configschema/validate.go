package configschema

import (
	"encoding/json"
	"fmt"
	"math"
	"net/mail"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Error codes a FieldError carries. The frontend maps them to messages.
const (
	CodeRequired  = "required"
	CodeType      = "type"
	CodeEnum      = "enum"
	CodeMinLength = "min_length"
	CodeMaxLength = "max_length"
	CodePattern   = "pattern"
	CodeMinimum   = "minimum"
	CodeMaximum   = "maximum"
	CodeFormat    = "format"
)

// FieldError is one problem with one field. Path is dotted ("auth.token")
// with "[i]" for array items.
type FieldError struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// FieldErrors is every problem Validate found.
type FieldErrors []FieldError

func (e FieldErrors) Error() string {
	parts := make([]string, len(e))
	for i, fe := range e {
		parts[i] = fe.Path + ": " + fe.Message
	}
	return "invalid configuration: " + strings.Join(parts, "; ")
}

// Validate checks a configuration against its schema and returns every
// problem, or nil. Fields the schema does not declare are allowed, so a
// configuration written by a newer version still validates. A required field
// hidden by x-visible-if is not required.
func Validate(s *Schema, value map[string]any) FieldErrors {
	var errs FieldErrors
	validateObject(s, value, "", &errs)
	if len(errs) == 0 {
		return nil
	}
	return errs
}

func validateObject(s *Schema, value map[string]any, path string, errs *FieldErrors) {
	required := make(map[string]bool, len(s.Required))
	for _, k := range s.Required {
		required[k] = true
	}
	for _, key := range s.OrderedKeys() {
		prop := s.Properties[key]
		if !visible(prop, value) {
			continue
		}
		fieldPath := joinPath(path, key)
		v, present := value[key]
		if !present || v == nil || v == "" {
			if required[key] {
				*errs = append(*errs, FieldError{fieldPath, CodeRequired, "is required"})
			}
			continue
		}
		validateValue(prop, v, fieldPath, errs)
	}
}

// visible evaluates x-visible-if against sibling values.
func visible(s *Schema, siblings map[string]any) bool {
	for key, want := range s.VisibleIf {
		if !looselyEqual(siblings[key], want) {
			return false
		}
	}
	return true
}

func validateValue(s *Schema, v any, path string, errs *FieldErrors) {
	add := func(code, format string, args ...any) {
		*errs = append(*errs, FieldError{path, code, fmt.Sprintf(format, args...)})
	}
	switch s.Type {
	case TypeObject:
		obj, ok := v.(map[string]any)
		if !ok {
			add(CodeType, "must be an object")
			return
		}
		validateObject(s, obj, path, errs)
		return
	case TypeArray:
		items, ok := v.([]any)
		if !ok {
			add(CodeType, "must be an array")
			return
		}
		for i, item := range items {
			validateValue(s.Items, item, fmt.Sprintf("%s[%d]", path, i), errs)
		}
		return
	case TypeString:
		str, ok := v.(string)
		if !ok {
			add(CodeType, "must be a string")
			return
		}
		validateString(s, str, add)
	case TypeNumber, TypeInteger:
		n, ok := toFloat(v)
		if !ok {
			add(CodeType, "must be a number")
			return
		}
		if s.Type == TypeInteger && n != math.Trunc(n) {
			add(CodeType, "must be an integer")
			return
		}
		if s.Minimum != nil && n < *s.Minimum {
			add(CodeMinimum, "must be at least %v", *s.Minimum)
		}
		if s.Maximum != nil && n > *s.Maximum {
			add(CodeMaximum, "must be at most %v", *s.Maximum)
		}
	case TypeBoolean:
		if _, ok := v.(bool); !ok {
			add(CodeType, "must be true or false")
			return
		}
	}
	if !allowedChoice(s, v) {
		add(CodeEnum, "is not one of the allowed values")
	}
}

func validateString(s *Schema, str string, add func(code, format string, args ...any)) {
	if s.Secret && str == RedactedPlaceholder {
		// An unchanged secret: nothing to check about the stored value.
		return
	}
	n := utf8.RuneCountInString(str)
	if s.MinLength != nil && n < *s.MinLength {
		add(CodeMinLength, "must be at least %d characters", *s.MinLength)
	}
	if s.MaxLength != nil && n > *s.MaxLength {
		add(CodeMaxLength, "must be at most %d characters", *s.MaxLength)
	}
	if s.Pattern != "" {
		if re, err := regexp.Compile(s.Pattern); err == nil && !re.MatchString(str) {
			add(CodePattern, "does not match the expected format")
		}
	}
	switch s.Format {
	case "uri":
		if u, err := url.Parse(str); err != nil || u.Scheme == "" || u.Host == "" {
			add(CodeFormat, "must be an absolute URL")
		}
	case "email":
		if _, err := mail.ParseAddress(str); err != nil {
			add(CodeFormat, "must be an email address")
		}
	}
}

func allowedChoice(s *Schema, v any) bool {
	if len(s.Enum) == 0 && len(s.OneOf) == 0 {
		return true
	}
	for _, e := range s.Enum {
		if looselyEqual(e, v) {
			return true
		}
	}
	for _, c := range s.OneOf {
		if looselyEqual(c.Const, v) {
			return true
		}
	}
	return false
}

// looselyEqual compares decoded JSON values, treating all numeric kinds as
// equal when their values are.
func looselyEqual(a, b any) bool {
	if fa, ok := toFloat(a); ok {
		fb, ok := toFloat(b)
		return ok && fa == fb
	}
	return reflect.DeepEqual(a, b)
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	case uint64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

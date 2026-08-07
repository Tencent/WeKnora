package types

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strings"
	"unicode"
)

// MetadataFilterOperator identifies the predicate applied to one metadata key.
type MetadataFilterOperator string

const (
	MetadataFilterOpEqual MetadataFilterOperator = "eq"
	MetadataFilterOpIn    MetadataFilterOperator = "in"
)

var strictJSONNumberPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

const (
	maxMetadataFilterDepth = 8
	maxMetadataFilterNodes = 64
	maxMetadataFilterField = 64
)

// MetadataFilter is a recursive boolean expression over row metadata.
// A node is either a non-empty And/Or group or a single field predicate.
type MetadataFilter struct {
	And    []MetadataFilter       `json:"and,omitempty"`
	Or     []MetadataFilter       `json:"or,omitempty"`
	Field  string                 `json:"field,omitempty"`
	Op     MetadataFilterOperator `json:"op,omitempty"`
	Value  any                    `json:"value"`
	Values []any                  `json:"values,omitempty"`

	andSet bool
	orSet  bool
}

// UnmarshalJSON records group fields separately from their decoded slices so
// explicit null group fields cannot be mistaken for omitted group fields.
func (f *MetadataFilter) UnmarshalJSON(data []byte) error {
	type metadataFilterAlias MetadataFilter
	var decoded metadataFilterAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	*f = MetadataFilter(decoded)
	_, f.andSet = fields["and"]
	_, f.orSet = fields["or"]
	return nil
}

// Validate checks the filter grammar and its resource limits.
func (f *MetadataFilter) Validate() error {
	if f == nil {
		return fmt.Errorf("metadata filter is nil")
	}
	state := metadataFilterValidation{}
	return state.validate(f, 1)
}

type metadataFilterValidation struct {
	nodes int
}

func (v *metadataFilterValidation) validate(f *MetadataFilter, depth int) error {
	if depth > maxMetadataFilterDepth {
		return fmt.Errorf("metadata filter exceeds maximum depth of %d", maxMetadataFilterDepth)
	}
	v.nodes++
	if v.nodes > maxMetadataFilterNodes {
		return fmt.Errorf("metadata filter exceeds maximum node count of %d", maxMetadataFilterNodes)
	}

	hasAnd := f.andSet || f.And != nil
	hasOr := f.orSet || f.Or != nil
	if hasAnd || hasOr {
		if f.Field != "" || f.Op != "" || f.Value != nil || f.Values != nil {
			return fmt.Errorf("metadata filter node cannot mix group and predicate fields")
		}
		if hasAnd == hasOr {
			return fmt.Errorf("metadata filter node must contain exactly one group")
		}
		children := f.And
		groupName := "and"
		if hasOr {
			children = f.Or
			groupName = "or"
		}
		if len(children) == 0 {
			return fmt.Errorf("metadata filter %s group must not be empty", groupName)
		}
		for i := range children {
			if err := v.validate(&children[i], depth+1); err != nil {
				return err
			}
		}
		return nil
	}

	if f.Field == "" || strings.TrimSpace(f.Field) != f.Field || len([]rune(f.Field)) > maxMetadataFilterField {
		return fmt.Errorf("metadata filter field must be a trimmed non-empty key of at most %d characters", maxMetadataFilterField)
	}
	for _, r := range f.Field {
		if unicode.IsControl(r) {
			return fmt.Errorf("metadata filter field contains a control character")
		}
	}
	switch f.Op {
	case MetadataFilterOpEqual:
		if f.Value == nil || f.Values != nil || !validMetadataScalar(f.Value) {
			return fmt.Errorf("metadata filter eq requires one scalar value")
		}
	case MetadataFilterOpIn:
		if f.Value != nil || len(f.Values) == 0 {
			return fmt.Errorf("metadata filter in requires a non-empty values list")
		}
		for _, value := range f.Values {
			if !validMetadataScalar(value) {
				return fmt.Errorf("metadata filter in values must be scalars")
			}
		}
	default:
		return fmt.Errorf("unsupported metadata filter operator %q", f.Op)
	}
	return nil
}

func validMetadataScalar(value any) bool {
	if value == nil {
		return false
	}
	if _, ok := value.(string); ok {
		return true
	}
	if _, ok := value.(bool); ok {
		return true
	}
	if _, ok := value.(json.Number); ok {
		if !strictJSONNumberPattern.MatchString(string(value.(json.Number))) {
			return false
		}
		_, ok = metadataNumber(value)
		return ok
	}
	kind := reflect.TypeOf(value).Kind()
	if kind >= reflect.Int && kind <= reflect.Float64 || kind >= reflect.Uint && kind <= reflect.Uint64 {
		_, ok := metadataNumber(value)
		return ok
	}
	return false
}

// Matches evaluates the filter against one metadata row. Invalid filters and
// missing fields fail closed by returning false.
func (f *MetadataFilter) Matches(metadata JSONMap) bool {
	if f == nil || f.Validate() != nil {
		return false
	}
	return matchesMetadataFilter(f, metadata)
}

func matchesMetadataFilter(f *MetadataFilter, metadata JSONMap) bool {
	if f.And != nil {
		for i := range f.And {
			if !matchesMetadataFilter(&f.And[i], metadata) {
				return false
			}
		}
		return true
	}
	if f.Or != nil {
		for i := range f.Or {
			if matchesMetadataFilter(&f.Or[i], metadata) {
				return true
			}
		}
		return false
	}

	stored, ok := metadata[f.Field]
	if !ok {
		return false
	}
	if values, ok := metadataArray(stored); ok {
		if f.Op == MetadataFilterOpEqual {
			for _, element := range values {
				if metadataScalarEqual(element, f.Value) {
					return true
				}
			}
			return false
		}
		for _, element := range values {
			for _, candidate := range f.Values {
				if metadataScalarEqual(element, candidate) {
					return true
				}
			}
		}
		return false
	}
	if f.Op == MetadataFilterOpEqual {
		return metadataScalarEqual(stored, f.Value)
	}
	for _, candidate := range f.Values {
		if metadataScalarEqual(stored, candidate) {
			return true
		}
	}
	return false
}

func metadataArray(value any) ([]any, bool) {
	rv := reflect.ValueOf(value)
	if !rv.IsValid() || (rv.Kind() != reflect.Array && rv.Kind() != reflect.Slice) {
		return nil, false
	}
	values := make([]any, rv.Len())
	for i := range values {
		values[i] = rv.Index(i).Interface()
	}
	return values, true
}

func metadataScalarEqual(left, right any) bool {
	if leftNumber, ok := metadataNumber(left); ok {
		rightNumber, rightOK := metadataNumber(right)
		return rightOK && leftNumber == rightNumber
	}
	return reflect.DeepEqual(left, right)
}

func metadataNumber(value any) (float64, bool) {
	if number, ok := value.(json.Number); ok {
		parsed, err := number.Float64()
		return parsed, err == nil && !math.IsNaN(parsed) && !math.IsInf(parsed, 0)
	}
	rv := reflect.ValueOf(value)
	if !rv.IsValid() {
		return 0, false
	}
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(rv.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(rv.Uint()), true
	case reflect.Float32, reflect.Float64:
		value := rv.Float()
		return value, !math.IsNaN(value) && !math.IsInf(value, 0)
	default:
		return 0, false
	}
}

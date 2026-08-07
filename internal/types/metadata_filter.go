package types

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// MetadataFilterOperator identifies the predicate applied to one metadata key.
type MetadataFilterOperator string

const (
	// MetadataFilterOpEqual matches a scalar or an element of a stored array.
	MetadataFilterOpEqual MetadataFilterOperator = "eq"
	// MetadataFilterOpIn matches any supplied scalar against a scalar or stored array.
	MetadataFilterOpIn MetadataFilterOperator = "in"
)

var strictJSONNumberPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

const (
	maxMetadataFilterDepth = 8
	maxMetadataFilterNodes = 64
	maxMetadataFilterField = 64

	maxMetadataFilterValues          = 64
	maxMetadataFilterValueBytes      = 4096
	maxMetadataFilterTotalValueBytes = 16384
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

	fieldSet  bool
	opSet     bool
	valueSet  bool
	valuesSet bool
}

// UnmarshalJSON strictly decodes one filter node. Presence flags distinguish
// omitted fields from valid zero scalars and reject branch mixing even when a
// conflicting field is explicitly null.
func (f *MetadataFilter) UnmarshalJSON(data []byte) error {
	if f == nil {
		return fmt.Errorf("metadata filter receiver is nil")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	opening, err := decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := opening.(json.Delim); !ok || delimiter != '{' {
		return fmt.Errorf("metadata filter node must be a JSON object")
	}

	decoded := MetadataFilter{}
	seen := make(map[string]struct{}, 6)
	for decoder.More() {
		keyToken, tokenErr := decoder.Token()
		if tokenErr != nil {
			return tokenErr
		}
		key, ok := keyToken.(string)
		if !ok {
			return fmt.Errorf("metadata filter field name must be a string")
		}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("duplicate metadata filter field %q", key)
		}
		seen[key] = struct{}{}

		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return err
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("metadata filter field %q must not be null", key)
		}
		switch key {
		case "and":
			decoded.andSet = true
			if err := decodeJSONWithNumbers(raw, &decoded.And); err != nil {
				return fmt.Errorf("decode metadata filter and: %w", err)
			}
		case "or":
			decoded.orSet = true
			if err := decodeJSONWithNumbers(raw, &decoded.Or); err != nil {
				return fmt.Errorf("decode metadata filter or: %w", err)
			}
		case "field":
			decoded.fieldSet = true
			if err := decodeJSONWithNumbers(raw, &decoded.Field); err != nil {
				return fmt.Errorf("decode metadata filter field: %w", err)
			}
		case "op":
			decoded.opSet = true
			if err := decodeJSONWithNumbers(raw, &decoded.Op); err != nil {
				return fmt.Errorf("decode metadata filter op: %w", err)
			}
		case "value":
			decoded.valueSet = true
			if err := decodeJSONWithNumbers(raw, &decoded.Value); err != nil {
				return fmt.Errorf("decode metadata filter value: %w", err)
			}
		case "values":
			decoded.valuesSet = true
			if err := decodeJSONWithNumbers(raw, &decoded.Values); err != nil {
				return fmt.Errorf("decode metadata filter values: %w", err)
			}
		default:
			return fmt.Errorf("unknown metadata filter field %q", key)
		}
	}
	closing, err := decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := closing.(json.Delim); !ok || delimiter != '}' {
		return fmt.Errorf("metadata filter node has invalid JSON object termination")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("metadata filter node contains trailing JSON")
		}
		return err
	}
	*f = decoded
	return nil
}

// MarshalJSON emits only fields belonging to the selected branch. Equality
// scalars remain present even when they are false, zero, or an empty string.
func (f MetadataFilter) MarshalJSON() ([]byte, error) {
	result := make(map[string]any, 2)
	hasAnd := f.andSet || f.And != nil
	hasOr := f.orSet || f.Or != nil
	if hasAnd || hasOr {
		if hasAnd {
			result["and"] = f.And
		}
		if hasOr {
			result["or"] = f.Or
		}
		return json.Marshal(result)
	}
	result["field"] = f.Field
	result["op"] = f.Op
	if f.Op == MetadataFilterOpIn || f.valuesSet || f.Values != nil {
		result["values"] = f.Values
	} else {
		result["value"] = f.Value
	}
	return json.Marshal(result)
}

func decodeJSONWithNumbers(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON")
		}
		return err
	}
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
		if f.fieldSet || f.Field != "" || f.opSet || f.Op != "" ||
			f.valueSet || f.Value != nil || f.valuesSet || f.Values != nil {
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

	hasField := f.fieldSet || f.Field != ""
	hasOp := f.opSet || f.Op != ""
	if !hasField || f.Field == "" || strings.TrimSpace(f.Field) != f.Field ||
		len([]rune(f.Field)) > maxMetadataFilterField {
		return fmt.Errorf(
			"metadata filter field must be a trimmed non-empty key of at most %d characters",
			maxMetadataFilterField,
		)
	}
	for _, r := range f.Field {
		if unicode.IsControl(r) {
			return fmt.Errorf("metadata filter field contains a control character")
		}
	}
	if !hasOp {
		return fmt.Errorf("metadata filter leaf requires an operator")
	}
	hasValue := f.valueSet || f.Value != nil
	hasValues := f.valuesSet || f.Values != nil
	switch f.Op {
	case MetadataFilterOpEqual:
		if !hasValue || f.Value == nil || hasValues || !validMetadataScalar(f.Value) {
			return fmt.Errorf("metadata filter eq requires one scalar value")
		}
	case MetadataFilterOpIn:
		if hasValue || !hasValues || len(f.Values) == 0 {
			return fmt.Errorf("metadata filter in requires a non-empty values list")
		}
		if len(f.Values) > maxMetadataFilterValues {
			return fmt.Errorf("metadata filter in accepts at most %d values", maxMetadataFilterValues)
		}
		totalBytes := 0
		for _, value := range f.Values {
			if !validMetadataScalar(value) {
				return fmt.Errorf("metadata filter in values must be scalars")
			}
			encoded, err := json.Marshal(value)
			if err != nil {
				return fmt.Errorf("encode metadata filter in value: %w", err)
			}
			if len(encoded) > maxMetadataFilterValueBytes {
				return fmt.Errorf("metadata filter in value exceeds %d encoded bytes", maxMetadataFilterValueBytes)
			}
			totalBytes += len(encoded)
			if totalBytes > maxMetadataFilterTotalValueBytes {
				return fmt.Errorf(
					"metadata filter in values exceed %d total encoded bytes",
					maxMetadataFilterTotalValueBytes,
				)
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
		return rightOK && leftNumber.equal(rightNumber)
	}
	return reflect.DeepEqual(left, right)
}

type normalizedMetadataNumber struct {
	negative bool
	digits   string
	exponent *big.Int
}

func (n normalizedMetadataNumber) equal(other normalizedMetadataNumber) bool {
	return n.negative == other.negative && n.digits == other.digits && n.exponent.Cmp(other.exponent) == 0
}

func metadataNumber(value any) (normalizedMetadataNumber, bool) {
	if number, ok := value.(json.Number); ok {
		return normalizeMetadataNumber(string(number))
	}
	rv := reflect.ValueOf(value)
	if !rv.IsValid() {
		return normalizedMetadataNumber{}, false
	}
	var encoded string
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		encoded = strconv.FormatInt(rv.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		encoded = strconv.FormatUint(rv.Uint(), 10)
	case reflect.Float32:
		floatValue := rv.Float()
		if math.IsNaN(floatValue) || math.IsInf(floatValue, 0) {
			return normalizedMetadataNumber{}, false
		}
		encoded = strconv.FormatFloat(floatValue, 'g', -1, 32)
	case reflect.Float64:
		floatValue := rv.Float()
		if math.IsNaN(floatValue) || math.IsInf(floatValue, 0) {
			return normalizedMetadataNumber{}, false
		}
		encoded = strconv.FormatFloat(floatValue, 'g', -1, 64)
	default:
		return normalizedMetadataNumber{}, false
	}
	return normalizeMetadataNumber(encoded)
}

// normalizeMetadataNumber canonicalizes JSON decimal syntax without converting
// through binary floating point. The exponent remains arbitrary precision, so
// comparison is exact without allocating enormous powers of ten.
func normalizeMetadataNumber(value string) (normalizedMetadataNumber, bool) {
	if !strictJSONNumberPattern.MatchString(value) {
		return normalizedMetadataNumber{}, false
	}
	negative := strings.HasPrefix(value, "-")
	if negative {
		value = value[1:]
	}
	exponent := new(big.Int)
	if separator := strings.IndexAny(value, "eE"); separator >= 0 {
		if _, ok := exponent.SetString(value[separator+1:], 10); !ok {
			return normalizedMetadataNumber{}, false
		}
		value = value[:separator]
	}
	fractionDigits := 0
	if point := strings.IndexByte(value, '.'); point >= 0 {
		fractionDigits = len(value) - point - 1
		value = value[:point] + value[point+1:]
	}
	digits := strings.TrimLeft(value, "0")
	if digits == "" {
		return normalizedMetadataNumber{digits: "0", exponent: new(big.Int)}, true
	}
	trailingZeros := len(digits) - len(strings.TrimRight(digits, "0"))
	if trailingZeros > 0 {
		digits = digits[:len(digits)-trailingZeros]
	}
	exponent.Sub(exponent, big.NewInt(int64(fractionDigits)))
	exponent.Add(exponent, big.NewInt(int64(trailingZeros)))
	return normalizedMetadataNumber{negative: negative, digits: digits, exponent: exponent}, true
}

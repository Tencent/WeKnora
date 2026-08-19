package types

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMetadataFilterValidateAcceptsNestedPolicy(t *testing.T) {
	filter := MetadataFilter{
		Or: []MetadataFilter{
			{And: []MetadataFilter{
				{Field: "employee_nature", Op: MetadataFilterOpEqual, Value: "formal"},
				{Field: "department", Op: MetadataFilterOpEqual, Value: "research"},
			}},
			{And: []MetadataFilter{
				{Field: "employee_nature", Op: MetadataFilterOpEqual, Value: "contractor"},
				{Field: "department", Op: MetadataFilterOpEqual, Value: "finance"},
			}},
		},
	}
	if err := filter.Validate(); err != nil {
		t.Fatalf("valid nested filter rejected: %v", err)
	}
}

func TestMetadataFilterValidateRejectsMalformedNodes(t *testing.T) {
	longField := strings.Repeat("a", 65)
	tooDeep := MetadataFilter{Field: "value", Op: MetadataFilterOpEqual, Value: true}
	for i := 0; i < 8; i++ {
		tooDeep = MetadataFilter{And: []MetadataFilter{tooDeep}}
	}

	tests := []struct {
		name   string
		filter MetadataFilter
	}{
		{
			name: "mixed group and predicate",
			filter: MetadataFilter{
				And:   []MetadataFilter{{Field: "x", Op: MetadataFilterOpEqual, Value: true}},
				Field: "x", Op: MetadataFilterOpEqual, Value: true,
			},
		},
		{name: "empty node", filter: MetadataFilter{}},
		{name: "empty and", filter: MetadataFilter{And: []MetadataFilter{}}},
		{name: "empty or", filter: MetadataFilter{Or: []MetadataFilter{}}},
		{name: "empty in", filter: MetadataFilter{Field: "x", Op: MetadataFilterOpIn, Values: []any{}}},
		{
			name: "eq with values",
			filter: MetadataFilter{
				Field: "x", Op: MetadataFilterOpEqual, Value: true, Values: []any{true},
			},
		},
		{name: "in with value", filter: MetadataFilter{Field: "x", Op: MetadataFilterOpIn, Value: true, Values: []any{true}}},
		{name: "unknown operator", filter: MetadataFilter{Field: "x", Op: MetadataFilterOperator("gt"), Value: true}},
		{name: "missing value", filter: MetadataFilter{Field: "x", Op: MetadataFilterOpEqual}},
		{name: "unsupported value", filter: MetadataFilter{Field: "x", Op: MetadataFilterOpEqual, Value: []any{"x"}}},
		{name: "empty field", filter: MetadataFilter{Field: "  ", Op: MetadataFilterOpEqual, Value: true}},
		{name: "long field", filter: MetadataFilter{Field: longField, Op: MetadataFilterOpEqual, Value: true}},
		{name: "control character field", filter: MetadataFilter{Field: "x\n", Op: MetadataFilterOpEqual, Value: true}},
		{name: "too deep", filter: tooDeep},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.filter.Validate(); err == nil {
				t.Fatal("malformed filter was accepted")
			}
		})
	}

	tooMany := MetadataFilter{And: make([]MetadataFilter, 65)}
	for i := range tooMany.And {
		tooMany.And[i] = MetadataFilter{Field: "x", Op: MetadataFilterOpEqual, Value: true}
	}
	if err := tooMany.Validate(); err == nil {
		t.Fatal("filter with more than 64 nodes was accepted")
	}
}

func TestMetadataFilterValidateAcceptsJSONScalars(t *testing.T) {
	for _, value := range []any{"text", float64(3), json.Number("3"), true} {
		filter := MetadataFilter{Field: "value", Op: MetadataFilterOpEqual, Value: value}
		if err := filter.Validate(); err != nil {
			t.Errorf("value %#v rejected: %v", value, err)
		}
	}

	var decoded MetadataFilter
	if err := json.Unmarshal([]byte(`{"field":"count","op":"eq","value":3}`), &decoded); err != nil {
		t.Fatal(err)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatalf("decoded number rejected: %v", err)
	}
}

func TestMetadataFilterJSONPreservesZeroEqualityValues(t *testing.T) {
	for _, value := range []any{false, float64(0), ""} {
		body, err := json.Marshal(MetadataFilter{Field: "value", Op: MetadataFilterOpEqual, Value: value})
		if err != nil {
			t.Fatalf("marshal value %#v: %v", value, err)
		}
		var decoded MetadataFilter
		if err := json.Unmarshal(body, &decoded); err != nil {
			t.Fatalf("unmarshal value %#v: %v", value, err)
		}
		if decoded.Value == nil {
			t.Fatalf("value %#v was omitted from JSON: %s", value, body)
		}
		if err := decoded.Validate(); err != nil {
			t.Fatalf("round-tripped value %#v rejected: %v", value, err)
		}
	}
}

func TestMetadataFilterJSONRejectsExplicitNullAndMixedGroups(t *testing.T) {
	for _, body := range []string{
		`{"and":null,"field":"x","op":"eq","value":true}`,
		`{"or":null,"field":"x","op":"eq","value":true}`,
		`{"and":null,"or":[{"field":"x","op":"eq","value":true}]}`,
	} {
		var filter MetadataFilter
		if err := json.Unmarshal([]byte(body), &filter); err == nil {
			t.Fatalf("malformed explicit group decoded: %s", body)
		}
	}
}

func TestMetadataFilterValidateRejectsInvalidJSONNumbers(t *testing.T) {
	for _, value := range []json.Number{"not-a-number", "NaN", "+Inf", "-Inf", "01", "1.", " 1", "1 "} {
		filter := MetadataFilter{Field: "value", Op: MetadataFilterOpEqual, Value: value}
		if err := filter.Validate(); err == nil {
			t.Fatalf("invalid JSON number %q was accepted", value)
		}
	}
}

func TestMetadataFilterValidateAcceptsStrictJSONNumbers(t *testing.T) {
	for _, value := range []json.Number{"0", "-0", "12.5", "1e3", "-2.5E-4"} {
		filter := MetadataFilter{Field: "value", Op: MetadataFilterOpEqual, Value: value}
		if err := filter.Validate(); err != nil {
			t.Fatalf("valid JSON number %q was rejected: %v", value, err)
		}
	}
}

func TestMetadataFilterJSONRejectsUnknownDuplicateAndBranchMixedFields(t *testing.T) {
	tests := []string{
		`{"field":"department","op":"eq","value":"research","unknown":true}`,
		`{"field":"department","field":"finance","op":"eq","value":"research"}`,
		`{"and":[{"field":"department","op":"eq","value":"research"}],"field":null}`,
		`{"and":[{"field":"department","op":"eq","value":"research"}],"op":null}`,
		`{"and":[{"field":"department","op":"eq","value":"research"}],"value":null}`,
		`{"and":[{"field":"department","op":"eq","value":"research"}],"values":null}`,
	}
	for _, body := range tests {
		var filter MetadataFilter
		if err := json.Unmarshal([]byte(body), &filter); err == nil {
			t.Fatalf("malformed filter was decoded: %s", body)
		}
	}
}

func TestMetadataFilterJSONRejectsMissingOrNullLeafFields(t *testing.T) {
	for _, body := range []string{
		`{"op":"eq","value":false}`,
		`{"field":null,"op":"eq","value":false}`,
		`{"field":"enabled","value":false}`,
		`{"field":"enabled","op":null,"value":false}`,
		`{"field":"enabled","op":"eq"}`,
		`{"field":"enabled","op":"eq","value":null}`,
		`{"field":"enabled","op":"in"}`,
		`{"field":"enabled","op":"in","values":null}`,
	} {
		var filter MetadataFilter
		err := json.Unmarshal([]byte(body), &filter)
		if err == nil {
			err = filter.Validate()
		}
		if err == nil {
			t.Fatalf("missing or null required leaf field was accepted: %s", body)
		}
	}
}

func TestMetadataFilterJSONPreservesScalarZeroValuesAndOmitsLeafFieldsFromGroups(t *testing.T) {
	for _, value := range []any{false, json.Number("0"), ""} {
		body, err := json.Marshal(MetadataFilter{Field: "value", Op: MetadataFilterOpEqual, Value: value})
		if err != nil {
			t.Fatalf("marshal scalar %#v: %v", value, err)
		}
		if !strings.Contains(string(body), `"value":`) {
			t.Fatalf("zero scalar was omitted: %s", body)
		}
	}

	body, err := json.Marshal(MetadataFilter{And: []MetadataFilter{{
		Field: "enabled", Op: MetadataFilterOpEqual, Value: false,
	}}})
	if err != nil {
		t.Fatalf("marshal group: %v", err)
	}
	var groupFields map[string]json.RawMessage
	if err := json.Unmarshal(body, &groupFields); err != nil {
		t.Fatalf("decode marshaled group: %v", err)
	}
	if _, exists := groupFields["value"]; exists {
		t.Fatalf("group JSON contains value: %s", body)
	}
	if _, exists := groupFields["field"]; exists {
		t.Fatalf("group JSON contains leaf-only fields: %s", body)
	}
}

func TestMetadataFilterValidateBoundsInValuesPayload(t *testing.T) {
	const (
		maxValues     = 64
		maxValueBytes = 4096
		maxTotalBytes = 16384
	)
	validValues := make([]any, maxValues)
	for i := range validValues {
		validValues[i] = "v"
	}
	if err := (&MetadataFilter{Field: "role", Op: MetadataFilterOpIn, Values: validValues}).Validate(); err != nil {
		t.Fatalf("maximum values count rejected: %v", err)
	}

	tooMany := append(validValues, "overflow")
	if err := (&MetadataFilter{Field: "role", Op: MetadataFilterOpIn, Values: tooMany}).Validate(); err == nil {
		t.Fatal("values list above maximum count was accepted")
	}

	if err := (&MetadataFilter{
		Field: "role", Op: MetadataFilterOpIn,
		Values: []any{strings.Repeat("x", maxValueBytes-2)},
	}).Validate(); err != nil {
		t.Fatalf("single value at maximum encoded byte length rejected: %v", err)
	}
	if err := (&MetadataFilter{
		Field: "role", Op: MetadataFilterOpIn,
		Values: []any{strings.Repeat("x", maxValueBytes)},
	}).Validate(); err == nil {
		t.Fatal("single encoded value above maximum byte length was accepted")
	}

	exactTotalValues := make([]any, maxValues)
	for i := range exactTotalValues {
		exactTotalValues[i] = strings.Repeat("x", maxTotalBytes/maxValues-2)
	}
	if err := (&MetadataFilter{Field: "role", Op: MetadataFilterOpIn, Values: exactTotalValues}).Validate(); err != nil {
		t.Fatalf("values payload at maximum total encoded length rejected: %v", err)
	}

	totalValues := make([]any, maxValues)
	for i := range totalValues {
		totalValues[i] = strings.Repeat("x", maxTotalBytes/maxValues)
	}
	if err := (&MetadataFilter{Field: "role", Op: MetadataFilterOpIn, Values: totalValues}).Validate(); err == nil {
		t.Fatal("values payload above maximum total encoded length was accepted")
	}
}

func TestMetadataFilterMatchesAdjacentIntegersAboveTwoToThe53Exactly(t *testing.T) {
	var filter MetadataFilter
	if err := json.Unmarshal([]byte(`{"field":"employee_id","op":"eq","value":9007199254740993}`), &filter); err != nil {
		t.Fatalf("decode filter: %v", err)
	}
	chunk := &Chunk{Metadata: JSON(`{"access_metadata":{"employee_id":9007199254740992}}`)}
	metadata, err := chunk.AccessMetadata()
	if err != nil {
		t.Fatalf("decode access metadata: %v", err)
	}
	if filter.Matches(metadata) {
		t.Fatal("adjacent integers above 2^53 compared equal")
	}
	chunk.Metadata = JSON(`{"access_metadata":{"employee_id":9007199254740993}}`)
	metadata, err = chunk.AccessMetadata()
	if err != nil {
		t.Fatalf("decode matching access metadata: %v", err)
	}
	if !filter.Matches(metadata) {
		t.Fatal("identical integer above 2^53 did not match")
	}
}

func TestMetadataFilterMatchesMissingFieldAsFalse(t *testing.T) {
	filter := MetadataFilter{Field: "department", Op: MetadataFilterOpEqual, Value: "research"}
	if filter.Matches(JSONMap{"employee_nature": "formal"}) {
		t.Fatal("missing protected field must not match")
	}
}

func TestMetadataFilterMatchesScalarsAndArrays(t *testing.T) {
	tests := []struct {
		name     string
		filter   MetadataFilter
		metadata JSONMap
		want     bool
	}{
		{
			name:     "scalar equality",
			filter:   MetadataFilter{Field: "department", Op: MetadataFilterOpEqual, Value: "research"},
			metadata: JSONMap{"department": "research"}, want: true,
		},
		{
			name: "scalar membership",
			filter: MetadataFilter{
				Field: "department", Op: MetadataFilterOpIn, Values: []any{"finance", "research"},
			},
			metadata: JSONMap{"department": "research"}, want: true,
		},
		{
			name:     "array equality",
			filter:   MetadataFilter{Field: "roles", Op: MetadataFilterOpEqual, Value: "reviewer"},
			metadata: JSONMap{"roles": []any{"author", "reviewer"}}, want: true,
		},
		{
			name: "array intersection",
			filter: MetadataFilter{
				Field: "roles", Op: MetadataFilterOpIn, Values: []any{"reviewer", "admin"},
			},
			metadata: JSONMap{"roles": []any{"author", "reviewer"}}, want: true,
		},
		{
			name: "array no intersection",
			filter: MetadataFilter{
				Field: "roles", Op: MetadataFilterOpIn, Values: []any{"admin"},
			},
			metadata: JSONMap{"roles": []any{"author", "reviewer"}}, want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.filter.Matches(tt.metadata); got != tt.want {
				t.Fatalf("Matches() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMetadataFilterMatchesCorrelatedOrAndPolicy(t *testing.T) {
	filter := MetadataFilter{Or: []MetadataFilter{
		{And: []MetadataFilter{
			{Field: "employee_nature", Op: MetadataFilterOpEqual, Value: "formal"},
			{Field: "department", Op: MetadataFilterOpEqual, Value: "research"},
		}},
		{And: []MetadataFilter{
			{Field: "employee_nature", Op: MetadataFilterOpEqual, Value: "contractor"},
			{Field: "department", Op: MetadataFilterOpEqual, Value: "finance"},
		}},
	}}

	if !filter.Matches(JSONMap{"employee_nature": "formal", "department": "research"}) {
		t.Fatal("matching policy branch did not match")
	}
	if filter.Matches(JSONMap{"employee_nature": "formal", "department": "finance"}) {
		t.Fatal("cross-field combination over-granted access")
	}
}

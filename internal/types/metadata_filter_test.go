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
		{name: "mixed group and predicate", filter: MetadataFilter{And: []MetadataFilter{{Field: "x", Op: MetadataFilterOpEqual, Value: true}}, Field: "x", Op: MetadataFilterOpEqual, Value: true}},
		{name: "empty node", filter: MetadataFilter{}},
		{name: "empty and", filter: MetadataFilter{And: []MetadataFilter{}}},
		{name: "empty or", filter: MetadataFilter{Or: []MetadataFilter{}}},
		{name: "empty in", filter: MetadataFilter{Field: "x", Op: MetadataFilterOpIn, Values: []any{}}},
		{name: "eq with values", filter: MetadataFilter{Field: "x", Op: MetadataFilterOpEqual, Value: true, Values: []any{true}}},
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
		{name: "scalar equality", filter: MetadataFilter{Field: "department", Op: MetadataFilterOpEqual, Value: "research"}, metadata: JSONMap{"department": "research"}, want: true},
		{name: "scalar membership", filter: MetadataFilter{Field: "department", Op: MetadataFilterOpIn, Values: []any{"finance", "research"}}, metadata: JSONMap{"department": "research"}, want: true},
		{name: "array equality", filter: MetadataFilter{Field: "roles", Op: MetadataFilterOpEqual, Value: "reviewer"}, metadata: JSONMap{"roles": []any{"author", "reviewer"}}, want: true},
		{name: "array intersection", filter: MetadataFilter{Field: "roles", Op: MetadataFilterOpIn, Values: []any{"reviewer", "admin"}}, metadata: JSONMap{"roles": []any{"author", "reviewer"}}, want: true},
		{name: "array no intersection", filter: MetadataFilter{Field: "roles", Op: MetadataFilterOpIn, Values: []any{"admin"}}, metadata: JSONMap{"roles": []any{"author", "reviewer"}}, want: false},
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

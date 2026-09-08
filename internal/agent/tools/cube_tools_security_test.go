package tools

import (
	"strings"
	"testing"
)

func TestValidateBoundModelsRejectsOutOfScopeFilters(t *testing.T) {
	bound := []string{"orders"}
	input := &CubeQueryInput{
		Measures: []string{"orders.count"},
		Filters: []CubeQueryFilterInput{
			{Member: "users.email", Operator: "equals", Values: []string{"test@test.com"}},
		},
	}
	err := validateBoundModels(bound, input)
	if err == nil {
		t.Fatal("out-of-scope filter member should be rejected")
	}
	if !strings.Contains(err.Error(), "users") {
		t.Errorf("error should mention out-of-scope model, got: %v", err)
	}
}

func TestValidateBoundModelsRejectsOutOfScopeTimeDimensions(t *testing.T) {
	bound := []string{"orders"}
	input := &CubeQueryInput{
		Measures:       []string{"orders.count"},
		TimeDimensions: []CubeTimeDimension{{Dimension: "users.created_at", Granularity: "month"}},
	}
	err := validateBoundModels(bound, input)
	if err == nil {
		t.Fatal("out-of-scope time_dimension should be rejected")
	}
}

func TestValidateBoundModelsRejectsOutOfScopeOrder(t *testing.T) {
	bound := []string{"orders"}
	input := &CubeQueryInput{
		Measures: []string{"orders.count"},
		Order:    map[string]string{"users.created_at": "desc"},
	}
	err := validateBoundModels(bound, input)
	if err == nil {
		t.Fatal("out-of-scope order key should be rejected")
	}
}

func TestValidateBoundModelsAcceptsInScope(t *testing.T) {
	bound := []string{"orders"}
	input := &CubeQueryInput{
		Measures:       []string{"orders.count"},
		Dimensions:     []string{"orders.status"},
		Filters:        []CubeQueryFilterInput{{Member: "orders.status", Operator: "equals", Values: []string{"active"}}},
		TimeDimensions: []CubeTimeDimension{{Dimension: "orders.created_at", Granularity: "month"}},
		Order:          map[string]string{"orders.count": "desc"},
	}
	if err := validateBoundModels(bound, input); err != nil {
		t.Errorf("all in-scope members should pass: %v", err)
	}
}

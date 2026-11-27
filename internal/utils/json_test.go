package utils

import (
	"encoding/json"
	"testing"
)

func TestToJSON(t *testing.T) {
	type sample struct {
		Name string `json:"name"`
		N    int    `json:"n"`
	}
	tests := []struct {
		name  string
		value interface{}
		want  string
	}{
		{"struct", sample{Name: "x", N: 2}, `{"name":"x","n":2}`},
		{"map keys are sorted", map[string]int{"b": 2, "a": 1}, `{"a":1,"b":2}`},
		{"string", "hi", `"hi"`},
		{"nil", nil, "null"},
		{"bool", true, "true"},
		// Values encoding/json cannot marshal must yield "" instead of panicking.
		{"func value", func() {}, ""},
		{"channel value", make(chan int), ""},
	}
	for _, tt := range tests {
		if got := ToJSON(tt.value); got != tt.want {
			t.Errorf("ToJSON(%v) = %q, want %q", tt.value, got, tt.want)
		}
	}
}

func TestGenerateSchemaSmoke(t *testing.T) {
	type sample struct {
		Name string `json:"name"`
	}
	raw := GenerateSchema[sample]()
	var decoded map[string]interface{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("GenerateSchema returned invalid JSON %s: %v", raw, err)
	}
	if _, ok := decoded["properties"]; !ok {
		t.Fatalf("GenerateSchema output %s has no properties", raw)
	}
}

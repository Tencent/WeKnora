package tools

import (
	"encoding/json"
	"testing"
)

func TestCastParams_StringToBool(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"enabled":{"type":"boolean"}}}`)
	args := json.RawMessage(`{"enabled":"true"}`)
	result := CastParams(args, schema)

	var parsed map[string]interface{}
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["enabled"] != true {
		t.Errorf("expected true, got %v (%T)", parsed["enabled"], parsed["enabled"])
	}
}

func TestCastParams_StringToInt(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"count":{"type":"integer"}}}`)
	args := json.RawMessage(`{"count":"42"}`)
	result := CastParams(args, schema)

	var parsed map[string]interface{}
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatal(err)
	}
	// JSON numbers are float64 in Go
	if parsed["count"] != float64(42) {
		t.Errorf("expected 42, got %v (%T)", parsed["count"], parsed["count"])
	}
}

func TestCastParams_StringToFloat(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"score":{"type":"number"}}}`)
	args := json.RawMessage(`{"score":"3.14"}`)
	result := CastParams(args, schema)

	var parsed map[string]interface{}
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["score"] != 3.14 {
		t.Errorf("expected 3.14, got %v", parsed["score"])
	}
}

func TestCastParams_NoChangeNeeded(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}}}`)
	args := json.RawMessage(`{"name":"hello"}`)
	result := CastParams(args, schema)

	if string(result) != string(args) {
		t.Errorf("expected no change, got %s", result)
	}
}

func TestCastParams_NilSchema(t *testing.T) {
	args := json.RawMessage(`{"foo":"bar"}`)
	result := CastParams(args, nil)
	if string(result) != string(args) {
		t.Errorf("expected no change with nil schema")
	}
}

func TestCastParams_BoolFalseString(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"flag":{"type":"boolean"}}}`)
	args := json.RawMessage(`{"flag":"false"}`)
	result := CastParams(args, schema)

	var parsed map[string]interface{}
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["flag"] != false {
		t.Errorf("expected false, got %v (%T)", parsed["flag"], parsed["flag"])
	}
}

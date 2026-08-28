package tools

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestCastParams_StringToStringArray(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"patterns":{"type":"array","items":{"type":"string"}}}}`)
	args := json.RawMessage(`{"patterns":"OpenClaw"}`)
	result := CastParams(args, schema)

	var parsed map[string]interface{}
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatal(err)
	}

	patterns, ok := parsed["patterns"].([]interface{})
	if !ok {
		t.Fatalf("expected patterns to be array, got %T", parsed["patterns"])
	}
	if len(patterns) != 1 || patterns[0] != "OpenClaw" {
		t.Fatalf("expected [OpenClaw], got %v", patterns)
	}
}

// ---- ClampParams ------------------------------------------------------------

func clampSchema() json.RawMessage {
	// Mirrors list_knowledge_chunks limits: limit integer in [1,100], offset >= 0, score float in [0,1]
	return json.RawMessage(`{
  "type":"object",
  "properties":{
    "limit":  {"type":"integer","minimum":1,"maximum":100},
    "offset": {"type":"integer","minimum":0},
    "score":  {"type":"number", "minimum":0,"maximum":1},
    "name":   {"type":"string"}
  }
}`)
}

func parseMap(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	return m
}

func TestClampParams_IntegerMaximum(t *testing.T) {
	schema := clampSchema()
	args := json.RawMessage(`{"limit":500,"name":"unchanged"}`)
	clamped, changed := ClampParams(args, schema)
	require.True(t, changed, "expected over-sized limit to be clamped")
	m := parseMap(t, clamped)
	assert.EqualValues(t, 100, m["limit"], "limit should snap to schema maximum 100")
	assert.Equal(t, "unchanged", m["name"], "string parameters must not be touched")
	// Clamped output must pass ValidateParams afterwards
	require.Empty(t, ValidateParams(clamped, schema))
}

func TestClampParams_IntegerMinimum(t *testing.T) {
	schema := clampSchema()
	args := json.RawMessage(`{"limit":-5,"offset":-1}`)
	clamped, changed := ClampParams(args, schema)
	require.True(t, changed)
	m := parseMap(t, clamped)
	assert.EqualValues(t, 1, m["limit"])
	assert.EqualValues(t, 0, m["offset"])
	require.Empty(t, ValidateParams(clamped, schema))
}

func TestClampParams_NumberBounds(t *testing.T) {
	schema := clampSchema()
	args := json.RawMessage(`{"score":1.5}`)
	clamped, changed := ClampParams(args, schema)
	require.True(t, changed)
	m := parseMap(t, clamped)
	assert.InDelta(t, 1.0, m["score"], 1e-9)
	require.Empty(t, ValidateParams(clamped, schema))
}

func TestClampParams_NoChangeWhenInBounds(t *testing.T) {
	schema := clampSchema()
	args := json.RawMessage(`{"limit":50,"offset":20,"score":0.7,"name":"ok"}`)
	clamped, changed := ClampParams(args, schema)
	assert.False(t, changed, "in-bounds values must not be rewritten")
	assert.Equal(t, string(args), string(clamped))
}

func TestClampParams_NilSchemaOrEmptyArgs(t *testing.T) {
	args := json.RawMessage(`{"limit":1000}`)
	clamped, changed := ClampParams(args, nil)
	assert.False(t, changed)
	assert.Equal(t, string(args), string(clamped))

	clamped, changed = ClampParams(nil, clampSchema())
	assert.False(t, changed)
	assert.Nil(t, clamped)
}

func TestClampParams_ReproduceGraphLimitBug(t *testing.T) {
	// Exact shape of the bug that killed extract-video-knowledge skill:
	// agent-chat returns "parameter 'limit' must be <= 100" when the model
	// asks for the whole document in one list_knowledge_chunks call.
	schema := json.RawMessage(`{
  "type":"object",
  "properties":{
    "knowledge_id":{"type":"string"},
    "limit":       {"type":"integer","minimum":1,"maximum":100},
    "offset":      {"type":"integer","minimum":0}
  }
}`)
	args := json.RawMessage(`{"knowledge_id":"d123","limit":5000}`)
	clamped, changed := ClampParams(args, schema)
	require.True(t, changed)
	// After clamping, validation that used to report `limit must be <= 100` passes.
	require.Empty(t, ValidateParams(clamped, schema))
	m := parseMap(t, clamped)
	assert.EqualValues(t, 100, m["limit"])
	assert.Equal(t, "d123", m["knowledge_id"])
}

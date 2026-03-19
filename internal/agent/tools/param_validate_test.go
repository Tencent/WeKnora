package tools

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateParams(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {"type": "string", "minLength": 1},
			"limit": {"type": "integer", "minimum": 1, "maximum": 100},
			"mode":  {"type": "string", "enum": ["fast", "deep"]},
			"score": {"type": "number", "minimum": 0, "maximum": 1},
			"enabled": {"type": "boolean"}
		},
		"required": ["query"]
	}`)

	t.Run("valid params pass", func(t *testing.T) {
		args := json.RawMessage(`{"query": "hello", "limit": 10, "mode": "fast"}`)
		errs := ValidateParams(args, schema)
		assert.Empty(t, errs)
	})

	t.Run("missing required field", func(t *testing.T) {
		args := json.RawMessage(`{"limit": 10}`)
		errs := ValidateParams(args, schema)
		require.Len(t, errs, 1)
		assert.Equal(t, "query", errs[0].Param)
		assert.Contains(t, errs[0].Message, "required")
	})

	t.Run("null required field", func(t *testing.T) {
		args := json.RawMessage(`{"query": null}`)
		errs := ValidateParams(args, schema)
		require.Len(t, errs, 1)
		assert.Contains(t, errs[0].Message, "required")
	})

	t.Run("wrong type", func(t *testing.T) {
		args := json.RawMessage(`{"query": 123}`)
		errs := ValidateParams(args, schema)
		require.Len(t, errs, 1)
		assert.Equal(t, "query", errs[0].Param)
		assert.Contains(t, errs[0].Message, "type")
	})

	t.Run("enum violation", func(t *testing.T) {
		args := json.RawMessage(`{"query": "test", "mode": "slow"}`)
		errs := ValidateParams(args, schema)
		require.Len(t, errs, 1)
		assert.Equal(t, "mode", errs[0].Param)
		assert.Contains(t, errs[0].Message, "one of")
	})

	t.Run("minimum violation", func(t *testing.T) {
		args := json.RawMessage(`{"query": "test", "limit": 0}`)
		errs := ValidateParams(args, schema)
		require.Len(t, errs, 1)
		assert.Equal(t, "limit", errs[0].Param)
		assert.Contains(t, errs[0].Message, ">= 1")
	})

	t.Run("maximum violation", func(t *testing.T) {
		args := json.RawMessage(`{"query": "test", "limit": 200}`)
		errs := ValidateParams(args, schema)
		require.Len(t, errs, 1)
		assert.Equal(t, "limit", errs[0].Param)
		assert.Contains(t, errs[0].Message, "<= 100")
	})

	t.Run("minLength violation", func(t *testing.T) {
		args := json.RawMessage(`{"query": ""}`)
		errs := ValidateParams(args, schema)
		require.Len(t, errs, 1)
		assert.Equal(t, "query", errs[0].Param)
		assert.Contains(t, errs[0].Message, "at least 1 characters")
	})

	t.Run("number bounds", func(t *testing.T) {
		args := json.RawMessage(`{"query": "test", "score": 1.5}`)
		errs := ValidateParams(args, schema)
		require.Len(t, errs, 1)
		assert.Equal(t, "score", errs[0].Param)
	})

	t.Run("multiple errors", func(t *testing.T) {
		args := json.RawMessage(`{"limit": -1, "mode": "invalid"}`)
		errs := ValidateParams(args, schema)
		assert.GreaterOrEqual(t, len(errs), 3) // missing query + limit min + mode enum
	})

	t.Run("extra params allowed", func(t *testing.T) {
		args := json.RawMessage(`{"query": "test", "unknown_param": "value"}`)
		errs := ValidateParams(args, schema)
		assert.Empty(t, errs)
	})

	t.Run("nil schema returns nil", func(t *testing.T) {
		args := json.RawMessage(`{"query": "test"}`)
		errs := ValidateParams(args, nil)
		assert.Nil(t, errs)
	})

	t.Run("empty args returns nil", func(t *testing.T) {
		errs := ValidateParams(nil, schema)
		assert.Nil(t, errs)
	})

	t.Run("boolean type check", func(t *testing.T) {
		args := json.RawMessage(`{"query": "test", "enabled": "yes"}`)
		errs := ValidateParams(args, schema)
		require.Len(t, errs, 1)
		assert.Equal(t, "enabled", errs[0].Param)
		assert.Contains(t, errs[0].Message, "boolean")
	})
}

func TestFormatValidationErrors(t *testing.T) {
	t.Run("empty errors", func(t *testing.T) {
		assert.Equal(t, "", FormatValidationErrors(nil))
	})

	t.Run("single error", func(t *testing.T) {
		errs := []ValidationError{{Param: "q", Message: "required parameter 'q' is missing"}}
		result := FormatValidationErrors(errs)
		assert.Contains(t, result, "Parameter validation failed")
		assert.Contains(t, result, "required parameter 'q' is missing")
	})

	t.Run("multiple errors joined", func(t *testing.T) {
		errs := []ValidationError{
			{Param: "a", Message: "error a"},
			{Param: "b", Message: "error b"},
		}
		result := FormatValidationErrors(errs)
		assert.Contains(t, result, "error a; error b")
	})
}

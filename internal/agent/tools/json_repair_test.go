package tools

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRepairJSON(t *testing.T) {
	t.Run("valid JSON unchanged", func(t *testing.T) {
		input := `{"query": "hello", "limit": 10}`
		result := RepairJSON(input)
		assert.Equal(t, input, result)
		assert.True(t, json.Valid([]byte(result)))
	})

	t.Run("trailing comma before brace", func(t *testing.T) {
		input := `{"query": "hello", "limit": 10,}`
		result := RepairJSON(input)
		assert.True(t, json.Valid([]byte(result)), "result: %s", result)
	})

	t.Run("trailing comma before bracket", func(t *testing.T) {
		input := `{"items": [1, 2, 3,]}`
		result := RepairJSON(input)
		assert.True(t, json.Valid([]byte(result)), "result: %s", result)
	})

	t.Run("missing closing brace", func(t *testing.T) {
		input := `{"query": "hello"`
		result := RepairJSON(input)
		assert.True(t, json.Valid([]byte(result)), "result: %s", result)
	})

	t.Run("missing closing bracket and brace", func(t *testing.T) {
		input := `{"items": [1, 2, 3`
		result := RepairJSON(input)
		assert.True(t, json.Valid([]byte(result)), "result: %s", result)
	})

	t.Run("empty string returns empty object", func(t *testing.T) {
		result := RepairJSON("")
		assert.Equal(t, "{}", result)
	})

	t.Run("truncated string value", func(t *testing.T) {
		input := `{"query": "hello world`
		result := RepairJSON(input)
		assert.True(t, json.Valid([]byte(result)), "result: %s", result)
	})

	t.Run("nested missing closers", func(t *testing.T) {
		input := `{"outer": {"inner": [1, 2`
		result := RepairJSON(input)
		assert.True(t, json.Valid([]byte(result)), "result: %s", result)
	})

	t.Run("comma in string not removed", func(t *testing.T) {
		input := `{"query": "hello, world"}`
		result := RepairJSON(input)
		assert.Equal(t, input, result)
	})

	t.Run("already valid complex JSON", func(t *testing.T) {
		input := `{"queries": ["what is AI", "machine learning"], "limit": 5}`
		result := RepairJSON(input)
		assert.Equal(t, input, result)
		assert.True(t, json.Valid([]byte(result)))
	})

	t.Run("multiple trailing commas", func(t *testing.T) {
		input := `{"a": 1, "b": [1, 2,], "c": 3,}`
		result := RepairJSON(input)
		assert.True(t, json.Valid([]byte(result)), "result: %s", result)
	})
}

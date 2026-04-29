package tools

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

	t.Run("invalid escape: regex plus (C\\+\\+)", func(t *testing.T) {
		// The LLM emits a raw regex without double-escaping — \+ is not a
		// legal JSON escape. Repair should rewrite it to \\+ so the
		// unmarshalled string is the regex pattern `C\+\+`, matching
		// literal `C++`.
		input := `{"queries": ["C\+\+"]}`
		result := RepairJSON(input)
		assert.True(t, json.Valid([]byte(result)), "result: %s", result)

		var parsed struct {
			Queries []string `json:"queries"`
		}
		require.NoError(t, json.Unmarshal([]byte(result), &parsed))
		assert.Equal(t, []string{`C\+\+`}, parsed.Queries)
	})

	t.Run("invalid escape: common regex shorthand", func(t *testing.T) {
		input := `{"queries": ["\d+\.\d+", "\bword\b", "foo\|bar"]}`
		result := RepairJSON(input)
		assert.True(t, json.Valid([]byte(result)), "result: %s", result)

		var parsed struct {
			Queries []string `json:"queries"`
		}
		require.NoError(t, json.Unmarshal([]byte(result), &parsed))
		// \b is a VALID JSON escape (backspace) — the LLM gets what it asked
		// for (a backspace char), we don't silently rewrite valid escapes.
		// \d, \., \| are invalid → repaired to literal backslash sequences.
		assert.Equal(t, `\d+\.\d+`, parsed.Queries[0])
		assert.Equal(t, "\bword\b", parsed.Queries[1])
		assert.Equal(t, `foo\|bar`, parsed.Queries[2])
	})

	t.Run("invalid escape repair is idempotent", func(t *testing.T) {
		input := `{"queries": ["C\+\+"]}`
		once := RepairJSON(input)
		twice := RepairJSON(once)
		assert.Equal(t, once, twice)
	})

	t.Run("valid escapes are preserved", func(t *testing.T) {
		// Valid JSON escapes must pass through untouched.
		input := `{"path": "C:\\Users\\x", "line": "a\nb", "quote": "say \"hi\""}`
		result := RepairJSON(input)
		assert.Equal(t, input, result)
		assert.True(t, json.Valid([]byte(result)))
	})
}

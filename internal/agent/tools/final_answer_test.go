package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestParseFinalAnswerArgs covers the three-tier recovery path used by both
// the final_answer tool and the ReAct loop's terminal detection (issue #1008).
func TestParseFinalAnswerArgs(t *testing.T) {
	t.Run("strict JSON is parsed as-is", func(t *testing.T) {
		got, ok := ParseFinalAnswerArgs(`{"answer": "Hello, world."}`)
		assert.True(t, ok)
		assert.Equal(t, "Hello, world.", got)
	})

	t.Run("trailing comma is recovered via RepairJSON", func(t *testing.T) {
		got, ok := ParseFinalAnswerArgs(`{"answer": "Hi",}`)
		assert.True(t, ok)
		assert.Equal(t, "Hi", got)
	})

	t.Run("missing closing brace is recovered via RepairJSON", func(t *testing.T) {
		got, ok := ParseFinalAnswerArgs(`{"answer": "Hi"`)
		assert.True(t, ok)
		assert.Equal(t, "Hi", got)
	})

	t.Run("invalid backslash escape is recovered via RepairJSON", func(t *testing.T) {
		// LLM forgot to double-escape the regex metachar — RepairJSON should
		// rewrite "\+" to "\\+" so Unmarshal succeeds.
		got, ok := ParseFinalAnswerArgs(`{"answer": "C\+\+"}`)
		assert.True(t, ok)
		assert.Equal(t, `C\+\+`, got)
	})

	t.Run("unescaped inner quote is recovered via regex fallback", func(t *testing.T) {
		// Neither strict parse nor RepairJSON can recover this one; the regex
		// still captures the well-formed prefix up to the rogue quote.
		raw := `{"answer": "She said "hello" to me"}`
		got, ok := ParseFinalAnswerArgs(raw)
		assert.True(t, ok)
		assert.NotEmpty(t, got)
	})

	t.Run("missing answer field returns not ok", func(t *testing.T) {
		_, ok := ParseFinalAnswerArgs(`{"other": "value"}`)
		assert.False(t, ok)
	})

	t.Run("empty answer returns not ok", func(t *testing.T) {
		_, ok := ParseFinalAnswerArgs(`{"answer": ""}`)
		assert.False(t, ok)
	})

	t.Run("completely garbled input returns not ok", func(t *testing.T) {
		_, ok := ParseFinalAnswerArgs(`not json at all`)
		assert.False(t, ok)
	})
}

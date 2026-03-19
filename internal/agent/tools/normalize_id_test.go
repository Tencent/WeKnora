package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeToolCallID(t *testing.T) {
	t.Run("normal ID unchanged", func(t *testing.T) {
		result := NormalizeToolCallID("call_abc123", "search", 0)
		assert.Equal(t, "call_abc123", result)
	})

	t.Run("empty ID generates deterministic ID", func(t *testing.T) {
		id1 := NormalizeToolCallID("", "search", 0)
		id2 := NormalizeToolCallID("", "search", 0)
		id3 := NormalizeToolCallID("", "search", 1)

		assert.NotEmpty(t, id1)
		assert.Equal(t, id1, id2, "same tool+index should produce same ID")
		assert.NotEqual(t, id1, id3, "different index should produce different ID")
		assert.True(t, len(id1) > 0 && len(id1) <= maxToolCallIDLen)
	})

	t.Run("whitespace-only ID treated as empty", func(t *testing.T) {
		id := NormalizeToolCallID("   ", "search", 0)
		assert.NotEmpty(t, id)
		assert.True(t, len(id) <= maxToolCallIDLen)
	})

	t.Run("special characters replaced", func(t *testing.T) {
		id := NormalizeToolCallID("call/foo@bar#baz", "search", 0)
		assert.NotContains(t, id, "/")
		assert.NotContains(t, id, "@")
		assert.NotContains(t, id, "#")
	})

	t.Run("long ID truncated with hash", func(t *testing.T) {
		longID := "call_" + string(make([]byte, 200))
		id := NormalizeToolCallID(longID, "search", 0)
		assert.LessOrEqual(t, len(id), maxToolCallIDLen)
	})

	t.Run("ID at exact limit unchanged", func(t *testing.T) {
		exactID := "call_" + "a"
		for len(exactID) < maxToolCallIDLen {
			exactID += "a"
		}
		id := NormalizeToolCallID(exactID, "search", 0)
		assert.Equal(t, exactID, id)
	})
}

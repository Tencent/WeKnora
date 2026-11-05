package tools

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeToolCallIDPreservesDistinctIDs(t *testing.T) {
	for _, tc := range []struct {
		name  string
		left  string
		right string
	}{
		{"different punctuation", "call/a", "call?a"},
		{"safe and unsafe", "call/a", "call_a"},
		{"different unicode", "call_甲", "call_乙"},
		{"long unsafe IDs", strings.Repeat("a", 80) + "/", strings.Repeat("a", 80) + "?"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			left := NormalizeToolCallID(tc.left, "search", 0)
			right := NormalizeToolCallID(tc.right, "search", 0)
			assert.NotEqual(t, left, right, "distinct provider IDs must not collapse during sanitization")
			for _, pair := range [][2]string{{tc.left, left}, {tc.right, right}} {
				assert.NotEmpty(t, pair[1])
				assert.LessOrEqual(t, len(pair[1]), maxToolCallIDLen)
				assert.NotRegexp(t, validIDChars, pair[1])
				assert.Equal(t, pair[1], NormalizeToolCallID(pair[0], "search", 0), "must be deterministic")
				assert.Equal(t, pair[1], NormalizeToolCallID(pair[1], "search", 0), "must be idempotent")
			}
		})
	}
}

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

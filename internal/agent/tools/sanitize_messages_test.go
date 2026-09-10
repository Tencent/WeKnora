package tools

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/models/invoke"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSanitizeMessages(t *testing.T) {
	t.Run("normal messages unchanged", func(t *testing.T) {
		messages := []invoke.Message{
			{Role: "system", Content: []invoke.Part{{Text: "You are helpful"}}},
			{Role: "user", Content: []invoke.Part{{Text: "Hello"}}},
			{Role: "assistant", Content: []invoke.Part{{Text: "Hi there"}}},
		}
		result := SanitizeMessages(messages)
		assert.Len(t, result, 3)
	})

	t.Run("consecutive user messages merged", func(t *testing.T) {
		messages := []invoke.Message{
			{Role: "system", Content: []invoke.Part{{Text: "You are helpful"}}},
			{Role: "user", Content: []invoke.Part{{Text: "Hello"}}},
			{Role: "user", Content: []invoke.Part{{Text: "How are you?"}}},
		}
		result := SanitizeMessages(messages)
		require.Len(t, result, 2) // system + merged user
		assert.Contains(t, result[1].Text(), "Hello")
		assert.Contains(t, result[1].Text(), "How are you?")
	})

	t.Run("consecutive tool messages not merged", func(t *testing.T) {
		messages := []invoke.Message{
			{Role: "system", Content: []invoke.Part{{Text: "system"}}},
			{Role: "assistant", Content: []invoke.Part{{Text: "thinking"}}, ToolCalls: []invoke.ToolCall{
				{ID: "call_1"}, {ID: "call_2"},
			}},
			invoke.TextMessage("tool", "result1"),
			invoke.TextMessage("tool", "result2"),
		}
		result := SanitizeMessages(messages)
		assert.Len(t, result, 4) // all preserved
	})

	t.Run("empty content messages removed and consecutive merged", func(t *testing.T) {
		messages := []invoke.Message{
			{Role: "system", Content: []invoke.Part{{Text: "system"}}},
			{Role: "user", Content: []invoke.Part{{Text: "hello"}}},
			{Role: "assistant", Content: []invoke.Part{{Text: ""}}},
			{Role: "user", Content: []invoke.Part{{Text: "bye"}}},
		}
		result := SanitizeMessages(messages)
		// empty assistant removed → two user messages merge
		assert.Len(t, result, 2)
		assert.Contains(t, result[1].Text(), "hello")
		assert.Contains(t, result[1].Text(), "bye")
	})

	t.Run("empty system message preserved", func(t *testing.T) {
		messages := []invoke.Message{
			{Role: "system", Content: []invoke.Part{{Text: ""}}},
			{Role: "user", Content: []invoke.Part{{Text: "hello"}}},
		}
		result := SanitizeMessages(messages)
		assert.Len(t, result, 2) // system preserved even if empty
	})

	t.Run("orphaned tool result converted", func(t *testing.T) {
		messages := []invoke.Message{
			{Role: "system", Content: []invoke.Part{{Text: "system"}}},
			{
				Role:       "tool",
				ToolCallID: "nonexistent_id", Name: "search",
			},
		}
		result := SanitizeMessages(messages)
		require.Len(t, result, 2)
		assert.Equal(t, invoke.RoleSystem, result[1].Role) // converted
		assert.Contains(t, result[1].Text(), "search")
	})

	t.Run("empty slice", func(t *testing.T) {
		result := SanitizeMessages(nil)
		assert.Empty(t, result)
	})
}

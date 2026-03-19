package tools

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSanitizeMessages(t *testing.T) {
	t.Run("normal messages unchanged", func(t *testing.T) {
		messages := []chat.Message{
			{Role: "system", Content: "You are helpful"},
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi there"},
		}
		result := SanitizeMessages(messages)
		assert.Len(t, result, 3)
	})

	t.Run("consecutive user messages merged", func(t *testing.T) {
		messages := []chat.Message{
			{Role: "system", Content: "You are helpful"},
			{Role: "user", Content: "Hello"},
			{Role: "user", Content: "How are you?"},
		}
		result := SanitizeMessages(messages)
		require.Len(t, result, 2) // system + merged user
		assert.Contains(t, result[1].Content, "Hello")
		assert.Contains(t, result[1].Content, "How are you?")
	})

	t.Run("consecutive tool messages not merged", func(t *testing.T) {
		messages := []chat.Message{
			{Role: "system", Content: "system"},
			{Role: "assistant", Content: "thinking", ToolCalls: []chat.ToolCall{
				{ID: "call_1"}, {ID: "call_2"},
			}},
			{Role: "tool", Content: "result1", ToolCallID: "call_1"},
			{Role: "tool", Content: "result2", ToolCallID: "call_2"},
		}
		result := SanitizeMessages(messages)
		assert.Len(t, result, 4) // all preserved
	})

	t.Run("empty content messages removed and consecutive merged", func(t *testing.T) {
		messages := []chat.Message{
			{Role: "system", Content: "system"},
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: ""},
			{Role: "user", Content: "bye"},
		}
		result := SanitizeMessages(messages)
		// empty assistant removed → two user messages merge
		assert.Len(t, result, 2)
		assert.Contains(t, result[1].Content, "hello")
		assert.Contains(t, result[1].Content, "bye")
	})

	t.Run("empty system message preserved", func(t *testing.T) {
		messages := []chat.Message{
			{Role: "system", Content: ""},
			{Role: "user", Content: "hello"},
		}
		result := SanitizeMessages(messages)
		assert.Len(t, result, 2) // system preserved even if empty
	})

	t.Run("orphaned tool result converted", func(t *testing.T) {
		messages := []chat.Message{
			{Role: "system", Content: "system"},
			{Role: "tool", Content: "some result",
				ToolCallID: "nonexistent_id", Name: "search"},
		}
		result := SanitizeMessages(messages)
		require.Len(t, result, 2)
		assert.Equal(t, "system", result[1].Role) // converted
		assert.Contains(t, result[1].Content, "search")
	})

	t.Run("empty slice", func(t *testing.T) {
		result := SanitizeMessages(nil)
		assert.Empty(t, result)
	})
}

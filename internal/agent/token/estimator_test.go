package token

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEstimator(t *testing.T) {
	e := NewEstimator(0) // use default

	t.Run("empty string", func(t *testing.T) {
		assert.Equal(t, 0, e.EstimateString(""))
	})

	t.Run("english text", func(t *testing.T) {
		// "hello world" = 11 chars ≈ 11/3.5 + 1 ≈ 4 tokens
		tokens := e.EstimateString("hello world")
		assert.Greater(t, tokens, 0)
		assert.Less(t, tokens, 20)
	})

	t.Run("chinese text", func(t *testing.T) {
		// 10 Chinese chars at ~1.5 chars/token ≈ 7 tokens
		tokens := e.EstimateString("你好世界测试数据中文")
		assert.Greater(t, tokens, 3)
		assert.Less(t, tokens, 20)
	})

	t.Run("CJK is more tokens per char than latin", func(t *testing.T) {
		// Same number of characters, but CJK should estimate more tokens
		latin := strings.Repeat("a", 100)
		cjk := strings.Repeat("中", 100)
		latinTokens := e.EstimateString(latin)
		cjkTokens := e.EstimateString(cjk)
		assert.Greater(t, cjkTokens, latinTokens,
			"CJK text should estimate more tokens per character")
	})

	t.Run("message estimation includes overhead", func(t *testing.T) {
		msg := chat.Message{
			Role:    "assistant",
			Content: "hello",
		}
		tokens := e.EstimateMessage(&msg)
		contentTokens := e.EstimateString("hello")
		assert.Greater(t, tokens, contentTokens,
			"message tokens should include overhead beyond just content")
	})

	t.Run("message with tool calls", func(t *testing.T) {
		msg := chat.Message{
			Role:    "assistant",
			Content: "thinking...",
			ToolCalls: []chat.ToolCall{
				{
					Function: chat.FunctionCall{
						Name:      "knowledge_search",
						Arguments: `{"query": "test"}`,
					},
				},
			},
		}
		tokens := e.EstimateMessage(&msg)
		assert.Greater(t, tokens, 10)
	})

	t.Run("estimate messages", func(t *testing.T) {
		messages := []chat.Message{
			{Role: "system", Content: "You are a helpful assistant."},
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi there!"},
		}
		tokens := e.EstimateMessages(messages)
		assert.Greater(t, tokens, 10)
	})
}

func TestCompressContext(t *testing.T) {
	e := NewEstimator(0)

	t.Run("no compression needed", func(t *testing.T) {
		messages := []chat.Message{
			{Role: "system", Content: "system prompt"},
			{Role: "user", Content: "hello"},
		}
		result := CompressContext(messages, e, 100000)
		assert.Equal(t, messages, result)
	})

	t.Run("preserves system and last message", func(t *testing.T) {
		// Create messages that exceed the token limit
		messages := []chat.Message{
			{Role: "system", Content: "system prompt"},
			{Role: "user", Content: strings.Repeat("old message ", 1000)},
			{Role: "assistant", Content: strings.Repeat("old response ", 1000)},
			{Role: "user", Content: "current query"},
		}
		result := CompressContext(messages, e, 100) // very low limit to force compression
		require.GreaterOrEqual(t, len(result), 2)
		assert.Equal(t, "system", result[0].Role)
		assert.Equal(t, "current query", result[len(result)-1].Content)
	})

	t.Run("keeps tool_call and tool_result paired", func(t *testing.T) {
		messages := []chat.Message{
			{Role: "system", Content: "system prompt"},
			// Old conversation
			{Role: "user", Content: strings.Repeat("x", 500)},
			{Role: "assistant", Content: strings.Repeat("y", 500)},
			// Tool interaction that should stay together
			{Role: "assistant", Content: "thinking", ToolCalls: []chat.ToolCall{
				{ID: "call_1", Function: chat.FunctionCall{Name: "search", Arguments: `{"q":"test"}`}},
			}},
			{Role: "tool", Content: strings.Repeat("result ", 100), ToolCallID: "call_1", Name: "search"},
			// Current query
			{Role: "user", Content: "what did you find?"},
		}

		result := CompressContext(messages, e, 200) // force some compression

		b, _ := json.Marshal(result)
		fmt.Println(string(b))

		// Check that tool_call and tool_result are still paired
		for i, msg := range result {
			if msg.Role == "tool" {
				// The previous message should be an assistant with tool_calls
				require.Greater(t, i, 0)
				assert.Equal(t, "assistant", result[i-1].Role)
				assert.Greater(t, len(result[i-1].ToolCalls), 0)
			}
		}
	})

	t.Run("zero maxTokens returns unchanged", func(t *testing.T) {
		messages := []chat.Message{
			{Role: "system", Content: "system"},
			{Role: "user", Content: "hello"},
		}
		result := CompressContext(messages, e, 0)
		assert.Equal(t, messages, result)
	})

	t.Run("two messages returns unchanged", func(t *testing.T) {
		messages := []chat.Message{
			{Role: "system", Content: "system"},
			{Role: "user", Content: "hello"},
		}
		result := CompressContext(messages, e, 1) // even with tiny limit
		assert.Equal(t, messages, result)
	})
}

func TestGroupToolMessages(t *testing.T) {
	t.Run("standalone messages", func(t *testing.T) {
		messages := []chat.Message{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi"},
		}
		groups := groupToolMessages(messages)
		assert.Len(t, groups, 2)
	})

	t.Run("tool call with results grouped", func(t *testing.T) {
		messages := []chat.Message{
			{Role: "user", Content: "search for X"},
			{Role: "assistant", Content: "thinking", ToolCalls: []chat.ToolCall{
				{ID: "call_1", Function: chat.FunctionCall{Name: "search"}},
				{ID: "call_2", Function: chat.FunctionCall{Name: "fetch"}},
			}},
			{Role: "tool", Content: "result1", ToolCallID: "call_1"},
			{Role: "tool", Content: "result2", ToolCallID: "call_2"},
			{Role: "user", Content: "thanks"},
		}
		groups := groupToolMessages(messages)
		assert.Len(t, groups, 3)    // user, (assistant+2 tools), user
		assert.Len(t, groups[1], 3) // assistant + 2 tool results
		b, _ := json.Marshal(groups)
		fmt.Println(string(b))
	})
}

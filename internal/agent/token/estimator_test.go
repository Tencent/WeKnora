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
	e, err := NewEstimator()
	if err != nil {
		t.Fatalf("Failed to create estimator: %v", err)
	}

	t.Run("empty string", func(t *testing.T) {
		assert.Equal(t, 0, e.EstimateString(""))
	})

	t.Run("english text", func(t *testing.T) {
		tokens := e.EstimateString("hello world")
		assert.Equal(t, 2, tokens)
	})

	t.Run("chinese text", func(t *testing.T) {
		tokens := e.EstimateString("你好世界测试数据中文")
		fmt.Println(tokens)
		assert.Greater(t, tokens, 0)
	})

	t.Run("CJK produces more tokens per char than latin", func(t *testing.T) {
		latin := strings.Repeat("a", 100)
		cjk := strings.Repeat("中", 100)
		latinTokens := e.EstimateString(latin)
		cjkTokens := e.EstimateString(cjk)
		fmt.Println(latinTokens, cjkTokens)
		assert.Greater(t, cjkTokens, latinTokens,
			"CJK text should produce more tokens per character")
	})

	t.Run("message estimation includes overhead", func(t *testing.T) {
		msg := chat.Message{
			Role:    "assistant",
			Content: "hello",
		}
		tokens := e.EstimateMessage(&msg)
		contentTokens := e.EstimateString("hello")
		fmt.Println(tokens, contentTokens)
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
	e, err := NewEstimator()
	require.NoError(t, err)

	t.Run("no compression needed", func(t *testing.T) {
		messages := []chat.Message{
			{Role: "system", Content: "system prompt"},
			{Role: "user", Content: "hello"},
		}
		tokens := e.EstimateMessages(messages)
		result := CompressContext(messages, e, 100000, tokens)
		assert.Equal(t, messages, result)
	})

	t.Run("preserves system and last message", func(t *testing.T) {
		messages := []chat.Message{
			{Role: "system", Content: "system prompt"},
			{Role: "user", Content: strings.Repeat("old message ", 1000)},
			{Role: "assistant", Content: strings.Repeat("old response ", 1000)},
			{Role: "user", Content: "current query"},
		}
		tokens := e.EstimateMessages(messages)
		result := CompressContext(messages, e, 100, tokens)
		require.GreaterOrEqual(t, len(result), 2)
		assert.Equal(t, "system", result[0].Role)
		assert.Equal(t, "current query", result[len(result)-1].Content)
	})

	t.Run("keeps tool_call and tool_result paired", func(t *testing.T) {
		messages := []chat.Message{
			{Role: "system", Content: "system prompt"},
			{Role: "user", Content: strings.Repeat("x", 500)},
			{Role: "assistant", Content: strings.Repeat("y", 500)},
			{Role: "assistant", Content: "thinking", ToolCalls: []chat.ToolCall{
				{ID: "call_1", Function: chat.FunctionCall{Name: "search", Arguments: `{"q":"test"}`}},
			}},
			{Role: "tool", Content: strings.Repeat("result ", 100), ToolCallID: "call_1", Name: "search"},
			{Role: "user", Content: "what did you find?"},
		}

		tokens := e.EstimateMessages(messages)
		result := CompressContext(messages, e, 200, tokens)

		b, _ := json.Marshal(result)
		fmt.Println(string(b))

		for i, msg := range result {
			if msg.Role == "tool" {
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
		result := CompressContext(messages, e, 0, 0)
		assert.Equal(t, messages, result)
	})

	t.Run("two messages returns unchanged", func(t *testing.T) {
		messages := []chat.Message{
			{Role: "system", Content: "system"},
			{Role: "user", Content: "hello"},
		}
		result := CompressContext(messages, e, 1, 999)
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
		assert.Len(t, groups, 3)
		assert.Len(t, groups[1], 3)
		b, _ := json.Marshal(groups)
		fmt.Println(string(b))
	})
}

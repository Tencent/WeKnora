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

	t.Run("round2+: user query not at end, preserves current turn tail", func(t *testing.T) {
		longText := strings.Repeat("filler content ", 200)
		messages := []chat.Message{
			{Role: "system", Content: "system prompt"},
			// old history
			{Role: "user", Content: longText},
			{Role: "assistant", Content: longText},
			{Role: "user", Content: longText},
			{Role: "assistant", Content: longText},
			// current turn
			{Role: "user", Content: "current question"},
			{Role: "assistant", Content: "let me search", ToolCalls: []chat.ToolCall{
				{ID: "c1", Function: chat.FunctionCall{Name: "search", Arguments: `{"q":"test"}`}},
			}},
			{Role: "tool", Content: "search results", ToolCallID: "c1", Name: "search"},
		}

		tokens := e.EstimateMessages(messages)
		result := CompressContext(messages, e, 300, tokens)

		// System prompt preserved.
		assert.Equal(t, "system", result[0].Role)
		assert.Equal(t, "system prompt", result[0].Content)

		// Current turn tail must be fully intact.
		userIdx := -1
		for i, m := range result {
			if m.Role == "user" && m.Content == "current question" {
				userIdx = i
				break
			}
		}
		require.NotEqual(t, -1, userIdx, "user query must be preserved")
		require.Greater(t, len(result), userIdx+2, "assistant + tool must follow")
		assert.Equal(t, "assistant", result[userIdx+1].Role)
		assert.Equal(t, "let me search", result[userIdx+1].Content)
		assert.Equal(t, "tool", result[userIdx+2].Role)
		assert.Equal(t, "search results", result[userIdx+2].Content)

		// Should be shorter than original (old history trimmed).
		assert.Less(t, len(result), len(messages))
	})

	t.Run("round2+: multiple tool results after user query all preserved", func(t *testing.T) {
		longText := strings.Repeat("data ", 300)
		messages := []chat.Message{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: longText},
			{Role: "assistant", Content: longText},
			// current turn with parallel tool calls
			{Role: "user", Content: "do things"},
			{Role: "assistant", Content: "ok", ToolCalls: []chat.ToolCall{
				{ID: "c1", Function: chat.FunctionCall{Name: "t1"}},
				{ID: "c2", Function: chat.FunctionCall{Name: "t2"}},
			}},
			{Role: "tool", Content: "res1", ToolCallID: "c1", Name: "t1"},
			{Role: "tool", Content: "res2", ToolCallID: "c2", Name: "t2"},
		}

		tokens := e.EstimateMessages(messages)
		result := CompressContext(messages, e, 200, tokens)

		// Both tool results must be present.
		var toolNames []string
		for _, m := range result {
			if m.Role == "tool" {
				toolNames = append(toolNames, m.Name)
			}
		}
		assert.Contains(t, toolNames, "t1")
		assert.Contains(t, toolNames, "t2")

		// User query preserved.
		found := false
		for _, m := range result {
			if m.Content == "do things" {
				found = true
			}
		}
		assert.True(t, found)
	})

	t.Run("round2+: no history between system and user query returns unchanged", func(t *testing.T) {
		messages := []chat.Message{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "thinking", ToolCalls: []chat.ToolCall{
				{ID: "c1", Function: chat.FunctionCall{Name: "t1"}},
			}},
			{Role: "tool", Content: "done", ToolCallID: "c1", Name: "t1"},
		}
		tokens := e.EstimateMessages(messages)
		result := CompressContext(messages, e, 10, tokens)
		assert.Equal(t, messages, result, "no history to trim → unchanged")
	})

	t.Run("no user message at all returns unchanged", func(t *testing.T) {
		messages := []chat.Message{
			{Role: "system", Content: "sys"},
			{Role: "assistant", Content: strings.Repeat("x", 1000)},
			{Role: "assistant", Content: strings.Repeat("y", 1000)},
		}
		tokens := e.EstimateMessages(messages)
		result := CompressContext(messages, e, 10, tokens)
		// lastUserIdx defaults to len-1 (last msg), history = messages[1:last] = [assistant]
		// This still does its best—the important thing is it doesn't panic.
		assert.NotNil(t, result)
	})

	t.Run("round2+: tool pair in history never split", func(t *testing.T) {
		longText := strings.Repeat("verbose ", 200)
		messages := []chat.Message{
			{Role: "system", Content: "sys"},
			// old turn 1
			{Role: "user", Content: "old query 1"},
			{Role: "assistant", Content: longText, ToolCalls: []chat.ToolCall{
				{ID: "old1", Function: chat.FunctionCall{Name: "old_tool"}},
			}},
			{Role: "tool", Content: longText, ToolCallID: "old1", Name: "old_tool"},
			// old turn 2
			{Role: "user", Content: "old query 2"},
			{Role: "assistant", Content: "short reply"},
			// current turn
			{Role: "user", Content: "current"},
			{Role: "assistant", Content: "working", ToolCalls: []chat.ToolCall{
				{ID: "new1", Function: chat.FunctionCall{Name: "new_tool"}},
			}},
			{Role: "tool", Content: "result", ToolCallID: "new1", Name: "new_tool"},
		}

		tokens := e.EstimateMessages(messages)
		result := CompressContext(messages, e, 300, tokens)

		// Verify no orphaned tool message: every "tool" msg must be preceded by
		// an assistant with tool_calls.
		for i, m := range result {
			if m.Role == "tool" {
				require.Greater(t, i, 0, "tool message at index 0 is impossible")
				prev := result[i-1]
				isPairedTool := prev.Role == "tool"
				isPairedAssistant := prev.Role == "assistant" && len(prev.ToolCalls) > 0
				assert.True(t, isPairedTool || isPairedAssistant,
					"tool message at %d must be preceded by assistant+tool_calls or another tool, got %s", i, prev.Role)
			}
		}
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

package memory

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/token"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/stretchr/testify/assert"
)

func TestConsolidator_ShouldConsolidate(t *testing.T) {
	est := token.NewEstimator(0)

	t.Run("below threshold returns false", func(t *testing.T) {
		c := NewConsolidator(nil, est, 100000, 0)
		messages := []chat.Message{
			{Role: "system", Content: "system prompt"},
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi"},
			{Role: "user", Content: "current"},
		}
		assert.False(t, c.ShouldConsolidate(messages))
	})

	t.Run("too few messages returns false", func(t *testing.T) {
		c := NewConsolidator(nil, est, 10, 0)
		messages := []chat.Message{
			{Role: "system", Content: "system"},
			{Role: "user", Content: "hello"},
		}
		assert.False(t, c.ShouldConsolidate(messages))
	})

	t.Run("disabled returns false", func(t *testing.T) {
		c := NewConsolidator(nil, est, 0, 0) // disabled
		messages := []chat.Message{
			{Role: "system", Content: "system"},
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi"},
			{Role: "user", Content: "current"},
		}
		assert.False(t, c.ShouldConsolidate(messages))
	})
}

func TestTruncateForPrompt(t *testing.T) {
	t.Run("short string unchanged", func(t *testing.T) {
		assert.Equal(t, "hello", truncateForPrompt("hello", 100))
	})

	t.Run("long string truncated", func(t *testing.T) {
		result := truncateForPrompt("hello world this is long", 10)
		assert.Equal(t, "hello worl...", result)
	})

	t.Run("CJK string truncated by rune", func(t *testing.T) {
		input := "你好世界测试数据中文字符串"
		result := truncateForPrompt(input, 5)
		assert.Equal(t, "你好世界测...", result)
	})
}

func TestRawArchive(t *testing.T) {
	est := token.NewEstimator(0)
	c := NewConsolidator(nil, est, 100000, 0)

	messages := []chat.Message{
		{Role: "user", Content: "search for X"},
		{
			Role: "assistant", Content: "let me search",
			ToolCalls: []chat.ToolCall{
				{Function: chat.FunctionCall{Name: "knowledge_search"}},
			},
		},
		{Role: "tool", Content: "result data", Name: "knowledge_search"},
	}

	result := c.rawArchive(messages)
	assert.Contains(t, result, "Raw conversation archive")
	assert.Contains(t, result, "User: search for X")
	assert.Contains(t, result, "knowledge_search")
	assert.Contains(t, result, "Tool[knowledge_search]: result data")
}

func TestBuildConsolidationPrompt(t *testing.T) {
	est := token.NewEstimator(0)
	c := NewConsolidator(nil, est, 100000, 0)

	messages := []chat.Message{
		{Role: "user", Content: "find info about AI"},
		{Role: "assistant", Content: "searching...", ToolCalls: []chat.ToolCall{
			{Function: chat.FunctionCall{Name: "web_search"}},
		}},
		{Role: "tool", Content: "results here", Name: "web_search"},
	}

	prompt := c.buildConsolidationPrompt(messages)
	assert.Contains(t, prompt, "**User**: find info about AI")
	assert.Contains(t, prompt, "web_search")
	assert.Contains(t, prompt, "**Tool [web_search]**: results here")
}

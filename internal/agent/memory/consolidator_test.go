package memory

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/token"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubChat is a minimal chat.Chat implementation for testing consolidation.
type stubChat struct {
	response string
	err      error
}

func (s *stubChat) Chat(_ context.Context, _ []chat.Message, _ *chat.ChatOptions) (*types.ChatResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &types.ChatResponse{Content: s.response}, nil
}

func (s *stubChat) ChatStream(context.Context, []chat.Message, *chat.ChatOptions) (<-chan types.StreamResponse, error) {
	return nil, nil
}

func (s *stubChat) GetModelName() string { return "stub" }
func (s *stubChat) GetModelID() string   { return "stub" }

func TestConsolidator_ShouldConsolidate(t *testing.T) {
	est, err := token.NewEstimator()
	assert.NoError(t, err)

	t.Run("below threshold returns false", func(t *testing.T) {
		c := NewConsolidator(nil, est, 100000, 0)
		messages := []chat.Message{
			{Role: "system", Content: "system prompt"},
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi"},
			{Role: "user", Content: "current"},
		}
		tokens := est.EstimateMessages(messages)
		assert.False(t, c.ShouldConsolidate(tokens))
	})

	t.Run("over threshold returns true", func(t *testing.T) {
		c := NewConsolidator(nil, est, 10, 0) // very low threshold
		assert.True(t, c.ShouldConsolidate(100))
	})

	t.Run("disabled returns false", func(t *testing.T) {
		c := NewConsolidator(nil, est, 0, 0)
		assert.False(t, c.ShouldConsolidate(99999))
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
	est, err := token.NewEstimator()
	assert.NoError(t, err)
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
	est, err := token.NewEstimator()
	assert.NoError(t, err)
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

// ---------- Consolidate() 核心流程测试 ----------

func TestConsolidate_TooFewMessages(t *testing.T) {
	est, err := token.NewEstimator()
	require.NoError(t, err)
	c := NewConsolidator(&stubChat{response: "summary"}, est, 100, 0)

	msgs := []chat.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
	}
	result, err := c.Consolidate(context.Background(), msgs)
	require.NoError(t, err)
	assert.Equal(t, msgs, result, "<=3 messages should be returned unchanged")
}

func TestConsolidate_Round1_UserQueryAtEnd(t *testing.T) {
	est, err := token.NewEstimator()
	require.NoError(t, err)

	// Use a stub that returns a known summary.
	c := NewConsolidator(&stubChat{response: "summary of old history"}, est, 200, 0)

	long := strings.Repeat("old context data ", 200)
	msgs := []chat.Message{
		{Role: "system", Content: "You are a helpful assistant"},
		{Role: "user", Content: long},
		{Role: "assistant", Content: long},
		{Role: "user", Content: long},
		{Role: "assistant", Content: long},
		{Role: "user", Content: "current question"}, // last user = current query
	}

	result, err := c.Consolidate(context.Background(), msgs)
	require.NoError(t, err)

	// System prompt is preserved.
	assert.Equal(t, "system", result[0].Role)
	assert.Equal(t, "You are a helpful assistant", result[0].Content)

	// The current user query (last user message) must be preserved at the tail.
	last := result[len(result)-1]
	assert.Equal(t, "user", last.Role)
	assert.Equal(t, "current question", last.Content)

	// A summary message must have been inserted.
	assert.Equal(t, "system", result[1].Role)
	assert.Contains(t, result[1].Content, "Memory Summary")

	// Total message count should be fewer than original.
	assert.Less(t, len(result), len(msgs))
}

func TestConsolidate_Round2Plus_UserQueryNotAtEnd(t *testing.T) {
	est, err := token.NewEstimator()
	require.NoError(t, err)

	c := NewConsolidator(&stubChat{response: "consolidated history"}, est, 300, 0)

	longContent := strings.Repeat("verbose content ", 200)

	// Simulates Agent Round 2+:
	// [system, ...old_history..., user_query, assistant+tools, tool_results]
	// The user query is NOT the last message.
	msgs := []chat.Message{
		{Role: "system", Content: "You are a helpful assistant"},
		// --- old history (from previous turns) ---
		{Role: "user", Content: longContent},
		{Role: "assistant", Content: longContent},
		{Role: "user", Content: longContent},
		{Role: "assistant", Content: longContent, ToolCalls: []chat.ToolCall{
			{ID: "old_call", Function: chat.FunctionCall{Name: "search", Arguments: `{}`}},
		}},
		{Role: "tool", Content: longContent, ToolCallID: "old_call", Name: "search"},
		// --- current turn ---
		{Role: "user", Content: "what is the weather today?"}, // current user query
		{Role: "assistant", Content: "let me check", ToolCalls: []chat.ToolCall{
			{ID: "call_1", Function: chat.FunctionCall{Name: "weather", Arguments: `{"city":"beijing"}`}},
		}},
		{Role: "tool", Content: "sunny, 25°C", ToolCallID: "call_1", Name: "weather"},
	}

	result, err := c.Consolidate(context.Background(), msgs)
	require.NoError(t, err)

	// System prompt preserved.
	assert.Equal(t, "system", result[0].Role)

	// The entire current turn tail must be intact (user query + assistant + tool).
	// Find the user query in result.
	userQueryIdx := -1
	for i, m := range result {
		if m.Role == "user" && m.Content == "what is the weather today?" {
			userQueryIdx = i
			break
		}
	}
	require.NotEqual(t, -1, userQueryIdx, "current user query must be preserved")

	// After user query: assistant with tool_calls, then tool result.
	require.Greater(t, len(result), userQueryIdx+2)
	assert.Equal(t, "assistant", result[userQueryIdx+1].Role)
	assert.Equal(t, "let me check", result[userQueryIdx+1].Content)
	assert.Len(t, result[userQueryIdx+1].ToolCalls, 1)

	assert.Equal(t, "tool", result[userQueryIdx+2].Role)
	assert.Equal(t, "sunny, 25°C", result[userQueryIdx+2].Content)

	// Summary message exists.
	hasSummary := false
	for _, m := range result {
		if m.Role == "system" && strings.Contains(m.Content, "Memory Summary") {
			hasSummary = true
			break
		}
	}
	assert.True(t, hasSummary, "should contain a memory summary message")

	// Message count reduced.
	assert.Less(t, len(result), len(msgs))
}

func TestConsolidate_Round2Plus_MultipleToolCallsPreserved(t *testing.T) {
	est, err := token.NewEstimator()
	require.NoError(t, err)

	c := NewConsolidator(&stubChat{response: "summary"}, est, 300, 0)

	longContent := strings.Repeat("filler ", 300)
	msgs := []chat.Message{
		{Role: "system", Content: "sys"},
		// old history
		{Role: "user", Content: longContent},
		{Role: "assistant", Content: longContent},
		// current turn with parallel tool calls
		{Role: "user", Content: "do two things"},
		{Role: "assistant", Content: "ok", ToolCalls: []chat.ToolCall{
			{ID: "c1", Function: chat.FunctionCall{Name: "toolA", Arguments: `{}`}},
			{ID: "c2", Function: chat.FunctionCall{Name: "toolB", Arguments: `{}`}},
		}},
		{Role: "tool", Content: "resultA", ToolCallID: "c1", Name: "toolA"},
		{Role: "tool", Content: "resultB", ToolCallID: "c2", Name: "toolB"},
	}

	result, err := c.Consolidate(context.Background(), msgs)
	require.NoError(t, err)

	// All three current-turn messages after the user query must be present.
	var toolNames []string
	for _, m := range result {
		if m.Role == "tool" {
			toolNames = append(toolNames, m.Name)
		}
	}
	assert.Contains(t, toolNames, "toolA")
	assert.Contains(t, toolNames, "toolB")

	// User query preserved.
	found := false
	for _, m := range result {
		if m.Role == "user" && m.Content == "do two things" {
			found = true
		}
	}
	assert.True(t, found)
}

func TestConsolidate_NoUserMessage_ReturnUnchanged(t *testing.T) {
	est, err := token.NewEstimator()
	require.NoError(t, err)

	c := NewConsolidator(&stubChat{response: "summary"}, est, 100, 0)

	// Edge case: no user message at all (shouldn't happen normally but be defensive).
	msgs := []chat.Message{
		{Role: "system", Content: "sys"},
		{Role: "assistant", Content: "hello"},
		{Role: "assistant", Content: "world"},
		{Role: "assistant", Content: "more"},
	}

	result, err := c.Consolidate(context.Background(), msgs)
	require.NoError(t, err)
	assert.Equal(t, msgs, result, "no user message → return unchanged")
}

func TestConsolidate_LLMFailure_FallsBackToRawArchive(t *testing.T) {
	est, err := token.NewEstimator()
	require.NoError(t, err)

	failChat := &stubChat{err: assert.AnError}
	c := NewConsolidator(failChat, est, 200, 0)

	longContent := strings.Repeat("data ", 300)
	msgs := []chat.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: longContent},
		{Role: "assistant", Content: longContent},
		{Role: "user", Content: longContent},
		{Role: "assistant", Content: longContent},
		{Role: "user", Content: "current"},
		{Role: "assistant", Content: "thinking", ToolCalls: []chat.ToolCall{
			{ID: "c1", Function: chat.FunctionCall{Name: "t1"}},
		}},
		{Role: "tool", Content: "res", ToolCallID: "c1", Name: "t1"},
	}

	result, err := c.Consolidate(context.Background(), msgs)
	require.NoError(t, err)

	// Should still have a summary (raw archive fallback).
	hasSummary := false
	for _, m := range result {
		if m.Role == "system" && strings.Contains(m.Content, "Memory Summary") {
			hasSummary = true
			assert.Contains(t, m.Content, "Raw conversation archive",
				"should be a raw archive when LLM fails")
		}
	}
	assert.True(t, hasSummary)

	// Current turn tail must still be preserved.
	last := result[len(result)-1]
	assert.Equal(t, "tool", last.Role)

	userFound := false
	for _, m := range result {
		if m.Role == "user" && m.Content == "current" {
			userFound = true
		}
	}
	assert.True(t, userFound, "current user query must survive even on LLM failure")
}

func TestConsolidate_OnlyCurrentTurn_NothingToConsolidate(t *testing.T) {
	est, err := token.NewEstimator()
	require.NoError(t, err)
	c := NewConsolidator(&stubChat{response: "summary"}, est, 100, 0)

	// Only system + user + assistant + tool → history between system and user is empty.
	msgs := []chat.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "let me help", ToolCalls: []chat.ToolCall{
			{ID: "c1", Function: chat.FunctionCall{Name: "t1"}},
		}},
		{Role: "tool", Content: "done", ToolCallID: "c1", Name: "t1"},
	}

	result, err := c.Consolidate(context.Background(), msgs)
	require.NoError(t, err)
	assert.Equal(t, msgs, result, "nothing to consolidate → unchanged")
}

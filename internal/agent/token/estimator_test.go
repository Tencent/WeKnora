package token

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/invoke"
	"github.com/stretchr/testify/assert"
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
		msg := invoke.Message{
			Role: "assistant",
		}
		tokens := e.EstimateMessage(&msg)
		contentTokens := e.EstimateString("hello")
		fmt.Println(tokens, contentTokens)
		assert.Greater(t, tokens, contentTokens,
			"message tokens should include overhead beyond just content")
	})

	t.Run("message with tool calls", func(t *testing.T) {
		msg := invoke.Message{
			Role: "assistant",

			ToolCalls: []invoke.ToolCall{
				{
					Function: invoke.FunctionCall{
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
		messages := []invoke.Message{
			{Role: "system", Content: []invoke.Part{{Text: "You are a helpful assistant."}}},
			{Role: "user", Content: []invoke.Part{{Text: "Hello"}}},
			{Role: "assistant", Content: []invoke.Part{{Text: "Hi there!"}}},
		}
		tokens := e.EstimateMessages(messages)
		assert.Greater(t, tokens, 10)
	})
}

// Thinking models (DeepSeek V3.2/V4, MiMo) require prior reasoning_content to
// be sent back verbatim, and it typically dwarfs the visible reply. Leaving it
// out of the estimate made a 130k context measure as 26k, so compaction cut in
// the wrong place, freed almost nothing, and ran again every round.
func TestEstimateMessageCountsReasoningContent(t *testing.T) {
	e, err := NewEstimator()
	assert.NoError(t, err)

	reasoning := strings.Repeat("let me think about this step by step. ", 200)
	plain := invoke.TextMessage(invoke.RoleAssistant, "short answer")
	thinking := invoke.Message{
		Role: "assistant", Content: []invoke.Part{{Text: "short answer"}}, ReasoningContent: reasoning,
	}

	assert.Greater(t, e.EstimateMessage(&thinking), e.EstimateMessage(&plain)+1000,
		"reasoning content must be counted, it is sent on the wire")
	assert.InDelta(t, e.EstimateString(reasoning),
		e.EstimateMessage(&thinking)-e.EstimateMessage(&plain), 2)
}

// Providers bill images by tile count, so neither a short https:// URL nor a
// megabyte data URI says anything useful about the cost.
func TestEstimateMessageCountsImages(t *testing.T) {
	e, err := NewEstimator()
	assert.NoError(t, err)

	withImages := invoke.Message{Role: invoke.RoleUser, Content: []invoke.Part{
		{Text: "what is this"}, {Image: &invoke.ImageRef{URL: "https://x/a.png"}},
	}}
	assert.Greater(t, e.EstimateMessage(&withImages), estimatedImageTokens)

	multi := invoke.Message{Role: invoke.RoleUser, Content: []invoke.Part{
		{Text: "describe"},
		{Image: &invoke.ImageRef{URL: "data:image/png;base64,AAAA"}},
	}}
	assert.Greater(t, e.EstimateMessage(&multi), estimatedImageTokens)

	// Counting the same picture as both an image part and text twice would
	// double-bill it; each part is counted exactly once.
	both := invoke.Message{Role: invoke.RoleUser, Content: []invoke.Part{
		{Text: "describe"},
		{Image: &invoke.ImageRef{URL: "data:image/png;base64,AAAA"}},
	}}
	assert.Equal(t, e.EstimateMessage(&multi), e.EstimateMessage(&both))
}

// Tool schemas are part of the billed prompt but never appear in the message
// list, so omitting them biases every estimate low by a constant.
func TestEstimateTools(t *testing.T) {
	e, err := NewEstimator()
	assert.NoError(t, err)

	assert.Zero(t, e.EstimateTools(nil))
	tools := []invoke.ToolDef{{
		Name:        "shell_exec",
		Description: strings.Repeat("run a shell command in the sandbox. ", 20),
		Parameters:  []byte(`{"type":"object","properties":{"command":{"type":"string"}}}`),
	}}
	assert.Greater(t, e.EstimateTools(tools), 100)
}

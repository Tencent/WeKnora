package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type postprocessCacheTestChat struct {
	modelID string
}

func (m postprocessCacheTestChat) Chat(
	context.Context,
	[]chat.Message,
	*chat.ChatOptions,
) (*types.ChatResponse, error) {
	return &types.ChatResponse{Content: "ok"}, nil
}

func (m postprocessCacheTestChat) ChatStream(
	context.Context,
	[]chat.Message,
	*chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	return nil, nil
}

func (m postprocessCacheTestChat) GetModelName() string { return "test-model" }
func (m postprocessCacheTestChat) GetModelID() string   { return m.modelID }

func TestPostprocessLLMCacheKeyIncludesLayerModelPromptAndOptions(t *testing.T) {
	thinking := false
	messages := []chat.Message{{Role: "user", Content: "summarize this"}}
	opts := &chat.ChatOptions{Temperature: 0.3, MaxTokens: 512, Thinking: &thinking}
	model := postprocessCacheTestChat{modelID: "chat-a"}

	base := postprocessLLMCacheKey("summary", model, messages, opts, "summary-v1")
	require.NotEmpty(t, base)

	require.NotEqual(t, base, postprocessLLMCacheKey("question", model, messages, opts, "summary-v1"))
	require.NotEqual(t, base, postprocessLLMCacheKey("summary", postprocessCacheTestChat{modelID: "chat-b"}, messages, opts, "summary-v1"))
	require.NotEqual(t, base, postprocessLLMCacheKey("summary", model, []chat.Message{{Role: "user", Content: "different"}}, opts, "summary-v1"))
	require.NotEqual(t, base, postprocessLLMCacheKey("summary", model, messages, &chat.ChatOptions{Temperature: 0.7, MaxTokens: 512, Thinking: &thinking}, "summary-v1"))
	require.NotEqual(t, base, postprocessLLMCacheKey("summary", model, messages, opts, "summary-v2"))
}

func TestParseGeneratedQuestionsMatchesExistingFiltering(t *testing.T) {
	got := parseGeneratedQuestions(`
1. What is content addressing?
2) Why does stable identity matter?
- no
* How does cache invalidation work?
3. ok
4. Which layers remain stateful?
`, 3)

	require.Equal(t, []string{
		"What is content addressing?",
		"Why does stable identity matter?",
		"How does cache invalidation work?",
	}, got)
}

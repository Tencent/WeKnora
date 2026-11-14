package agent

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// Regression for the overflow-recovery path in callLLMWithRetry: the retry
// after a rejected-for-size request must compact the engine's unsanitized
// history. Sanitizing merges adjacent user messages and the merge keeps the
// first message's Kind, so a compaction-summary message merged with the live
// user turn reads as a summary whose content extends past </summary>. Every
// later compaction unwraps only the text between <summary> and </summary> and
// treats that merged message as the boundary, so the live turn folded into it
// becomes permanently invisible to summarization.
func TestOverflowRecoveryCompactsUnsanitizedHistory(t *testing.T) {
	messages := []chat.Message{
		{Role: "system", Content: "You are a test agent."},
		{
			Role:    "user",
			Kind:    chat.MessageKindCompactionSummary,
			Content: "Earlier context.\n<summary>\nUser is analyzing Q3 sales.\n</summary>",
		},
		{Role: "user", Content: "Now compare with Q4"},
	}
	model := &mockChat{responses: []mockResponse{
		{chunks: []types.StreamResponse{
			{
				ResponseType: types.ResponseTypeError,
				Content:      "prompt is too long: 200000 tokens > 131072 maximum",
				Done:         true,
			},
		}},
		{chunks: []types.StreamResponse{
			{ResponseType: types.ResponseTypeAnswer, Content: "ok", Done: true, FinishReason: "stop"},
		}},
	}}
	engine := newTestEngine(t, model)
	_, err := engine.callLLMWithRetry(
		context.Background(), &messages, nil, &types.AgentState{}, "q", 0, "test-session",
	)
	require.NoError(t, err)

	// The engine's copy keeps the structured history the caller stored.
	require.Len(t, messages, 3,
		"overflow recovery must not collapse the engine's history into the sanitized view")
	require.Equal(t, chat.MessageKindCompactionSummary, messages[1].Kind)
	require.Equal(t, "Now compare with Q4", messages[2].Content)
	require.NotContains(t, messages[1].Content, "Now compare with Q4")

	// The provider still sees the sanitized view of the recovered history.
	require.Len(t, model.calls, 2)
	require.Len(t, model.calls[1], 2,
		"the retried request still merges the adjacent user messages for the provider")
	require.Contains(t, model.calls[1][1].Content, "Now compare with Q4",
		"the live turn must reach the provider on the retried request")
}

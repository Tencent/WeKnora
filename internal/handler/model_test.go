package handler

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelUpdateRequestDisplayNamePresence(t *testing.T) {
	var omitted UpdateModelRequest
	require.NoError(t, json.Unmarshal([]byte(`{"name":"gpt-4o"}`), &omitted))
	assert.Nil(t, omitted.DisplayName)

	var cleared UpdateModelRequest
	require.NoError(t, json.Unmarshal([]byte(`{"display_name":""}`), &cleared))
	require.NotNil(t, cleared.DisplayName)
	assert.Equal(t, "", *cleared.DisplayName)
}

func TestParseModelDebugOptionsPreservesExplicitThinkingFalse(t *testing.T) {
	opts, err := parseModelDebugOptions(`{"thinking":false,"temperature":0,"max_tokens":256}`)
	require.NoError(t, err)
	require.NotNil(t, opts.Thinking)
	assert.False(t, *opts.Thinking)
	require.NotNil(t, opts.Temperature)
	assert.Zero(t, *opts.Temperature)
	require.NotNil(t, opts.MaxTokens)
	assert.Equal(t, 256, *opts.MaxTokens)
}

func TestParseModelDebugOptionsRejectsOutOfRangeValues(t *testing.T) {
	_, err := parseModelDebugOptions(`{"top_p":0}`)
	require.ErrorContains(t, err, "top_p")
}

func TestRedactedDebugConfig(t *testing.T) {
	got := redactedDebugConfig(map[string]string{
		"thinking_control": "enable_thinking",
		"secret_key":       "do-not-leak",
		"access_token":     "do-not-leak-either",
	})
	assert.Equal(t, "enable_thinking", got["thinking_control"])
	assert.Equal(t, "[REDACTED]", got["secret_key"])
	assert.Equal(t, "[REDACTED]", got["access_token"])
}

func TestConsumeModelDebugChatStream(t *testing.T) {
	stream := make(chan types.StreamResponse, 5)
	stream <- types.StreamResponse{ResponseType: types.ResponseTypeThinking, Content: "reason "}
	stream <- types.StreamResponse{ResponseType: types.ResponseTypeThinking, Content: "more", Done: true}
	stream <- types.StreamResponse{ResponseType: types.ResponseTypeAnswer, Content: "answer "}
	stream <- types.StreamResponse{ResponseType: types.ResponseTypeAnswer, Content: "done"}
	stream <- types.StreamResponse{
		ResponseType: types.ResponseTypeAnswer,
		Done:         true,
		FinishReason: "stop",
		Usage:        &types.TokenUsage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7},
	}
	close(stream)

	got, err := consumeModelDebugChatStream(stream)
	require.NoError(t, err)
	assert.Equal(t, "reason more", got.ReasoningContent)
	assert.Equal(t, "answer done", got.Content)
	assert.Equal(t, "stop", got.FinishReason)
	require.NotNil(t, got.Usage)
	assert.Equal(t, 7, got.Usage.TotalTokens)
	assert.Len(t, got.StreamEvents, 5)
}

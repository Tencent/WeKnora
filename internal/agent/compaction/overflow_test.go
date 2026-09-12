package compaction

import (
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
)

// Real error strings from the providers WeKnora talks to. An overflow read as
// a generic failure ends the turn; read as transient it retries the identical
// oversized request until the attempts run out.
func TestIsOverflowError(t *testing.T) {
	overflow := []string{
		"prompt is too long: 213462 tokens > 200000 maximum",
		`413 {"error":{"type":"request_too_large","message":"Request exceeds the maximum size"}}`,
		"Your input exceeds the context window of this model",
		"Requested token count exceeds the model's maximum context length of 131072 tokens",
		"Input length (265330) exceeds model's maximum context length (262144).",
		"The input token count (1196265) exceeds the maximum number of tokens allowed (1048575)",
		"This model's maximum prompt length is 131072 but the request contains 537812 tokens",
		"Please reduce the length of the messages or completion",
		"This endpoint's maximum context length is 131072 tokens",
		"The input (300 tokens) is longer than the model's context length (200 tokens).",
		"the request exceeds the available context size, try increasing it",
		"prompt token count of 5000 exceeds the limit of 4096",
		"invalid params, context window exceeds limit",
		"Your request exceeded model token limit: 128000",
		"Prompt has 5000 tokens, but the configured context size is 4096 tokens",
		"Range of input length should be [1, 30000]",
		"context_length_exceeded",
	}
	for _, msg := range overflow {
		assert.True(t, IsOverflowError(errors.New(msg)), "should detect overflow: %s", msg)
	}

	// Throttling formats itself as a token complaint and is not an overflow.
	notOverflow := []string{
		"ThrottlingException: Too many tokens, please wait before trying again.",
		"429 rate limit exceeded",
		"Too many requests",
		"connection reset by peer",
		"context deadline exceeded",
	}
	for _, msg := range notOverflow {
		assert.False(t, IsOverflowError(errors.New(msg)), "should not detect overflow: %s", msg)
	}

	assert.False(t, IsOverflowError(nil))
}

func TestResponseHitContextLimit(t *testing.T) {
	const window, budget = 100000, 8000

	// The general recoverable case: stopped on length below our own cap, so
	// our cap is not what stopped it.
	assert.True(t, ResponseHitContextLimit(&types.ChatResponse{
		FinishReason: "length",
		Usage:        types.TokenUsage{PromptTokens: 90000, CompletionTokens: 2000},
	}, window, budget))

	// Used the full budget: the model simply had more to say, and retrying
	// after a compaction would produce the same truncation.
	assert.False(t, ResponseHitContextLimit(&types.ChatResponse{
		FinishReason: "length",
		Usage:        types.TokenUsage{PromptTokens: 50000, CompletionTokens: budget},
	}, window, budget))

	// Silent overflow: the provider accepted a prompt larger than the window
	// instead of rejecting it (z.ai).
	assert.True(t, ResponseHitContextLimit(&types.ChatResponse{
		FinishReason: "stop",
		Usage:        types.TokenUsage{PromptTokens: window + 1, CompletionTokens: 10},
	}, window, budget))

	// Server truncated the input to fill the window exactly, leaving no room
	// to generate (Xiaomi MiMo).
	assert.True(t, ResponseHitContextLimit(&types.ChatResponse{
		FinishReason: "length",
		Usage:        types.TokenUsage{PromptTokens: window, CompletionTokens: 0},
	}, window, budget))

	// An ordinary completed response is not an overflow.
	assert.False(t, ResponseHitContextLimit(&types.ChatResponse{
		FinishReason: "stop",
		Usage:        types.TokenUsage{PromptTokens: 1000, CompletionTokens: 100},
	}, window, budget))

	// No usage means nothing to compare against.
	assert.False(t, ResponseHitContextLimit(&types.ChatResponse{FinishReason: "length"}, window, budget))
	assert.False(t, ResponseHitContextLimit(nil, window, budget))
}

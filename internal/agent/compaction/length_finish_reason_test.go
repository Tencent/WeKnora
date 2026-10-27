package compaction

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
)

// z.ai ends a generation with `model_context_window_exceeded` when the input
// exhausts the window. #4589 normalized that finish reason to a length stop in
// the agent engine and the Anthropic adapter; the compaction path must agree,
// or a summary truncated mid-section is accepted as a checkpoint and every
// later round inherits the truncation as its only memory.
func TestValidateSummaryRejectsContextWindowExceededFinish(t *testing.T) {
	assert.Error(t, validateSummary("## Goal\nhalf a summary that stops mid",
		"model_context_window_exceeded"))
	// Case-insensitive: finish reasons arrive verbatim from providers.
	assert.Error(t, validateSummary("## Goal\nhalf a summary that stops mid",
		"Model_Context_Window_Exceeded"))
	// A natural stop with the same content stays valid.
	assert.NoError(t, validateSummary("## Goal\ncomplete summary", "stop"))
}

func TestIsLengthStopRecognizesContextWindowExceeded(t *testing.T) {
	assert.True(t, isLengthStop("model_context_window_exceeded"))
	assert.True(t, isLengthStop(" Model_Context_Window_Exceeded\n"))
	// The pre-existing length reasons keep working; ordinary stops do not
	// become length stops.
	assert.True(t, isLengthStop("length"))
	assert.False(t, isLengthStop("stop"))
}

// The general recoverable shape: the provider stopped on the context window
// having emitted less than the budget we asked for, so our own cap cannot be
// what stopped it. This is what lets the engine compact and retry once.
func TestResponseHitContextLimitContextWindowExceededFinish(t *testing.T) {
	const window, budget = 100000, 8000

	assert.True(t, ResponseHitContextLimit(&types.ChatResponse{
		FinishReason: "model_context_window_exceeded",
		Usage:        types.TokenUsage{PromptTokens: 90000, CompletionTokens: 2000},
	}, window, budget))

	// Truncated to no output with a prompt that fills the window (shape 2).
	assert.True(t, ResponseHitContextLimit(&types.ChatResponse{
		FinishReason: "model_context_window_exceeded",
		Usage:        types.TokenUsage{PromptTokens: window, CompletionTokens: 0},
	}, window, budget))

	// An ordinary stop with room left in the budget is not a context hit.
	assert.False(t, ResponseHitContextLimit(&types.ChatResponse{
		FinishReason: "stop",
		Usage:        types.TokenUsage{PromptTokens: 90000, CompletionTokens: 2000},
	}, window, budget))
}

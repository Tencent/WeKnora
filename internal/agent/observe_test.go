package agent

import (
	"context"
	"testing"
	"time"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newFinalAnswerResponse builds a ChatResponse that carries a single
// final_answer tool call with the given raw JSON arguments.
func newFinalAnswerResponse(rawArgs string) *types.ChatResponse {
	return &types.ChatResponse{
		FinishReason: "tool_calls",
		ToolCalls: []types.LLMToolCall{
			{
				ID:   "call-1",
				Type: "function",
				Function: types.FunctionCall{
					Name:      agenttools.ToolFinalAnswer,
					Arguments: rawArgs,
				},
			},
		},
	}
}

// TestAnalyzeResponse_FinalAnswer_ValidArgs guards the happy path: well-formed
// arguments must be extracted into the final answer and terminate the loop.
func TestAnalyzeResponse_FinalAnswer_ValidArgs(t *testing.T) {
	engine := newTestEngine(t, &mockChat{})
	resp := newFinalAnswerResponse(`{"answer": "Here is the answer."}`)

	verdict := engine.analyzeResponse(
		context.Background(), resp, types.AgentStep{}, 0, "sess-1", time.Now(),
	)

	assert.True(t, verdict.isDone, "final_answer must terminate the loop")
	assert.Equal(t, "Here is the answer.", verdict.finalAnswer)
}

// TestAnalyzeResponse_FinalAnswer_MalformedJSON_RecoveredViaRepair covers the
// common case reported in issue #1008: the LLM emits final_answer with a
// trailing comma / missing brace. RepairJSON should recover the answer and
// the loop must still terminate in this single round (not re-invoke
// final_answer in the next round).
func TestAnalyzeResponse_FinalAnswer_MalformedJSON_RecoveredViaRepair(t *testing.T) {
	engine := newTestEngine(t, &mockChat{})
	resp := newFinalAnswerResponse(`{"answer": "repaired"`) // missing closing brace

	verdict := engine.analyzeResponse(
		context.Background(), resp, types.AgentStep{}, 0, "sess-1", time.Now(),
	)

	assert.True(t, verdict.isDone,
		"final_answer must terminate the loop even when JSON repair is needed")
	assert.Equal(t, "repaired", verdict.finalAnswer)
}

// TestAnalyzeResponse_FinalAnswer_UnrecoverableArgs_StillTerminates is the
// direct regression test for issue #1008: when the arguments are so malformed
// that even RepairJSON + regex cannot recover an answer, the loop MUST still
// terminate (with a user-visible fallback message) rather than continuing and
// letting the LLM re-emit final_answer on the next round.
func TestAnalyzeResponse_FinalAnswer_UnrecoverableArgs_StillTerminates(t *testing.T) {
	engine := newTestEngine(t, &mockChat{})
	// No `answer` key at all — strict parse succeeds (returns zero-value
	// answer), RepairJSON is a no-op on already-valid JSON, regex finds
	// nothing. All three tiers fail to recover an answer.
	resp := newFinalAnswerResponse(`{"unexpected": "field"}`)

	verdict := engine.analyzeResponse(
		context.Background(), resp, types.AgentStep{}, 0, "sess-1", time.Now(),
	)

	assert.True(t, verdict.isDone,
		"final_answer must terminate the loop even when args are unrecoverable — "+
			"otherwise the LLM re-emits final_answer and duplicates the answer (issue #1008)")
	assert.Equal(t, finalAnswerParseFallback, verdict.finalAnswer,
		"unrecoverable final_answer should surface the parse-failure fallback message")
}

// TestAnalyzeResponse_FinalAnswer_Garbage_StillTerminates exercises the most
// hostile case: completely non-JSON arguments. The loop must still terminate
// — protecting against the duplicate-answer loop reported in issue #1008.
func TestAnalyzeResponse_FinalAnswer_Garbage_StillTerminates(t *testing.T) {
	engine := newTestEngine(t, &mockChat{})
	resp := newFinalAnswerResponse(`not json at all`)

	verdict := engine.analyzeResponse(
		context.Background(), resp, types.AgentStep{}, 0, "sess-1", time.Now(),
	)

	assert.True(t, verdict.isDone)
	assert.Equal(t, finalAnswerParseFallback, verdict.finalAnswer)
}

// TestAnalyzeResponse_NonFinalAnswerTool_DoesNotTerminate is a regression
// guard: only final_answer is terminal. Other tool calls (e.g. thinking,
// knowledge_search) must keep the loop running.
func TestAnalyzeResponse_NonFinalAnswerTool_DoesNotTerminate(t *testing.T) {
	engine := newTestEngine(t, &mockChat{})
	resp := &types.ChatResponse{
		FinishReason: "tool_calls",
		ToolCalls: []types.LLMToolCall{
			{
				ID:   "call-1",
				Type: "function",
				Function: types.FunctionCall{
					Name:      agenttools.ToolKnowledgeSearch,
					Arguments: `{"query": "hi"}`,
				},
			},
		},
	}

	verdict := engine.analyzeResponse(
		context.Background(), resp, types.AgentStep{}, 0, "sess-1", time.Now(),
	)

	assert.False(t, verdict.isDone,
		"non-terminal tool calls must keep the loop running")
}

// TestAppendToolResults_PreservesReasoningContent verifies that the assistant
// message produced by appendToolResults carries the reasoning_content emitted
// by the model in the same round. Without this, MiMo and DeepSeek V3.2+
// thinking-mode reject the next ReAct round with HTTP 400
// "The reasoning_content in the thinking mode must be passed back to the API."
// (issue #1302).
func TestAppendToolResults_PreservesReasoningContent(t *testing.T) {
	engine := &AgentEngine{}

	t.Run("assistant message carries reasoning_content alongside thought and tool_calls", func(t *testing.T) {
		step := types.AgentStep{
			Iteration:        0,
			Thought:          "I will call search.",
			ReasoningContent: "Detailed chain of thought from MiMo/DeepSeek.",
			ToolCalls: []types.ToolCall{{
				ID:   "call_1",
				Name: "knowledge_search",
				Args: map[string]interface{}{"query": "hi"},
				Result: &types.ToolResult{
					Success: true,
					Output:  "result text",
				},
			}},
			Timestamp: time.Now(),
		}

		out := engine.appendToolResults(nil, step)

		require.Len(t, out, 2, "expect one assistant + one tool message")
		assert.Equal(t, "assistant", out[0].Role)
		assert.Equal(t, "I will call search.", out[0].Content)
		assert.Equal(t, "Detailed chain of thought from MiMo/DeepSeek.", out[0].ReasoningContent,
			"reasoning_content must be propagated to the assistant message so providers like MiMo "+
				"and DeepSeek thinking-mode see it on the next round (issue #1302)")
		require.Len(t, out[0].ToolCalls, 1)
		assert.Equal(t, "call_1", out[0].ToolCalls[0].ID)

		assert.Equal(t, "tool", out[1].Role)
		assert.Equal(t, "result text", out[1].Content)
	})

	t.Run("reasoning_content alone produces an assistant message", func(t *testing.T) {
		// A pure thinking emission with no visible content / tool calls is
		// unusual but legal — preserve it so the next round's request still
		// carries reasoning_content for strict providers.
		step := types.AgentStep{
			Iteration:        0,
			ReasoningContent: "reasoning only",
			Timestamp:        time.Now(),
		}

		out := engine.appendToolResults(nil, step)

		require.Len(t, out, 1)
		assert.Equal(t, "assistant", out[0].Role)
		assert.Equal(t, "reasoning only", out[0].ReasoningContent)
		assert.Empty(t, out[0].Content)
		assert.Empty(t, out[0].ToolCalls)
	})

	t.Run("step without thought/tool_calls/reasoning produces no assistant message", func(t *testing.T) {
		step := types.AgentStep{Iteration: 0, Timestamp: time.Now()}
		out := engine.appendToolResults(nil, step)
		assert.Empty(t, out, "empty steps must not inject empty assistant messages")
	})

	t.Run("appends to existing message slice", func(t *testing.T) {
		prior := []chat.Message{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "hi"},
		}
		step := types.AgentStep{
			Iteration:        1,
			Thought:          "answer",
			ReasoningContent: "thinking",
			Timestamp:        time.Now(),
		}
		out := engine.appendToolResults(prior, step)
		require.Len(t, out, 3)
		assert.Equal(t, "system", out[0].Role)
		assert.Equal(t, "user", out[1].Role)
		assert.Equal(t, "assistant", out[2].Role)
		assert.Equal(t, "thinking", out[2].ReasoningContent)
	})
}

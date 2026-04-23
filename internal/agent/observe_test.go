package agent

import (
	"context"
	"testing"
	"time"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
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

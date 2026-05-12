package service

import (
	"testing"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
)

// TestBuildUserHistoryMessage_PrefersRenderedContent verifies the user side of
// history rebuild: RenderedContent (the RAG-augmented version) wins over the
// raw Content, and image captions are appended only when there's no rendered
// override (the rendered version already carries any retrieval context that
// would have included the caption upstream).
func TestBuildUserHistoryMessage_PrefersRenderedContent(t *testing.T) {
	msg := &types.Message{
		Role:            "user",
		Content:         "what about the chart?",
		RenderedContent: "what about the chart? [augmented]",
		Images: types.MessageImages{
			{Caption: "a bar chart"},
		},
	}
	got := buildUserHistoryMessage(msg)
	assert.Equal(t, "user", got.Role)
	assert.Equal(t, "what about the chart? [augmented]", got.Content)
}

func TestBuildUserHistoryMessage_FallsBackToContentWithCaptions(t *testing.T) {
	msg := &types.Message{
		Role:    "user",
		Content: "look at this",
		Images: types.MessageImages{
			{Caption: "a bar chart"},
			{Caption: "a pie chart"},
		},
	}
	got := buildUserHistoryMessage(msg)
	assert.Equal(t, "user", got.Role)
	assert.Equal(t, "look at this\n\n[用户上传图片内容]\na bar chart\na pie chart", got.Content)
}

// TestBuildAssistantHistoryMessages_NaturalFinishEmitsSingleAnswer covers the
// most common path: a turn with no tool calls (model answered directly). The
// result must be a single assistant message holding the canonical answer —
// duplicates would inflate token usage every turn.
func TestBuildAssistantHistoryMessages_NaturalFinishEmitsSingleAnswer(t *testing.T) {
	msg := &types.Message{
		Role:    "assistant",
		Content: "Hello, nice to meet you!",
		AgentSteps: types.AgentSteps{
			{Iteration: 0, Thought: "Hello, nice to meet you!", ToolCalls: nil},
		},
	}
	got := buildAssistantHistoryMessages(msg)
	if assert.Len(t, got, 1) {
		assert.Equal(t, "assistant", got[0].Role)
		assert.Equal(t, "Hello, nice to meet you!", got[0].Content)
		assert.Empty(t, got[0].ToolCalls)
	}
}

// TestBuildAssistantHistoryMessages_StripsThinkBlocks ensures the trailing
// final-answer assistant message has any <think>…</think> blocks stripped, so
// internal-reasoning text doesn't leak into the next turn's context.
func TestBuildAssistantHistoryMessages_StripsThinkBlocks(t *testing.T) {
	msg := &types.Message{
		Role:    "assistant",
		Content: "<think>plotting...</think>The answer is 42.",
	}
	got := buildAssistantHistoryMessages(msg)
	if assert.Len(t, got, 1) {
		assert.Equal(t, "The answer is 42.", got[0].Content)
	}
}

// TestBuildAssistantHistoryMessages_ToolCallsExpandIntoOpenAIShape covers the
// option-B replay: non-terminal tool calls from AgentSteps become proper
// assistant_with_tool_calls + tool messages, and the canonical final answer is
// appended last. final_answer entries are filtered because they're terminal
// signals — the trailing assistant message already carries the answer.
func TestBuildAssistantHistoryMessages_ToolCallsExpandIntoOpenAIShape(t *testing.T) {
	msg := &types.Message{
		Role:    "assistant",
		Content: "Found 3 matches in the docs.",
		AgentSteps: types.AgentSteps{
			{
				Iteration: 0,
				Thought:   "Let me search.",
				ToolCalls: []types.ToolCall{
					{
						ID:   "call_1",
						Name: agenttools.ToolKnowledgeSearch,
						Args: map[string]interface{}{"query": "foo"},
						Result: &types.ToolResult{
							Success: true,
							Output:  "doc A, doc B, doc C",
						},
					},
				},
			},
			{
				Iteration: 1,
				Thought:   "",
				ToolCalls: []types.ToolCall{
					{
						ID:     "call_2",
						Name:   agenttools.ToolFinalAnswer,
						Args:   map[string]interface{}{"answer": "Found 3 matches in the docs."},
						Result: &types.ToolResult{Success: true},
					},
				},
			},
		},
	}
	got := buildAssistantHistoryMessages(msg)
	if !assert.Len(t, got, 3) {
		return
	}
	// 1. assistant message announcing the tool call
	assert.Equal(t, "assistant", got[0].Role)
	assert.Equal(t, "Let me search.", got[0].Content)
	if assert.Len(t, got[0].ToolCalls, 1) {
		assert.Equal(t, "call_1", got[0].ToolCalls[0].ID)
		assert.Equal(t, agenttools.ToolKnowledgeSearch, got[0].ToolCalls[0].Function.Name)
		assert.Contains(t, got[0].ToolCalls[0].Function.Arguments, "foo")
	}
	// 2. tool result paired with the call ID
	assert.Equal(t, "tool", got[1].Role)
	assert.Equal(t, "call_1", got[1].ToolCallID)
	assert.Equal(t, "doc A, doc B, doc C", got[1].Content)
	// 3. canonical final answer (final_answer tool call itself was filtered)
	assert.Equal(t, "assistant", got[2].Role)
	assert.Equal(t, "Found 3 matches in the docs.", got[2].Content)
	assert.Empty(t, got[2].ToolCalls)
}

// TestBuildAssistantHistoryMessages_ToolFailureSurfacesAsError ensures a
// historical failed tool call is replayed as an "Error: …" tool message so the
// model can see (and avoid retrying) the same failure path.
func TestBuildAssistantHistoryMessages_ToolFailureSurfacesAsError(t *testing.T) {
	msg := &types.Message{
		Role:    "assistant",
		Content: "Sorry, I could not complete the search.",
		AgentSteps: types.AgentSteps{
			{
				Iteration: 0,
				Thought:   "Trying search.",
				ToolCalls: []types.ToolCall{
					{
						ID:   "call_err",
						Name: agenttools.ToolKnowledgeSearch,
						Args: map[string]interface{}{"query": "x"},
						Result: &types.ToolResult{
							Success: false,
							Error:   "kb unreachable",
						},
					},
				},
			},
		},
	}
	got := buildAssistantHistoryMessages(msg)
	if !assert.Len(t, got, 3) {
		return
	}
	assert.Equal(t, chat.Message{
		Role:       "tool",
		Content:    "Error: kb unreachable",
		ToolCallID: "call_err",
		Name:       agenttools.ToolKnowledgeSearch,
	}, got[1])
}

// TestFilterNonTerminalToolCalls confirms only final_answer is dropped — every
// other tool (KB search, web search, MCP tools…) must survive the filter.
func TestFilterNonTerminalToolCalls(t *testing.T) {
	in := []types.ToolCall{
		{Name: agenttools.ToolKnowledgeSearch},
		{Name: agenttools.ToolFinalAnswer},
		{Name: agenttools.ToolWebSearch},
	}
	out := filterNonTerminalToolCalls(in)
	if assert.Len(t, out, 2) {
		assert.Equal(t, agenttools.ToolKnowledgeSearch, out[0].Name)
		assert.Equal(t, agenttools.ToolWebSearch, out[1].Name)
	}
}

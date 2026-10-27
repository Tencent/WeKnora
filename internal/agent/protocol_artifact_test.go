package agent

import (
	"context"
	"strings"
	"testing"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func artifactResponse() mockResponse {
	// Same response shape as the captured incident: content deltas, no tool
	// calls, then finish_reason=stop. Use synthetic opaque bytes, not user data.
	return mockResponse{chunks: []types.StreamResponse{
		{ResponseType: types.ResponseTypeAnswer, Content: "open"},
		{ResponseType: types.ResponseTypeAnswer, Content: "ai＿reasoning: "},
		{ResponseType: types.ResponseTypeAnswer, Content: strings.Repeat("A", 128) + "="},
		{ResponseType: types.ResponseTypeAnswer, Content: "</arg_value:6124c78e>"},
		{ResponseType: types.ResponseTypeAnswer, Done: true, FinishReason: "stop"},
	}}
}

func TestProtocolArtifactRecoveryDoesNotReplayToolsOrPublishGarbage(t *testing.T) {
	for _, repeat := range []bool{false, true} {
		t.Run(map[bool]string{false: "recover", true: "bounded_failure"}[repeat], func(t *testing.T) {
			last := mockResponse{
				chunks: []types.StreamResponse{
					{ResponseType: types.ResponseTypeAnswer, Content: "完整报告", Done: true, FinishReason: "stop"},
				},
			}
			if repeat {
				last = artifactResponse()
			}
			model := &mockChat{responses: []mockResponse{
				{
					chunks: []types.StreamResponse{
						{
							ResponseType: types.ResponseTypeAnswer,
							Done:         true,
							FinishReason: "tool_calls",
							ToolCalls: []types.LLMToolCall{
								{
									ID:       "once",
									Type:     "function",
									Function: types.FunctionCall{Name: "read_sources", Arguments: `{}`},
								},
							},
						},
					},
				},
				artifactResponse(), last,
			}}
			e := newTestEngine(t, model)
			e.toolRegistry = agenttools.NewToolRegistry()
			source := newCountingTool("read_sources")
			e.toolRegistry.RegisterTool(source)
			recorder := &truncationRecorder{}
			recorder.attach(e.eventBus)
			state, err := e.Execute(context.Background(), "s", "m", "report", nil, nil)
			require.Equal(t, 1, source.calls)
			require.Len(t, model.calls, 3)
			if repeat {
				require.ErrorIs(t, err, agenttools.ErrProtocolArtifact)
				require.Nil(t, state)
			} else {
				require.NoError(t, err)
				require.Equal(t, "完整报告", state.FinalAnswer)
				require.True(t, state.IsComplete)
			}
			for _, ev := range recorder.snapshot() {
				require.NotContains(t, ev.Content, "reasoning")
				require.NotContains(t, ev.Content, "arg_value")
			}
			var history strings.Builder
			for _, m := range model.calls[2] {
				history.WriteString(m.Content)
			}
			require.NotContains(t, history.String(), "</arg_value:")
			require.NotContains(t, history.String(), "existing tool and delegated results")
			require.Contains(t, model.opts[2].RuntimeNotice, "existing tool and delegated results")
		})
	}
}

func TestFinalSynthesisRejectsProtocolArtifact(t *testing.T) {
	e := newTestEngine(t, &mockChat{responses: []mockResponse{artifactResponse()}})
	recorder := &truncationRecorder{}
	recorder.attach(e.eventBus)
	state := &types.AgentState{}
	err := e.streamFinalAnswerToEventBus(
		context.Background(),
		"report",
		state,
		"s",
		[]chat.Message{{Role: "user", Content: "report"}},
	)
	require.ErrorIs(t, err, agenttools.ErrProtocolArtifact)
	require.Empty(t, state.FinalAnswer)
	for _, ev := range recorder.snapshot() {
		require.Empty(t, ev.Content)
	}
}

func TestNonceThinkContentDoesNotLeakIntoFinalAnswer(t *testing.T) {
	model := &mockChat{responses: []mockResponse{{chunks: []types.StreamResponse{
		{ResponseType: types.ResponseTypeAnswer, Content: "<thi"},
		{ResponseType: types.ResponseTypeAnswer, Content: "nk:6124c78e>private reasoning"},
		{ResponseType: types.ResponseTypeAnswer, Content: "</think:612"},
		{ResponseType: types.ResponseTypeAnswer, Content: "4c78e>完整报告", Done: true, FinishReason: "stop"},
	}}}}
	e := newTestEngine(t, model)
	e.toolRegistry = agenttools.NewToolRegistry()
	recorder := &truncationRecorder{}
	recorder.attach(e.eventBus)
	state, err := e.Execute(context.Background(), "s", "m", "report", nil, nil)
	require.NoError(t, err)
	require.True(t, state.IsComplete)
	require.Equal(t, "完整报告", state.FinalAnswer)
	for _, emitted := range recorder.snapshot() {
		require.NotContains(t, emitted.Content, "private reasoning")
		require.NotContains(t, emitted.Content, "think:")
	}
}

func TestFinalSynthesisStripsNonceThinkContentBeforeStreaming(t *testing.T) {
	e := newTestEngine(t, &mockChat{responses: []mockResponse{{chunks: []types.StreamResponse{
		{
			ResponseType: types.ResponseTypeAnswer,
			Content:      "<think:6124c78e>private reasoning</think:6124c78e>完整报告",
			Done:         true,
			FinishReason: "stop",
		},
	}}}})
	recorder := &truncationRecorder{}
	recorder.attach(e.eventBus)
	state := &types.AgentState{}
	err := e.streamFinalAnswerToEventBus(
		context.Background(),
		"report",
		state,
		"s",
		[]chat.Message{{Role: "user", Content: "report"}},
	)
	require.NoError(t, err)
	require.Equal(t, "完整报告", state.FinalAnswer)
	for _, emitted := range recorder.snapshot() {
		require.NotContains(t, emitted.Content, "private reasoning")
		require.NotContains(t, emitted.Content, "think:")
	}
}

func TestOrphanToolCallsCloserRetriesFinalAnswerWithoutReplayingTools(t *testing.T) {
	model := &mockChat{responses: []mockResponse{
		{
			chunks: []types.StreamResponse{
				{
					ResponseType: types.ResponseTypeAnswer,
					Done:         true,
					FinishReason: "tool_calls",
					ToolCalls: []types.LLMToolCall{
						{
							ID:       "once",
							Type:     "function",
							Function: types.FunctionCall{Name: "read_sources", Arguments: `{}`},
						},
					},
				},
			},
		},
		{chunks: []types.StreamResponse{
			{ResponseType: types.ResponseTypeAnswer, Content: "\n\n</tool_"},
			{ResponseType: types.ResponseTypeAnswer, Content: "calls:6124c78e>", Done: true, FinishReason: "stop"},
		}},
		{
			chunks: []types.StreamResponse{
				{ResponseType: types.ResponseTypeAnswer, Content: "完整报告", Done: true, FinishReason: "stop"},
			},
		},
	}}
	e := newTestEngine(t, model)
	e.toolRegistry = agenttools.NewToolRegistry()
	source := newCountingTool("read_sources")
	e.toolRegistry.RegisterTool(source)
	recorder := &truncationRecorder{}
	recorder.attach(e.eventBus)
	state, err := e.Execute(context.Background(), "s", "m", "report", nil, nil)
	require.NoError(t, err)
	require.True(t, state.IsComplete)
	require.Equal(t, "完整报告", state.FinalAnswer)
	require.Equal(t, 1, source.calls)
	require.Len(t, model.calls, 3)
	for _, ev := range recorder.snapshot() {
		require.NotContains(t, ev.Content, "</tool_")
	}
}

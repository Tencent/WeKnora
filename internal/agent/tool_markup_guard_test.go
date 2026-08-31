package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLooksLikeLeakedToolMarkup(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{name: "fullwidth", content: "<｜｜DSML｜｜tool_calls>\n<｜｜DSML｜｜invoke name=\"knowledge_search\">", want: true},
		{name: "ascii", content: "  \n<||DSML||tool_calls>", want: true},
		{name: "single bars", content: "<|DSML|invoke name=\"knowledge_search\">", want: true},
		{name: "protocol discussion", content: "DSML is a tool-call protocol.", want: false},
		{name: "quoted example", content: "For example: <||DSML||tool_calls>", want: false},
		{name: "ordinary answer", content: "Here is the answer.", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, looksLikeLeakedToolMarkup(tt.content))
		})
	}
}

func TestToolMarkupStreamGuardBuffersOnlyAmbiguousPrefix(t *testing.T) {
	t.Run("split leaked envelope is rejected", func(t *testing.T) {
		guard := &toolMarkupStreamGuard{}
		assert.Empty(t, guard.Feed("\n<｜", false))
		assert.Empty(t, guard.Feed("｜DSML｜", false))
		assert.Empty(t, guard.Feed("｜tool_calls>payload", true))
		assert.True(t, guard.Rejected())
	})

	t.Run("ordinary text streams immediately", func(t *testing.T) {
		guard := &toolMarkupStreamGuard{}
		assert.Equal(t, "Hello ", guard.Feed("Hello ", false))
		assert.Equal(t, "world", guard.Feed("world", true))
		assert.False(t, guard.Rejected())
	})
}

func TestAnalyzeResponseRejectsLeakedToolMarkup(t *testing.T) {
	engine := newTestEngine(t, &mockChat{})
	response := &types.ChatResponse{
		FinishReason: "stop",
		Content:      "<｜｜DSML｜｜tool_calls>\n<｜｜DSML｜｜invoke name=\"knowledge_search\">",
	}

	verdict := engine.analyzeResponse(
		context.Background(), response, types.AgentStep{}, 0, "sess-1", time.Now(),
	)

	assert.True(t, verdict.isDone)
	assert.True(t, verdict.leakedToolMarkup)
	assert.Empty(t, verdict.finalAnswer)
}

func TestExecuteLoopRetriesLeakedToolMarkupWithoutEmittingOrPersistingIt(t *testing.T) {
	leaked := "<｜｜DSML｜｜tool_calls>\n<｜｜DSML｜｜invoke name=\"knowledge_search\">"
	model := &mockChat{responses: []mockResponse{
		{chunks: []types.StreamResponse{
			{ResponseType: types.ResponseTypeAnswer, Content: "<｜"},
			{ResponseType: types.ResponseTypeAnswer, Content: "｜DSML｜｜tool_calls>\n"},
			{ResponseType: types.ResponseTypeAnswer, Content: "<｜｜DSML｜｜invoke name=\"knowledge_search\">", Done: true, FinishReason: "stop"},
		}},
		{chunks: []types.StreamResponse{
			{ResponseType: types.ResponseTypeAnswer, Content: "Safe answer.", Done: true, FinishReason: "stop"},
		}},
	}}
	engine := newTestEngine(t, model)
	var emitted string
	engine.eventBus.On(event.EventAgentFinalAnswer, func(_ context.Context, evt event.Event) error {
		if data, ok := evt.Data.(event.AgentFinalAnswerData); ok {
			emitted += data.Content
		}
		return nil
	})

	state := &types.AgentState{}
	_, err := engine.executeLoop(context.Background(), state, "test query",
		emptyMessages(), emptyTools(), "sess-1", "msg-1")

	require.NoError(t, err)
	assert.True(t, state.IsComplete)
	assert.Equal(t, "Safe answer.", state.FinalAnswer)
	assert.Equal(t, "Safe answer.", emitted)
	assert.Equal(t, 2, model.callCount)
	for _, step := range state.RoundSteps {
		assert.NotContains(t, step.Thought, "DSML")
	}
	require.Len(t, model.calls, 2)
	for _, message := range model.calls[1] {
		assert.NotContains(t, message.Content, leaked)
	}
	assert.Contains(t, model.calls[1][len(model.calls[1])-1].Content, "proper function call")
}

func TestStreamFinalAnswerRejectsLeakedToolMarkupBeforeEmission(t *testing.T) {
	model := &mockChat{responses: []mockResponse{{chunks: []types.StreamResponse{
		{ResponseType: types.ResponseTypeAnswer, Content: "<||DSML||tool_"},
		{ResponseType: types.ResponseTypeAnswer, Content: "calls>payload", Done: true, FinishReason: "stop"},
	}}}}
	engine := newTestEngine(t, model)
	var emitted string
	engine.eventBus.On(event.EventAgentFinalAnswer, func(_ context.Context, evt event.Event) error {
		if data, ok := evt.Data.(event.AgentFinalAnswerData); ok {
			emitted += data.Content
		}
		return nil
	})

	state := &types.AgentState{}
	err := engine.streamFinalAnswerToEventBus(context.Background(), "test query", state, "sess-1")

	require.ErrorIs(t, err, errLeakedToolMarkup)
	assert.Empty(t, emitted)
	assert.Empty(t, state.FinalAnswer)
}

func TestRepeatedLeakedToolMarkupFallsBackInsteadOfTriggeringStuckLoop(t *testing.T) {
	leakedChunks := func() []types.StreamResponse {
		return []types.StreamResponse{{
			ResponseType: types.ResponseTypeAnswer,
			Content:      "<||DSML||tool_calls>payload",
			Done:         true,
			FinishReason: "stop",
		}}
	}
	model := &mockChat{responses: []mockResponse{
		{chunks: leakedChunks()},
		{chunks: leakedChunks()},
		{chunks: leakedChunks()},
	}}
	engine := newTestEngine(t, model)

	state := &types.AgentState{}
	_, err := engine.executeLoop(context.Background(), state, "test query",
		emptyMessages(), emptyTools(), "sess-1", "msg-1")

	require.NoError(t, err)
	assert.True(t, state.IsComplete)
	assert.NotContains(t, state.FinalAnswer, "DSML")
	assert.True(t, strings.HasPrefix(state.FinalAnswer, "I'm sorry"))
	assert.Empty(t, state.RoundSteps)
}

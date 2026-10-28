package agent

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestStreamRetryReplacesFailedPartialAnswer(t *testing.T) {
	model := &mockChat{responses: []mockResponse{
		{chunks: []types.StreamResponse{
			{ResponseType: types.ResponseTypeAnswer, Content: "partial draft"},
			{ResponseType: types.ResponseTypeError, Content: "upstream 503", Done: true},
		}},
		{chunks: []types.StreamResponse{
			{ResponseType: types.ResponseTypeAnswer, Content: "final answer"},
			{ResponseType: types.ResponseTypeAnswer, Done: true, FinishReason: "stop"},
		}},
	}}
	engine := newTestEngine(t, model)
	recorder := &truncationRecorder{}
	recorder.attach(engine.eventBus)

	state := &types.AgentState{}
	messages := emptyMessages()
	_, err := engine.callLLMWithRetry(
		context.Background(), &messages, emptyTools(), state, "question", 0, "session",
	)
	require.NoError(t, err)

	var rendered string
	replaces := 0
	for _, data := range recorder.snapshot() {
		if data.Replace {
			rendered = ""
			replaces++
		}
		rendered += data.Content
	}
	require.Equal(t, "final answer", rendered)
	require.Equal(t, 1, replaces)
}

func TestStreamFailureWithoutAnswerDoesNotEmitReplace(t *testing.T) {
	model := &mockChat{responses: []mockResponse{{chunks: []types.StreamResponse{
		{ResponseType: types.ResponseTypeError, Content: "upstream 503", Done: true},
	}}}}
	engine := newTestEngine(t, model)
	recorder := &truncationRecorder{}
	recorder.attach(engine.eventBus)

	messages := emptyMessages()
	_, err := engine.callLLMWithRetry(
		context.Background(), &messages, emptyTools(), &types.AgentState{}, "question", 0, "session",
	)
	require.Error(t, err)
	for _, data := range recorder.snapshot() {
		require.False(t, data.Replace, "a failed attempt with no visible answer has nothing to replace")
	}
}

func TestStreamCancellationKeepsPartialAnswer(t *testing.T) {
	model := &mockChat{responses: []mockResponse{{chunks: []types.StreamResponse{
		{ResponseType: types.ResponseTypeAnswer, Content: "partial draft"},
		{ResponseType: types.ResponseTypeError, Content: "context canceled", Done: true},
	}}}}
	engine := newTestEngine(t, model)
	recorder := &truncationRecorder{}
	recorder.attach(engine.eventBus)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	messages := emptyMessages()
	_, err := engine.callLLMWithRetry(
		ctx, &messages, emptyTools(), &types.AgentState{}, "question", 0, "session",
	)
	require.Error(t, err)
	for _, data := range recorder.snapshot() {
		require.False(t, data.Replace, "cancellation has no retry; keep the partial answer")
	}
}

func TestStreamSuccessDoesNotEmitReplace(t *testing.T) {
	model := &mockChat{responses: []mockResponse{{chunks: []types.StreamResponse{
		{ResponseType: types.ResponseTypeAnswer, Content: "final answer"},
		{ResponseType: types.ResponseTypeAnswer, Done: true, FinishReason: "stop"},
	}}}}
	engine := newTestEngine(t, model)
	recorder := &truncationRecorder{}
	recorder.attach(engine.eventBus)

	messages := emptyMessages()
	response, err := engine.callLLMWithRetry(
		context.Background(), &messages, emptyTools(), &types.AgentState{}, "question", 0, "session",
	)
	require.NoError(t, err)
	require.Equal(t, "final answer", response.Content)
	for _, data := range recorder.snapshot() {
		require.False(t, data.Replace)
	}
}

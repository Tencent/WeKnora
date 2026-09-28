package agent

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// A chunk the endpoint or a proxy mangled used to end the round outright: the
// failure carried neither a type nor a marker the retry classifier knew, so
// think.go never retried it and the turn fell through to the no-tool
// degradation path — on the user's side, a round in which no tool was called.
func TestIsTransientErrorCorruptStreamChunkIsRetryable(t *testing.T) {
	// What the client loops wrap a bad frame into. api.StreamAssembler.Fail
	// puts exactly this text into StreamResponse.Content.
	decodeErr := fmt.Errorf("decode stream chunk: %w: %w",
		api.ErrCorruptStreamChunk, errors.New("invalid character '<' looking for beginning of value"))
	// The agent rebuilds an error from that content, so only the text is left.
	flattened := errors.New("LLM stream error: " + decodeErr.Error())

	require.Contains(t, flattened.Error(), types.StreamChunkCorruptError,
		"the marker has to survive the error-chunk flattening to be of any use")

	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "typed: the error value still carries the sentinel",
			err:  decodeErr,
			want: true,
		},
		{
			name: "flattened: only the marker text made it through",
			err:  flattened,
			want: true,
		},
		{
			name: "flattened: a tool call that would not decode",
			err: errors.New("LLM stream error: decode tool call 0: " + types.StreamChunkCorruptError +
				": unexpected end of JSON input"),
			want: true,
		},
		{
			// The exact shape this failure had before the marker existed. It
			// names the corruption but nothing tells the classifier that the
			// request is worth sending again, so the round ended here.
			name: "no marker: the undecorated completions frame",
			err: errors.New("LLM stream error: decode stream chunk: " +
				"invalid character '<' looking for beginning of value"),
			want: false,
		},
		{
			name: "no marker: the undecorated SSE frame",
			err:  errors.New("LLM stream error: decode SSE response: unexpected end of JSON input"),
			want: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isTransientError(tc.err))
		})
	}
}

// corruptChunkResponse is what api.StreamAssembler.Fail puts on the stream for a
// frame nobody could decode: the flattened error text rides on an error chunk.
func corruptChunkResponse() types.StreamResponse {
	return types.StreamResponse{
		ResponseType: types.ResponseTypeError,
		Content: "decode stream chunk: " + types.StreamChunkCorruptError +
			": invalid character '<' looking for beginning of value",
		Done:         true,
		FinishReason: types.FinishReasonIncomplete,
	}
}

// collectAnswers records the answer events of one round, which is what the
// client concatenates into the message the user reads.
func collectAnswers(engine *AgentEngine) func() []string {
	var answers []string
	engine.eventBus.On(event.EventAgentFinalAnswer, func(_ context.Context, evt event.Event) error {
		if d, ok := evt.Data.(event.AgentFinalAnswerData); ok && d.Content != "" {
			answers = append(answers, d.Content)
		}
		return nil
	})
	return func() []string { return answers }
}

// A re-send streams the round from the start, and the client appends every
// answer event it receives. Retrying the mangled frame after "Hel" had already
// gone out would therefore leave "HelHello world" in the answer area — the
// partial answer followed by the whole one. The retry is skipped instead, and
// the round ends the way any other unrecoverable round does.
func TestCorruptStreamChunkIsNotRetriedAfterContentWasEmitted(t *testing.T) {
	model := &mockChat{responses: []mockResponse{
		{chunks: []types.StreamResponse{
			{ResponseType: types.ResponseTypeAnswer, Content: "Hel"},
			corruptChunkResponse(),
		}},
		// Prepared for the retry that must not happen; reaching it is the bug.
		{chunks: []types.StreamResponse{
			{
				ResponseType: types.ResponseTypeAnswer, Content: "Hello world", Done: true,
				FinishReason: "stop",
			},
		}},
	}}
	engine := newTestEngine(t, model)
	answers := collectAnswers(engine)

	messages := emptyMessages()
	response, err := engine.callLLMWithRetry(
		context.Background(), &messages, nil, &types.AgentState{}, "q", 0, "sess-1")

	require.Equal(t, 1, model.callCount, "a re-send after visible output would duplicate the answer")
	require.Equal(t, []string{"Hel"}, answers(), "only the partial answer reached the client")
	require.Error(t, err, "the hole in the answer still fails the round; it is not papered over")
	require.Nil(t, response)
}

// Before anything has been emitted, the same failure is invisible to retry:
// nobody has seen a byte, so the second attempt simply replaces the round that
// never appeared. This is the case the marker was added for — a proxy's HTML
// error page arriving instead of the first frame.
func TestCorruptStreamChunkIsRetriedBeforeAnythingWasEmitted(t *testing.T) {
	model := &mockChat{responses: []mockResponse{
		{chunks: []types.StreamResponse{corruptChunkResponse()}},
		{chunks: []types.StreamResponse{
			{
				ResponseType: types.ResponseTypeAnswer, Content: "Hello world", Done: true,
				FinishReason: "stop",
			},
		}},
	}}
	engine := newTestEngine(t, model)
	answers := collectAnswers(engine)

	messages := emptyMessages()
	response, err := engine.callLLMWithRetry(
		context.Background(), &messages, nil, &types.AgentState{}, "q", 0, "sess-1")

	require.NoError(t, err)
	require.Equal(t, 2, model.callCount, "the retry is invisible, so it must still happen")
	require.Equal(t, "Hello world", response.Content)
	require.Equal(t, []string{"Hello world"}, answers(),
		"the client sees exactly one answer, from the attempt that worked")
}

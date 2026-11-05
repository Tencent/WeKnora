package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

// finalAnswerChunk is a stream that completed normally: one answer chunk that
// also closes the stream.
func finalAnswerChunk(content string) types.StreamResponse {
	return types.StreamResponse{
		ResponseType: types.ResponseTypeAnswer,
		Content:      content,
		Done:         true,
		FinishReason: "stop",
	}
}

// runRound drives one callLLMWithRetry round over the prepared responses and
// reports what the client ends up with: how many attempts went out, the answer
// text the answer events concatenate to, and the round's outcome.
func runRound(t *testing.T, model *mockChat) (int, []string, *types.ChatResponse, error) {
	t.Helper()
	engine := newTestEngine(t, model)
	answers := collectAnswers(engine)
	messages := emptyMessages()
	response, err := engine.callLLMWithRetry(
		context.Background(), &messages, nil, &types.AgentState{}, "q", 0, "sess-1")
	return model.callCount, answers(), response, err
}

// transientFailure is one way an attempt can break transiently, as the
// streaming path produces it.
type transientFailure struct {
	name  string
	chunk types.StreamResponse
}

// nonCorruptTransientFailures are the transient stream failures the gate has to
// cover besides the mangled frame: a stream that ended before its finish
// reason, a provider 5xx and a read timeout. Each is shaped the way the
// streaming path hands it to the agent — a provider error is flattened into the
// text of an error chunk, which is the only form that survives as far as the
// retry loop.
func nonCorruptTransientFailures() []transientFailure {
	return []transientFailure{
		{
			// chat_completion_stream.go rewrites an answer that closed without a
			// finish reason into exactly this chunk before the agent sees it.
			name: "stream ended early",
			chunk: types.StreamResponse{
				ResponseType: types.ResponseTypeError,
				Content:      types.StreamEndedEarlyError,
				Done:         true,
				FinishReason: types.FinishReasonIncomplete,
			},
		},
		{
			name: "provider 503",
			chunk: types.StreamResponse{
				ResponseType: types.ResponseTypeError,
				Content:      "503 Service Unavailable",
				Done:         true,
				FinishReason: types.FinishReasonIncomplete,
			},
		},
		{
			name: "read timeout",
			chunk: types.StreamResponse{
				ResponseType: types.ResponseTypeError,
				Content:      `Post "https://api.example.com/v1/chat/completions": read tcp: i/o timeout`,
				Done:         true,
				FinishReason: types.FinishReasonIncomplete,
			},
		},
	}
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
		{chunks: []types.StreamResponse{finalAnswerChunk("Hello world")}},
	}}

	calls, answers, response, err := runRound(t, model)

	require.Equal(t, 1, calls, "a re-send after visible output would duplicate the answer")
	require.Equal(t, []string{"Hel"}, answers, "only the partial answer reached the client")
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
		{chunks: []types.StreamResponse{finalAnswerChunk("Hello world")}},
	}}

	calls, answers, response, err := runRound(t, model)

	require.NoError(t, err)
	require.Equal(t, 2, calls, "the retry is invisible, so it must still happen")
	require.Equal(t, "Hello world", response.Content)
	require.Equal(t, []string{"Hello world"}, answers,
		"the client sees exactly one answer, from the attempt that worked")
}

// The gate is not about the mangled frame: any transient failure re-sent after
// the client received part of the round duplicates that part, because the retry
// streams from the start under a new answer event ID and the client appends.
// "Hel" followed by "Hello world" reads as "HelHello world" whatever broke the
// first attempt — a 503 and a timeout repeat exactly what the bad frame did.
func TestTransientErrorsAreNotRetriedAfterContentWasEmitted(t *testing.T) {
	for _, tc := range nonCorruptTransientFailures() {
		t.Run(tc.name, func(t *testing.T) {
			require.NotContains(t, strings.ToLower(tc.chunk.Content), corruptStreamChunkMarker,
				"this case has to come from a non-corrupt transient class")
			model := &mockChat{responses: []mockResponse{
				{chunks: []types.StreamResponse{
					{ResponseType: types.ResponseTypeAnswer, Content: "Hel"},
					tc.chunk,
				}},
				// Prepared for the retry that must not happen; reaching it is the bug.
				{chunks: []types.StreamResponse{finalAnswerChunk("Hello world")}},
			}}

			calls, answers, response, err := runRound(t, model)

			require.Equal(t, 1, calls, "a re-send after visible output would duplicate the answer")
			require.Equal(t, []string{"Hel"}, answers, "only the partial answer reached the client")
			require.Error(t, err, "the broken round still fails; it is not papered over")
			require.Nil(t, response)
		})
	}
}

// The retry keeps its value exactly where it is invisible: nothing has been
// emitted, so the second attempt is the whole of what the user ever sees. The
// gate must not swallow this half, whatever the transient failure was.
func TestTransientErrorsAreRetriedBeforeAnythingWasEmitted(t *testing.T) {
	for _, tc := range nonCorruptTransientFailures() {
		t.Run(tc.name, func(t *testing.T) {
			model := &mockChat{responses: []mockResponse{
				{chunks: []types.StreamResponse{tc.chunk}},
				{chunks: []types.StreamResponse{finalAnswerChunk("Hello world")}},
			}}

			calls, answers, response, err := runRound(t, model)

			require.NoError(t, err)
			require.Equal(t, 2, calls, "the retry is invisible, so it must still happen")
			require.Equal(t, "Hello world", response.Content)
			require.Equal(t, []string{"Hello world"}, answers,
				"the client sees exactly one answer, from the attempt that worked")
		})
	}
}

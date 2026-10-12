package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// truncationRecorder captures every EventAgentFinalAnswer with its Truncated
// flag, so a test can tell an ordinary close from one that reports the
// completion cap.
type truncationRecorder struct {
	mu     sync.Mutex
	events []event.AgentFinalAnswerData
}

func (r *truncationRecorder) attach(bus *event.EventBus) {
	bus.On(event.EventAgentFinalAnswer, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.AgentFinalAnswerData)
		if !ok {
			return nil
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		r.events = append(r.events, data)
		return nil
	})
}

func (r *truncationRecorder) snapshot() []event.AgentFinalAnswerData {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]event.AgentFinalAnswerData(nil), r.events...)
}

func (r *truncationRecorder) doneEvent(t *testing.T) event.AgentFinalAnswerData {
	t.Helper()
	for _, e := range r.snapshot() {
		if e.Done {
			return e
		}
	}
	t.Fatal("no Done marker was emitted")
	return event.AgentFinalAnswerData{}
}

// A completion cap that cuts the answer off ends the turn. Before #3446 this
// verdict was non-terminal, and the half-written answer went back into the
// history for the model to rewrite from the top.
func TestAnalyzeResponse_TruncatedAnswerEndsTheTurn(t *testing.T) {
	engine := newTestEngine(t, &mockChat{})
	recorder := &truncationRecorder{}
	recorder.attach(engine.eventBus)

	verdict := engine.analyzeResponse(
		context.Background(),
		&types.ChatResponse{FinishReason: "length", Content: "step one of eight, and then"},
		types.AgentStep{}, 0, "sess-1", time.Now(),
	)

	assert.True(t, verdict.isDone, "a truncated answer must end the turn, not loop")
	assert.True(t, verdict.truncated)
	assert.False(t, verdict.emptyContent)
	assert.Equal(t, "step one of eight, and then", verdict.finalAnswer,
		"the partial text is the answer; there is nothing else to deliver")

	emitted := recorder.snapshot()
	require.Len(t, emitted, 1, "the answer was not streamed live, so it is emitted once here")
	assert.Equal(t, "step one of eight, and then", emitted[0].Content)
	assert.True(t, emitted[0].Truncated, "the client has to be told the text is partial")
	assert.False(t, emitted[0].Done, "Done is left to the caller, after the steer drain")
}

// max_tokens and max_output_tokens mean the same thing as length.
func TestAnalyzeResponse_TruncationAliasesEndTheTurn(t *testing.T) {
	for _, reason := range []string{"length", "max_tokens", "max_output_tokens", "MAX_TOKENS"} {
		t.Run(reason, func(t *testing.T) {
			engine := newTestEngine(t, &mockChat{})
			verdict := engine.analyzeResponse(
				context.Background(),
				&types.ChatResponse{FinishReason: reason, Content: "partial"},
				types.AgentStep{}, 0, "sess-1", time.Now(),
			)
			assert.True(t, verdict.isDone)
			assert.True(t, verdict.truncated)
		})
	}
}

// A reasoning model can spend the whole budget thinking and return no text at
// all. There is no partial answer to hand over, so this defers to the existing
// bounded empty-content retry rather than finishing with an empty message.
func TestAnalyzeResponse_TruncatedWithNoTextDefersToEmptyRetry(t *testing.T) {
	engine := newTestEngine(t, &mockChat{})
	recorder := &truncationRecorder{}
	recorder.attach(engine.eventBus)

	verdict := engine.analyzeResponse(
		context.Background(),
		&types.ChatResponse{FinishReason: "length", Content: "   "},
		types.AgentStep{}, 0, "sess-1", time.Now(),
	)

	assert.True(t, verdict.isDone)
	assert.True(t, verdict.emptyContent, "the caller nudges and retries; it must not end on an empty answer")
	assert.False(t, verdict.truncated)
	assert.Empty(t, recorder.snapshot(), "a retryable round must emit no answer event at all (#2906)")
}

// A round that hits the cap while still asking for tool work stays
// non-terminal: act.go fails the truncated calls and the model re-issues them.
func TestAnalyzeResponse_TruncatedToolCallRoundIsNotTerminal(t *testing.T) {
	engine := newTestEngine(t, &mockChat{})
	verdict := engine.analyzeResponse(
		context.Background(),
		&types.ChatResponse{
			FinishReason: "length",
			Content:      "let me look that up",
			ToolCalls: []types.LLMToolCall{{
				ID: "c1", Type: "function",
				Function: types.FunctionCall{Name: "wiki_read_page", Arguments: `{"slugs":["a`},
			}},
		},
		types.AgentStep{}, 0, "sess-1", time.Now(),
	)
	assert.False(t, verdict.isDone, "the tool-call path owns this round")
	assert.False(t, verdict.truncated)
}

// The #3446 reproduction. One response, cut off at the cap, no tool calls.
// The turn must end on it — mockChat fails the test if a second round is
// attempted, which is exactly what used to happen (and produced the
// rewrite-from-the-top spiral).
func TestExecuteLoop_TruncatedAnswerDoesNotReAnswer(t *testing.T) {
	mock := &mockChat{responses: []mockResponse{{chunks: []types.StreamResponse{{
		ResponseType: types.ResponseTypeAnswer,
		Content:      "1. First point. 2. Second point. 3. Third po",
		Done:         true,
		FinishReason: "length",
	}}}}}
	engine := newTestEngine(t, mock)
	recorder := &truncationRecorder{}
	recorder.attach(engine.eventBus)

	state := &types.AgentState{}
	_, err := engine.executeLoop(
		context.Background(), state, "write me a long outline",
		emptyMessages(), emptyTools(), "sess-1", "msg-1",
	)

	require.NoError(t, err)
	assert.Equal(t, 1, mock.callCount, "the loop must not start another round after a truncated answer")
	assert.True(t, state.IsComplete)
	assert.Equal(t, "1. First point. 2. Second point. 3. Third po", state.FinalAnswer)
	assert.True(t, recorder.doneEvent(t).Truncated,
		"a live-streamed answer only learns about the cap from the Done marker")
}

// Truncation that keeps landing inside tool-call arguments is the one shape
// that still loops: the call is refused, the model re-issues it, and the cap
// hits again. The guard stops it well before the round budget runs out.
func TestExecuteLoop_ConsecutiveTruncatedToolCallsStopAtTheGuard(t *testing.T) {
	truncatedToolRound := mockResponse{chunks: []types.StreamResponse{{
		ToolCalls: []types.LLMToolCall{{
			ID: "c1", Type: "function",
			Function: types.FunctionCall{Name: "wiki_read_page", Arguments: `{"slugs":["very-long`},
		}},
		Done:         true,
		FinishReason: "length",
	}}}
	// One more than the guard allows: reaching it would fail the test.
	rounds := make([]mockResponse, maxConsecutiveLengthRounds+1)
	for i := range rounds {
		rounds[i] = truncatedToolRound
	}
	mock := &mockChat{responses: rounds}
	engine := newTestEngine(t, mock, func(cfg *types.AgentConfig) { cfg.MaxIterations = 30 })

	state := &types.AgentState{}
	_, err := engine.executeLoop(
		context.Background(), state, "read every page",
		emptyMessages(), emptyTools(), "sess-1", "msg-1",
	)

	require.NoError(t, err)
	assert.Equal(t, maxConsecutiveLengthRounds, mock.callCount,
		"the guard must fire on the Nth consecutive capped round, not at MaxIterations")
	assert.True(t, state.IsComplete)
	assert.Equal(t, truncatedAnswerFallback, state.FinalAnswer,
		"no round produced answer text, so the fallback explains why")
}

// A finish reason the loop does not recognise, with no content and no tool
// calls, used to spin until MaxIterations: the stuck-loop guard skipped empty
// rounds and reset its own counter every time (#3446).
func TestExecuteLoop_RepeatedEmptyRoundsStopAtTheStuckGuard(t *testing.T) {
	emptyRound := mockResponse{chunks: []types.StreamResponse{{Done: true, FinishReason: "unrecognised"}}}
	rounds := make([]mockResponse, maxRepeatedResponseRounds+2)
	for i := range rounds {
		rounds[i] = emptyRound
	}
	mock := &mockChat{responses: rounds}
	engine := newTestEngine(t, mock, func(cfg *types.AgentConfig) { cfg.MaxIterations = 30 })

	state := &types.AgentState{}
	_, err := engine.executeLoop(
		context.Background(), state, "anything",
		emptyMessages(), emptyTools(), "sess-1", "msg-1",
	)

	require.NoError(t, err)
	assert.Equal(t, maxRepeatedResponseRounds+1, mock.callCount,
		"identical empty rounds must count as repeats")
	assert.True(t, state.IsComplete)
	assert.Equal(t, stalledAnswerFallback, state.FinalAnswer)
}

// A guard can stop a turn on a round whose text never went out as a stream —
// the fallback it chooses, or content the round accumulated without
// streaming. Either way the client must receive the text before the Done
// marker, not a Done for something it never saw.
func TestFinishStalledTurn_EmitsAnswerThatNeverStreamed(t *testing.T) {
	for _, tc := range []struct {
		name       string
		response   *types.ChatResponse
		truncated  bool
		wantAnswer string
	}{
		{
			name:       "unstreamed partial text is delivered as the answer",
			response:   &types.ChatResponse{Content: "half an answer"},
			truncated:  true,
			wantAnswer: "half an answer",
		},
		{
			name:       "no text at all falls back to the truncation message",
			response:   &types.ChatResponse{Content: ""},
			truncated:  true,
			wantAnswer: truncatedAnswerFallback,
		},
		{
			name:       "a non-truncation stall falls back to the generic message",
			response:   &types.ChatResponse{Content: "   "},
			truncated:  false,
			wantAnswer: stalledAnswerFallback,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine := newTestEngine(t, &mockChat{})
			recorder := &truncationRecorder{}
			recorder.attach(engine.eventBus)
			state := &types.AgentState{}

			engine.finishStalledTurn(context.Background(), state, "sess-1", tc.response, tc.truncated)

			assert.True(t, state.IsComplete)
			assert.Equal(t, tc.wantAnswer, state.FinalAnswer)

			emitted := recorder.snapshot()
			require.Len(t, emitted, 2, "the text, then the Done marker")
			assert.Equal(t, tc.wantAnswer, emitted[0].Content)
			assert.False(t, emitted[0].Done)
			assert.Equal(t, tc.truncated, emitted[0].Truncated)
			assert.True(t, emitted[1].Done)
			assert.Equal(t, tc.truncated, emitted[1].Truncated)
		})
	}
}

// When the round did stream its answer live, the guard must close that same
// stream rather than emit the text a second time.
func TestFinishStalledTurn_ReusesTheLiveAnswerStream(t *testing.T) {
	engine := newTestEngine(t, &mockChat{})
	recorder := &truncationRecorder{}
	recorder.attach(engine.eventBus)
	state := &types.AgentState{}

	engine.finishStalledTurn(context.Background(), state, "sess-1", &types.ChatResponse{
		Content:        "already on screen",
		AnswerStreamed: true,
		AnswerEventID:  "answer-live",
	}, true)

	emitted := recorder.snapshot()
	require.Len(t, emitted, 1, "re-emitting would render the answer twice")
	assert.True(t, emitted[0].Done)
	assert.True(t, emitted[0].Truncated)
	assert.Equal(t, "already on screen", state.FinalAnswer)
}

// A model that writes its reasoning inline as <think> can be cut off before
// the closing tag. The pair regex leaves such a block whole, so the round
// looks like it produced an answer when all it produced was reasoning —
// which would both leak the chain of thought into the answer area and skip
// the empty-content retry this case is supposed to use.
func TestAnalyzeResponse_UnterminatedThinkCountsAsNoAnswer(t *testing.T) {
	engine := newTestEngine(t, &mockChat{})
	recorder := &truncationRecorder{}
	recorder.attach(engine.eventBus)

	verdict := engine.analyzeResponse(
		context.Background(),
		&types.ChatResponse{
			FinishReason: "length",
			Content:      "<think>First I should enumerate the eight steps, then for each one",
		},
		types.AgentStep{}, 0, "sess-1", time.Now(),
	)

	assert.True(t, verdict.isDone)
	assert.True(t, verdict.emptyContent, "reasoning is not an answer; this must reach the empty-content retry")
	assert.False(t, verdict.truncated)
	assert.Empty(t, verdict.finalAnswer)
	assert.Empty(t, recorder.snapshot(), "the chain of thought must not be emitted as the answer")
}

// The same guard on a closed block followed by real text: the answer survives.
func TestAnalyzeResponse_ClosedThinkKeepsTheAnswerAfterIt(t *testing.T) {
	engine := newTestEngine(t, &mockChat{})
	verdict := engine.analyzeResponse(
		context.Background(),
		&types.ChatResponse{
			FinishReason: "length",
			Content:      "<think>planning</think>1. First point. 2. Second po",
		},
		types.AgentStep{}, 0, "sess-1", time.Now(),
	)
	assert.True(t, verdict.truncated)
	assert.Equal(t, "1. First point. 2. Second po", verdict.finalAnswer)
}

// The truncation has to survive the round trip through agent_steps: history is
// rebuilt from those, never from the live answer events.
func TestAnalyzeResponse_TruncatedVerdictMarksItsStep(t *testing.T) {
	engine := newTestEngine(t, &mockChat{})
	verdict := engine.analyzeResponse(
		context.Background(),
		&types.ChatResponse{FinishReason: "length", Content: "half an answer"},
		types.AgentStep{Iteration: 3}, 3, "sess-1", time.Now(),
	)
	require.True(t, verdict.truncated)
	assert.True(t, verdict.step.Truncated, "the step is what a reloaded conversation reads")

	// And it must survive the storage sanitizer that runs before the write.
	stored := agenttools.SanitizeAgentStepsForStorage([]types.AgentStep{verdict.step})
	require.Len(t, stored, 1)
	assert.True(t, stored[0].Truncated)
}

// Text that came with tool calls is a preamble, not an answer. The guard must
// not hand "let me look that up" to the user as the final answer.
func TestFinishStalledTurn_IgnoresAToolCallPreamble(t *testing.T) {
	engine := newTestEngine(t, &mockChat{})
	recorder := &truncationRecorder{}
	recorder.attach(engine.eventBus)
	state := &types.AgentState{}

	engine.finishStalledTurn(context.Background(), state, "sess-1", &types.ChatResponse{
		Content:        "let me look that up",
		AnswerStreamed: true,
		AnswerEventID:  "answer-live",
		ToolCalls: []types.LLMToolCall{{
			ID: "c1", Type: "function",
			Function: types.FunctionCall{Name: "wiki_read_page", Arguments: `{"slugs":["a`},
		}},
	}, true)

	assert.Equal(t, truncatedAnswerFallback, state.FinalAnswer,
		"a preamble must not become the answer")
	emitted := recorder.snapshot()
	require.Len(t, emitted, 2)
	assert.Equal(t, truncatedAnswerFallback, emitted[0].Content)
	assert.True(t, emitted[1].Done)
}

// The round that trips the consecutive-length guard is recorded like any
// other, and its refused tool calls are what let the UI retract the preamble
// from the answer area.
func TestExecuteLoop_ConsecutiveLengthRecordsTheFinalRound(t *testing.T) {
	truncatedToolRound := mockResponse{chunks: []types.StreamResponse{{
		ResponseType: types.ResponseTypeAnswer,
		Content:      "let me look that up",
		ToolCalls: []types.LLMToolCall{{
			ID: "c1", Type: "function",
			Function: types.FunctionCall{Name: "wiki_read_page", Arguments: `{"slugs":["very-long`},
		}},
		Done:         true,
		FinishReason: "length",
	}}}
	rounds := make([]mockResponse, maxConsecutiveLengthRounds)
	for i := range rounds {
		rounds[i] = truncatedToolRound
	}
	engine := newTestEngine(t, &mockChat{responses: rounds},
		func(cfg *types.AgentConfig) { cfg.MaxIterations = 30 })

	state := &types.AgentState{}
	_, err := engine.executeLoop(
		context.Background(), state, "read every page",
		emptyMessages(), emptyTools(), "sess-1", "msg-1",
	)

	require.NoError(t, err)
	require.Len(t, state.RoundSteps, maxConsecutiveLengthRounds,
		"the round that tripped the guard must not vanish from the transcript")
	last := state.RoundSteps[len(state.RoundSteps)-1]
	assert.True(t, last.Truncated)
	require.Len(t, last.ToolCalls, 1, "its tool calls are recorded as refused")
	require.NotNil(t, last.ToolCalls[0].Result)
	assert.False(t, last.ToolCalls[0].Result.Success)
	assert.Equal(t, truncatedAnswerFallback, state.FinalAnswer)
}

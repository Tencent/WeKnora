package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/background"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// fakeBackgroundRun hands out queued deliveries one call at a time.
type fakeBackgroundRun struct {
	roundStart  []background.Delivery
	beforeFinal []background.Delivery
	seen        [][]string
}

func (f *fakeBackgroundRun) RoundStart(context.Context) (background.Delivery, error) {
	if len(f.roundStart) == 0 {
		return background.Delivery{}, nil
	}
	d := f.roundStart[0]
	f.roundStart = f.roundStart[1:]
	return d, nil
}

func (f *fakeBackgroundRun) BeforeFinal(context.Context) (background.Delivery, error) {
	if len(f.beforeFinal) == 0 {
		return background.Delivery{}, nil
	}
	d := f.beforeFinal[0]
	f.beforeFinal = f.beforeFinal[1:]
	return d, nil
}

func (f *fakeBackgroundRun) TakeSeen() []string {
	if len(f.seen) == 0 {
		return nil
	}
	s := f.seen[0]
	f.seen = f.seen[1:]
	return s
}

func answer(content string) mockResponse {
	return mockResponse{chunks: []types.StreamResponse{
		{ResponseType: types.ResponseTypeAnswer, Content: content, Done: true, FinishReason: "stop"},
	}}
}

// userMessageContaining returns the first user message sent to the model that
// contains needle. Per-call envelopes may follow it, so position is not fixed.
func userMessageContaining(messages []chat.Message, needle string) string {
	for _, m := range messages {
		if m.Role == "user" && strings.Contains(m.Content, needle) {
			return m.Content
		}
	}
	return ""
}

func TestBackgroundNoticeIsInjectedBeforeTheRoundAndRecordedOnItsStep(t *testing.T) {
	model := &mockChat{responses: []mockResponse{answer("done")}}
	engine := newTestEngine(t, model)
	engine.SetBackgroundRun(&fakeBackgroundRun{roundStart: []background.Delivery{
		{Text: "build (sj_1) succeeded.", AckIDs: []string{"sj_1"}},
	}})

	state := &types.AgentState{}
	_, err := engine.executeLoop(context.Background(), state, "q", emptyMessages(), nil, "sess", "msg")
	require.NoError(t, err)

	want := "<background_notices>\nbuild (sj_1) succeeded.\n</background_notices>"
	require.Contains(t, userMessageContaining(model.calls[0], "<background_notices>"), want)
	require.Len(t, state.RoundSteps, 1)
	require.Equal(t, []types.BackgroundInjection{{Content: want, AckIDs: []string{"sj_1"}}},
		state.RoundSteps[0].BackgroundInjections)
	require.Empty(t, state.PendingBackground)
}

func TestBackgroundReminderKeepsTheLoopGoing(t *testing.T) {
	model := &mockChat{responses: []mockResponse{answer("first"), answer("second")}}
	engine := newTestEngine(t, model)
	engine.SetBackgroundRun(&fakeBackgroundRun{beforeFinal: []background.Delivery{
		{Text: "Background work you started in this turn is still running: build (sj_1)."},
	}})

	state := &types.AgentState{}
	_, err := engine.executeLoop(context.Background(), state, "q", emptyMessages(), nil, "sess", "msg")
	require.NoError(t, err)

	require.Len(t, model.calls, 2)
	require.NotEmpty(t, userMessageContaining(model.calls[1], "build (sj_1)"))
	require.Empty(t, userMessageContaining(model.calls[0], "build (sj_1)"), "the reminder comes only before finishing")
	require.Equal(t, "second", state.FinalAnswer)
	require.Len(t, state.RoundSteps, 2)
	require.True(t, state.RoundSteps[0].IntermediateAnswer)
	require.Len(t, state.RoundSteps[1].BackgroundInjections, 1)
}

func TestBackgroundSeenIDsRideOnTheToolStep(t *testing.T) {
	model := &mockChat{responses: []mockResponse{
		{chunks: []types.StreamResponse{{
			ResponseType: types.ResponseTypeAnswer,
			ToolCalls: []types.LLMToolCall{{
				ID: "call-1", Type: "function",
				Function: types.FunctionCall{Name: "probe", Arguments: "{}"},
			}},
			Done: true, FinishReason: "tool_calls",
		}}},
		answer("done"),
	}}
	engine := newTestEngine(t, model)
	engine.toolRegistry = agenttools.NewToolRegistry()
	engine.toolRegistry.RegisterTool(newCountingTool("probe"))
	engine.SetBackgroundRun(&fakeBackgroundRun{seen: [][]string{{"sj_9"}}})

	state := &types.AgentState{}
	_, err := engine.executeLoop(context.Background(), state, "q", emptyMessages(), nil, "sess", "msg")
	require.NoError(t, err)

	require.Len(t, state.RoundSteps, 2)
	require.Equal(t, []string{"sj_9"}, state.RoundSteps[0].BackgroundSeenIDs)
	require.Empty(t, state.RoundSteps[1].BackgroundSeenIDs)
}

func TestCompletionKeepsUndeliveredBackgroundOnATrailingStep(t *testing.T) {
	engine := newTestEngine(t, &mockChat{})
	engine.eventBus = event.NewEventBus()
	engine.SetBackgroundRun(&fakeBackgroundRun{seen: [][]string{{"sj_2"}}})
	var steps []types.AgentStep
	engine.eventBus.On(event.EventAgentComplete, func(_ context.Context, evt event.Event) error {
		steps, _ = evt.Data.(event.AgentCompleteData).AgentSteps.([]types.AgentStep)
		return nil
	})
	injection := types.BackgroundInjection{
		Content: "<background_notices>\nx\n</background_notices>",
		AckIDs:  []string{"sj_1"},
	}
	state := &types.AgentState{PendingBackground: []types.BackgroundInjection{injection}}

	engine.emitCompletionEvent(context.Background(), state, "sess", "msg", time.Now())

	require.Len(t, steps, 1)
	require.Equal(t, []types.BackgroundInjection{injection}, steps[0].BackgroundInjections)
	require.Equal(t, []string{"sj_2"}, steps[0].BackgroundSeenIDs)
}

func TestHasPendingInputPeeksAtTheSteerQueue(t *testing.T) {
	engine := newTestEngine(t, &mockChat{})
	require.False(t, engine.HasPendingInput(context.Background(), "sess", "msg"), "no sink, nothing pending")

	sink := &fakeSteerSink{queued: []map[string]interface{}{steerEntry("s1", "   ")}}
	engine.SetSteerSink(sink)
	require.False(t, engine.HasPendingInput(context.Background(), "sess", "msg"), "blank messages are dropped by drain")

	sink.queued = append(sink.queued, steerEntry("s2", "stop waiting"))
	require.True(t, engine.HasPendingInput(context.Background(), "sess", "msg"))
	require.Empty(t, sink.persisted, "peeking must not consume the message")
}

func TestBackgroundNoticesContentWrapsTrimmedText(t *testing.T) {
	require.Equal(t, "<background_notices>\na\nb\n</background_notices>", backgroundNoticesContent("  a\nb \n"))
	require.False(t, strings.Contains(backgroundNoticesContent("x"), "\n\n"))
}

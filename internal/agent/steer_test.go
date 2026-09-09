package agent

import (
	"context"
	"strings"
	"testing"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSteerSink is a types.SteerSink whose queue is fed by the test. It
// records persistence calls so the drain path can be asserted end to end.
type fakeSteerSink struct {
	queued      []map[string]interface{}
	persisted   []string // contents passed to PersistSteerMessage, in order
	userIDs     []string
	mentions    []types.MentionedItems // mentions recorded alongside each persist
	failPersist bool
	consumed    map[string]struct{}
}

func (s *fakeSteerSink) PollSteer(
	_ context.Context, _, _ string, lastOffset int,
) ([]map[string]interface{}, int, error) {
	_ = lastOffset
	out := make([]map[string]interface{}, 0)
	for _, evt := range s.queued {
		id, _ := evt["id"].(string)
		if _, ok := s.consumed[id]; ok {
			continue
		}
		out = append(out, evt)
	}
	return out, len(s.queued), nil
}

func (s *fakeSteerSink) PersistSteerMessage(
	_ context.Context, _, _, steerID, content string, mentionedItems types.MentionedItems, _ string,
) string {
	if s.failPersist {
		return ""
	}
	s.persisted = append(s.persisted, content)
	id := "user-row-for-" + content
	s.userIDs = append(s.userIDs, id)
	s.mentions = append(s.mentions, mentionedItems)
	if s.consumed == nil {
		s.consumed = map[string]struct{}{}
	}
	if steerID != "" {
		s.consumed[steerID] = struct{}{}
	}
	return id
}

func steerEntry(id, content string) map[string]interface{} {
	return map[string]interface{}{"id": id, "content": content}
}

// TestDrainSteerMessagesInjectsIntoTail verifies the core mid-run steering
// behaviour: a queued message becomes the last user message, is persisted,
// and emits an EventUserMessageInjected on the bus — all at the drain point,
// before the next LLM call would be built.
func TestDrainSteerMessagesInjectsIntoTail(t *testing.T) {
	model := &mockChat{}
	engine := newTestEngine(t, model)
	bus := event.NewEventBus()
	engine.eventBus = bus

	injectedCh := make(chan event.UserMessageInjectedData, 4)
	bus.On(event.EventUserMessageInjected, func(_ context.Context, evt event.Event) error {
		if data, ok := evt.Data.(event.UserMessageInjectedData); ok {
			injectedCh <- data
		}
		return nil
	})

	sink := &fakeSteerSink{
		queued: []map[string]interface{}{
			steerEntry("steer-1", "再补充一点：也对比一下成本"),
		},
	}
	engine.SetSteerSink(sink)

	messages := []chat.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "original query"},
		{Role: "assistant", Content: "", ToolCalls: []chat.ToolCall{{ID: "tc1"}}},
		{Role: "tool", Content: "tool output"},
	}
	state := &types.AgentState{CurrentRound: 1}
	engine.drainSteerMessages(context.Background(), state, &messages, "sess", "msg")

	require.Len(t, messages, 5)
	assert.Equal(t, "user", messages[4].Role)
	assert.Equal(t, "再补充一点：也对比一下成本", messages[4].Content)
	// Tool result pairing is untouched — the tool message stays where it was.
	assert.Equal(t, "tool", messages[3].Role)

	require.Len(t, sink.persisted, 1)
	assert.Equal(t, "再补充一点：也对比一下成本", sink.persisted[0])

	select {
	case data := <-injectedCh:
		assert.Equal(t, "steer-1", data.SteerID)
		assert.Equal(t, "msg", data.MessageID)
		assert.Equal(t, "user-row-for-再补充一点：也对比一下成本", data.UserMessageID)
	default:
		t.Fatal("expected an EventUserMessageInjected on the bus")
	}
}

// TestDrainSteerMessagesEmptyAndOffset pins that a drained queue is not
// replayed on the next boundary, and an empty queue is a no-op.
func TestDrainSteerMessagesEmptyAndOffset(t *testing.T) {
	model := &mockChat{}
	engine := newTestEngine(t, model)
	engine.eventBus = event.NewEventBus()
	sink := &fakeSteerSink{}
	engine.SetSteerSink(sink)

	messages := []chat.Message{{Role: "user", Content: "q"}}
	state := &types.AgentState{CurrentRound: 0}

	engine.drainSteerMessages(context.Background(), state, &messages, "sess", "msg")
	assert.Len(t, messages, 1)

	sink.queued = []map[string]interface{}{
		steerEntry("s1", "first"),
		steerEntry("s2", "second"),
	}
	engine.drainSteerMessages(context.Background(), state, &messages, "sess", "msg")
	assert.Len(t, messages, 3)
	assert.Equal(t, "first", messages[1].Content)
	assert.Equal(t, "second", messages[2].Content)
	require.Len(t, sink.persisted, 2)

	engine.drainSteerMessages(context.Background(), state, &messages, "sess", "msg")
	assert.Len(t, messages, 3)
	assert.Len(t, sink.persisted, 2)
}

// TestDrainSteerMessagesNilSinkNoOp ensures the default engine (no sink) is
// completely unaffected — the mid-run steering feature is opt-in.
func TestDrainSteerMessagesNilSinkNoOp(t *testing.T) {
	model := &mockChat{}
	engine := newTestEngine(t, model)
	engine.eventBus = event.NewEventBus()

	messages := []chat.Message{{Role: "user", Content: "q"}}
	state := &types.AgentState{CurrentRound: 0}
	engine.drainSteerMessages(context.Background(), state, &messages, "sess", "msg")
	assert.Len(t, messages, 1)
}

// TestDrainSteerMessagesBlankContentSkipped guards against blank steer
// payloads appending empty user turns that would confuse the model and
// SanitizeMessages alike.
func TestDrainSteerMessagesBlankContentSkipped(t *testing.T) {
	model := &mockChat{}
	engine := newTestEngine(t, model)
	engine.eventBus = event.NewEventBus()
	sink := &fakeSteerSink{
		queued: []map[string]interface{}{
			steerEntry("s1", "   \n  "),
			steerEntry("s2", "real instruction"),
		},
	}
	engine.SetSteerSink(sink)

	messages := []chat.Message{{Role: "user", Content: "q"}}
	state := &types.AgentState{CurrentRound: 0}
	engine.drainSteerMessages(context.Background(), state, &messages, "sess", "msg")

	require.Len(t, messages, 2)
	assert.Equal(t, "real instruction", messages[1].Content)
	require.Len(t, sink.persisted, 1)
}

// TestExecuteLoopInjectsBeforeNextLLMCall is the integration-level guard: with
// a sink wired, a queued steer message that arrives while round 1 is executing
// must appear in the messages array of round 2's LLM call.
func TestExecuteLoopInjectsBeforeNextLLMCall(t *testing.T) {
	// Round 1: model calls a tool; Round 2: model answers and stops.
	model := &mockChat{responses: []mockResponse{
		{chunks: []types.StreamResponse{
			{
				ResponseType: types.ResponseTypeAnswer,
				Content:      "let me check",
				ToolCalls: []types.LLMToolCall{{
					ID:       "tc1",
					Type:     "function",
					Function: types.FunctionCall{Name: "counting_tool", Arguments: "{}"},
				}},
			},
		}},
		{chunks: []types.StreamResponse{
			{ResponseType: types.ResponseTypeAnswer, Content: "done", Done: true},
		}},
	}}
	engine := newTestEngine(t, model)
	engine.eventBus = event.NewEventBus()

	sink := &fakeSteerSink{}
	engine.SetSteerSink(sink)

	// Simulate the user queueing a message while round 1 runs: it is in the
	// queue before the loop's second drain point executes.
	sink.queued = []map[string]interface{}{
		steerEntry("s1", "focus on the cost angle"),
	}

	engine.toolRegistry = agentRegistryForTest(t, "counting_tool")

	state := &types.AgentState{
		CurrentRound: 0,
		RoundSteps:   []types.AgentStep{},
	}
	messages := []chat.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "start"},
	}
	tools := engine.buildToolsForLLM()
	_, err := engine.executeLoop(context.Background(), state, "start", messages, tools, "sess", "msg")
	require.NoError(t, err)
	require.Len(t, model.calls, 2)

	// Round 2's call must contain the injected steer text. SanitizeMessages
	// merges consecutive same-role messages, so the injected user message
	// may be concatenated onto the original "start" user turn — that is the
	// desired end state (the model reads it as the user adding instructions
	// mid-task).
	second := model.calls[1]
	found := false
	for _, msg := range second {
		if msg.Role == "user" && strings.Contains(msg.Content, "focus on the cost angle") {
			found = true
			break
		}
	}
	assert.True(t, found, "injected steer text missing from round 2 LLM call: %+v", second)
	require.Len(t, sink.persisted, 1)
}

// delayedSteerSink hides queued messages until the Nth PollSteer call. Used
// to simulate a user sending a mid-run inject while round 1's LLM call is
// in flight — the round-start drain sees nothing, the natural-stop drain
// sees the message.
type delayedSteerSink struct {
	fakeSteerSink
	hideUntil int
	polls     int
}

func (s *delayedSteerSink) PollSteer(
	ctx context.Context, sessionID, messageID string, lastOffset int,
) ([]map[string]interface{}, int, error) {
	s.polls++
	if s.polls < s.hideUntil {
		return nil, lastOffset, nil
	}
	return s.fakeSteerSink.PollSteer(ctx, sessionID, messageID, lastOffset)
}

// TestExecuteLoopContinuesWhenSteerArrivesOnNaturalStop is the loop-end
// inject path: the model is about to finish, a user inject is waiting, and
// the engine must keep going instead of emitting a final answer.
func TestExecuteLoopContinuesWhenSteerArrivesOnNaturalStop(t *testing.T) {
	model := &mockChat{responses: []mockResponse{
		{chunks: []types.StreamResponse{
			{ResponseType: types.ResponseTypeAnswer, Content: "first draft", Done: true, FinishReason: "stop"},
		}},
		{chunks: []types.StreamResponse{
			{ResponseType: types.ResponseTypeAnswer, Content: "revised after steer", Done: true, FinishReason: "stop"},
		}},
	}}
	engine := newTestEngine(t, model)
	engine.eventBus = event.NewEventBus()

	sink := &delayedSteerSink{
		fakeSteerSink: fakeSteerSink{
			queued: []map[string]interface{}{
				steerEntry("s1", "rewrite this from the cost angle"),
			},
		},
		hideUntil: 2, // skip the round-1 start drain
	}
	engine.SetSteerSink(sink)

	state := &types.AgentState{RoundSteps: []types.AgentStep{}}
	messages := []chat.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "write a draft"},
	}
	_, err := engine.executeLoop(context.Background(), state, "write a draft", messages, nil, "sess", "msg")
	require.NoError(t, err)
	require.Len(t, model.calls, 2)

	second := model.calls[1]
	foundSteer := false
	foundDraft := false
	for _, msg := range second {
		if msg.Role == "user" && strings.Contains(msg.Content, "rewrite this from the cost angle") {
			foundSteer = true
		}
		if msg.Role == "assistant" && strings.Contains(msg.Content, "first draft") {
			foundDraft = true
		}
	}
	assert.True(t, foundSteer, "steer text missing from the continued round: %+v", second)
	assert.True(t, foundDraft, "the would-be final answer must stay in history so the model can revise it: %+v", second)
	assert.Equal(t, "revised after steer", state.FinalAnswer)
	require.Len(t, sink.persisted, 1)
}

// Persist failure must not append the text into the model context. Retrying
// the same event on the next boundary would otherwise duplicate it; leaving it
// unconsumed lets the next drain try again.
func TestDrainSteerMessagesSkipsAppendWhenPersistFails(t *testing.T) {
	model := &mockChat{}
	engine := newTestEngine(t, model)
	engine.eventBus = event.NewEventBus()
	sink := &fakeSteerSink{
		queued:      []map[string]interface{}{steerEntry("s1", "keep this pending")},
		failPersist: true,
	}
	engine.SetSteerSink(sink)

	messages := []chat.Message{{Role: "user", Content: "q"}}
	state := &types.AgentState{CurrentRound: 0}
	assert.Equal(t, 0, engine.drainSteerMessages(context.Background(), state, &messages, "sess", "msg"))
	assert.Len(t, messages, 1)
	assert.Empty(t, sink.persisted)
}

func countAnswerDone(bus *event.EventBus) *int {
	n := 0
	bus.On(event.EventAgentFinalAnswer, func(_ context.Context, evt event.Event) error {
		if data, ok := evt.Data.(event.AgentFinalAnswerData); ok && data.Done {
			n++
		}
		return nil
	})
	return &n
}

// Loop-end inject used to close the first answer (Done:true) before deciding
// to continue. The frontend then treated the session as idle and started a
// second AgentQA. Only the actual finishing round may emit Done.
func TestExecuteLoopLoopEndInjectDoesNotCloseAnswerBeforeContinue(t *testing.T) {
	model := &mockChat{responses: []mockResponse{
		{chunks: []types.StreamResponse{
			{ResponseType: types.ResponseTypeAnswer, Content: "first draft", Done: true, FinishReason: "stop"},
		}},
		{chunks: []types.StreamResponse{
			{ResponseType: types.ResponseTypeAnswer, Content: "revised after steer", Done: true, FinishReason: "stop"},
		}},
	}}
	engine := newTestEngine(t, model)
	bus := event.NewEventBus()
	engine.eventBus = bus
	doneCount := countAnswerDone(bus)

	sink := &delayedSteerSink{
		fakeSteerSink: fakeSteerSink{
			queued: []map[string]interface{}{
				steerEntry("s1", "rewrite this from the cost angle"),
			},
		},
		hideUntil: 2,
	}
	engine.SetSteerSink(sink)

	state := &types.AgentState{RoundSteps: []types.AgentStep{}}
	messages := []chat.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "write a draft"},
	}
	_, err := engine.executeLoop(context.Background(), state, "write a draft", messages, nil, "sess", "msg")
	require.NoError(t, err)
	require.Len(t, model.calls, 2)
	assert.Equal(t, 1, *doneCount, "Done:true must fire once, after the continued round actually finishes")
}

// Inject on the last in-budget round used to increment CurrentRound past the
// cap and fall into handleMaxIterations, which synthesizes from the original
// query and never runs another ReAct round.
func TestExecuteLoopLastRoundInjectRunsAnotherReActRound(t *testing.T) {
	model := &mockChat{responses: []mockResponse{
		{chunks: []types.StreamResponse{
			{ResponseType: types.ResponseTypeAnswer, Content: "first draft", Done: true, FinishReason: "stop"},
		}},
		{chunks: []types.StreamResponse{
			{ResponseType: types.ResponseTypeAnswer, Content: "revised after steer", Done: true, FinishReason: "stop"},
		}},
	}}
	engine := newTestEngine(t, model, withMaxIterations(1))
	engine.eventBus = event.NewEventBus()
	sink := &delayedSteerSink{
		fakeSteerSink: fakeSteerSink{
			queued: []map[string]interface{}{
				steerEntry("s1", "rewrite this from the cost angle"),
			},
		},
		hideUntil: 2,
	}
	engine.SetSteerSink(sink)

	state := &types.AgentState{RoundSteps: []types.AgentStep{}}
	messages := []chat.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "write a draft"},
	}
	_, err := engine.executeLoop(context.Background(), state, "write a draft", messages, nil, "sess", "msg")
	require.NoError(t, err)
	require.Len(t, model.calls, 2)
	assert.Equal(t, "revised after steer", state.FinalAnswer)
}

type sequencedSteerSink struct {
	fakeSteerSink
	byPoll map[int][]map[string]interface{}
	polls  int
}

func (s *sequencedSteerSink) PollSteer(
	ctx context.Context, sessionID, messageID string, lastOffset int,
) ([]map[string]interface{}, int, error) {
	s.polls++
	if batch, ok := s.byPoll[s.polls]; ok {
		s.queued = batch
	} else {
		s.queued = nil
	}
	return s.fakeSteerSink.PollSteer(ctx, sessionID, messageID, lastOffset)
}

// A second loop-end inject after the extra overrun round must not keep the
// turn alive. MaxIterations would otherwise be unbounded for a user who
// keeps sending delivery=inject.
func TestExecuteLoopSecondOverrunDoesNotContinue(t *testing.T) {
	model := &mockChat{responses: []mockResponse{
		{chunks: []types.StreamResponse{
			{ResponseType: types.ResponseTypeAnswer, Content: "first draft", Done: true, FinishReason: "stop"},
		}},
		{chunks: []types.StreamResponse{
			{ResponseType: types.ResponseTypeAnswer, Content: "revised after steer", Done: true, FinishReason: "stop"},
		}},
		{chunks: []types.StreamResponse{
			{ResponseType: types.ResponseTypeAnswer, Content: "should not run", Done: true, FinishReason: "stop"},
		}},
	}}
	engine := newTestEngine(t, model, withMaxIterations(1))
	engine.eventBus = event.NewEventBus()
	sink := &sequencedSteerSink{
		byPoll: map[int][]map[string]interface{}{
			2: {steerEntry("s1", "rewrite this from the cost angle")},
			4: {steerEntry("s2", "and again")},
		},
	}
	engine.SetSteerSink(sink)

	state := &types.AgentState{RoundSteps: []types.AgentStep{}}
	messages := []chat.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "write a draft"},
	}
	_, err := engine.executeLoop(context.Background(), state, "write a draft", messages, nil, "sess", "msg")
	require.NoError(t, err)
	require.Len(t, model.calls, 2)
	assert.Equal(t, "revised after steer", state.FinalAnswer)
	assert.Equal(t, []string{"rewrite this from the cost angle"}, sink.persisted)
}

// agentRegistryForTest builds a registry with one counting tool so the loop
// can exercise a full think → act → observe round without external services.
func agentRegistryForTest(t *testing.T, name string) *agenttools.ToolRegistry {
	t.Helper()
	registry := agenttools.NewToolRegistry()
	tool := newCountingTool(name)
	registry.RegisterTool(tool)
	return registry
}

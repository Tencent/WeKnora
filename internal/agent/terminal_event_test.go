package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// terminalAnswerRecorder captures every EventAgentFinalAnswer so tests can
// assert how many terminal (Done=true) markers a turn emitted and which
// answer text they closed.
type terminalAnswerRecorder struct {
	mu       sync.Mutex
	contents []string // Done=false payloads in emission order
	done     int      // Done=true terminal markers
}

func (r *terminalAnswerRecorder) attach(bus *event.EventBus) {
	bus.On(event.EventAgentFinalAnswer, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.AgentFinalAnswerData)
		if !ok {
			return nil
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		if data.Done {
			r.done++
		} else if data.Content != "" {
			r.contents = append(r.contents, data.Content)
		}
		return nil
	})
}

func (r *terminalAnswerRecorder) snapshot() (int, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.done, strings.Join(r.contents, "")
}

// TestAnalyzeResponse_EmptyNaturalStop_EmitsNoTerminalAnswer pins the #2906
// fix at the verdict layer: an empty natural stop is retryable, so it must
// not emit any EventAgentFinalAnswer — downstream consumers treat Done=true
// as "answer finished" and would terminate while the retry is still running.
func TestAnalyzeResponse_EmptyNaturalStop_EmitsNoTerminalAnswer(t *testing.T) {
	engine := newTestEngine(t, &mockChat{})
	recorder := &terminalAnswerRecorder{}
	recorder.attach(engine.eventBus)

	verdict := engine.analyzeResponse(
		context.Background(),
		&types.ChatResponse{FinishReason: "stop", Content: ""},
		types.AgentStep{}, 0, "sess-1", time.Now(),
	)

	done, content := recorder.snapshot()
	assert.True(t, verdict.isDone, "an empty natural stop still ends the round")
	assert.True(t, verdict.emptyContent, "the caller must see emptyContent to trigger the retry")
	assert.Empty(t, verdict.finalAnswer)
	assert.Equal(t, 0, done, "a retryable empty stop must not emit a terminal answer event")
	assert.Empty(t, content, "a retryable empty stop must not emit answer content either")
}

// TestExecuteLoop_EmptyThenAnswer_EmitsSingleTerminalAnswer is the
// deterministic reproduction from #2906: the first LLM response is an empty
// natural stop, the retry produces the real answer. The turn must end with
// exactly one terminal answer event carrying the real answer — not one
// premature empty marker plus a second one after the retry.
func TestExecuteLoop_EmptyThenAnswer_EmitsSingleTerminalAnswer(t *testing.T) {
	mock := &mockChat{
		responses: []mockResponse{
			{chunks: []types.StreamResponse{{Done: true}}},
			{chunks: []types.StreamResponse{{Content: "Here is my complete answer", Done: true}}},
		},
	}
	engine := newTestEngine(t, mock)
	recorder := &terminalAnswerRecorder{}
	recorder.attach(engine.eventBus)

	state := &types.AgentState{}
	_, err := engine.executeLoop(
		context.Background(),
		state,
		"test query",
		emptyMessages(),
		emptyTools(),
		"sess-1",
		"msg-1",
	)

	require.NoError(t, err)
	assert.True(t, state.IsComplete)
	assert.Equal(t, "Here is my complete answer", state.FinalAnswer)

	done, content := recorder.snapshot()
	assert.Equal(t, 1, done, "the whole turn must emit exactly one terminal answer event")
	assert.Contains(t, content, "Here is my complete answer")
	assert.NotContains(t, content, "unable to generate", "the retry succeeded, no fallback should surface")
}

// TestExecuteLoop_AllEmptyResponses_FallbackIsSoleTerminalAnswer covers the
// exhausted-retry path: analyzeResponse stays silent for every empty round,
// so the fallback the loop selects must be emitted as the turn's sole
// terminal answer.
func TestExecuteLoop_AllEmptyResponses_FallbackIsSoleTerminalAnswer(t *testing.T) {
	// 1 initial attempt + maxEmptyResponseRetries nudges.
	responses := make([]mockResponse, 0, 1+maxEmptyResponseRetries)
	for i := 0; i < 1+maxEmptyResponseRetries; i++ {
		responses = append(responses, mockResponse{chunks: []types.StreamResponse{{Done: true}}})
	}
	mock := &mockChat{responses: responses}

	engine := newTestEngine(t, mock)
	recorder := &terminalAnswerRecorder{}
	recorder.attach(engine.eventBus)

	state := &types.AgentState{}
	_, err := engine.executeLoop(
		context.Background(),
		state,
		"test query",
		emptyMessages(),
		emptyTools(),
		"sess-1",
		"msg-1",
	)

	require.NoError(t, err)
	assert.True(t, state.IsComplete)
	assert.NotEmpty(t, state.FinalAnswer)

	done, content := recorder.snapshot()
	assert.Equal(t, 1, done, "retries exhausted must still emit exactly one terminal answer event")
	assert.Contains(t, content, "unable to generate a response", "the terminal answer must be the fallback text")
}

func TestEmptyResponseRecoveryPreservesUserIntentAndTools(t *testing.T) {
	model := &mockChat{responses: []mockResponse{
		{chunks: []types.StreamResponse{{Done: true}}},
		{chunks: []types.StreamResponse{{Done: true, FinishReason: "tool_calls", ToolCalls: []types.LLMToolCall{{
			ID: "status", Type: "function", Function: types.FunctionCall{Name: "check_status", Arguments: `{}`},
		}}}}},
		{chunks: []types.StreamResponse{{Content: "Task result", Done: true}}},
	}}
	engine := newTestEngine(t, model)
	engine.toolRegistry = agenttools.NewToolRegistry()
	tool := newCountingTool("check_status")
	engine.toolRegistry.RegisterTool(tool)
	recorder := &terminalAnswerRecorder{}
	recorder.attach(engine.eventBus)

	state, err := engine.Execute(context.Background(), "s", "m", "Continue the task", nil, nil)
	require.NoError(t, err)
	require.True(t, state.IsComplete)
	require.Equal(t, "Task result", state.FinalAnswer)
	require.Equal(t, 1, tool.calls, "empty output must still allow work before answering")
	require.Len(t, model.calls, 3)
	userMessages := func(messages []chat.Message) []chat.Message {
		var users []chat.Message
		for _, message := range messages {
			if message.Role == "user" {
				users = append(users, message)
			}
		}
		return users
	}
	require.NotEmpty(t, userMessages(model.calls[0]))
	for _, messages := range model.calls[1:] {
		require.Equal(
			t,
			userMessages(model.calls[0]),
			userMessages(messages),
			"recovery must not fabricate user instructions",
		)
	}
	require.Equal(t, model.calls[0], model.calls[1], "retry must preserve the complete cacheable transcript")
	require.Empty(t, model.opts[0].RuntimeNotice)
	require.Contains(t, model.opts[1].RuntimeNotice, `"type":"empty_response_retry"`)
	require.Contains(t, model.opts[1].RuntimeNotice, "Tools and waiting remain available")
	require.Empty(t, model.opts[2].RuntimeNotice, "recovery applies to only the next model round")
	for _, messages := range model.calls {
		for _, message := range messages {
			require.NotContains(
				t,
				message.Content,
				`"type":"empty_response_retry"`,
				"recovery must not pollute durable history",
			)
		}
	}
	require.NotEmpty(t, model.opts[1].Tools)
	require.NotEqual(t, "none", model.opts[1].ToolChoice)
	done, _ := recorder.snapshot()
	require.Equal(t, 1, done, "recovery must not emit a premature final answer")
}

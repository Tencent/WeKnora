package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/compaction"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func finalAnswerResponse() mockResponse {
	return mockResponse{chunks: []types.StreamResponse{{
		ResponseType: types.ResponseTypeAnswer, Content: "Complete answer.", Done: true, FinishReason: "stop",
		Usage: &types.TokenUsage{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120},
	}}}
}

func finalToolMessages(content string) []chat.Message {
	return []chat.Message{
		{Role: "assistant", ToolCalls: []chat.ToolCall{{
			ID: "tool-1", Type: "function", Function: chat.FunctionCall{Name: "search", Arguments: "{}"},
		}}},
		{Role: "tool", ToolCallID: "tool-1", Name: "search", Content: content},
	}
}

func TestFinalAnswerPreservesManagedHistoryAndOutputBudget(t *testing.T) {
	model := &mockChat{responses: []mockResponse{finalAnswerResponse()}}
	engine := newTestEngine(t, model, withMaxContextTokens(200000), withMaxCompletionTokens(40960))
	state := &types.AgentState{RoundSteps: []types.AgentStep{{ToolCalls: []types.ToolCall{{
		Name: "search", Result: &types.ToolResult{Output: strings.Repeat("archived-only-evidence ", 100000)},
	}}}}}
	history := []chat.Message{emptyMessages()[0], compaction.SummaryMessage("Verified evidence summary")}
	history = append(history, finalToolMessages("Recent evidence")...)
	before, err := json.Marshal(history)
	require.NoError(t, err)
	require.NoError(t, engine.streamFinalAnswerToEventBus(t.Context(), "query", state, "session", history))
	require.Len(t, model.calls, 1)
	sent, err := json.Marshal(model.calls[0])
	require.NoError(t, err)
	require.Contains(t, string(sent), "Verified evidence summary")
	require.Contains(t, string(sent), "Recent evidence")
	require.NotContains(t, string(sent), "archived-only-evidence")
	require.Equal(t, 40960, model.opts[0].MaxCompletionTokens)
	require.Empty(t, model.opts[0].Tools)
	require.Equal(t, "none", model.opts[0].ToolChoice)
	require.LessOrEqual(t, engine.tokenEstimator.EstimateMessages(model.calls[0])+40960+contextSafetyTokens, 200000)
	after, err := json.Marshal(history)
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.True(t, strings.HasPrefix(state.RoundSteps[0].ToolCalls[0].Result.Output, "archived-only-evidence"))
	require.Equal(t, 120, state.TurnUsage.TotalTokens)
}

func TestFinalAnswerCompactsLastToolResults(t *testing.T) {
	for _, summaryFails := range []bool{false, true} {
		t.Run(fmt.Sprintf("summaryFails=%v", summaryFails), func(t *testing.T) {
			base := &mockChat{responses: []mockResponse{finalAnswerResponse()}}
			var model chat.Chat = base
			if !summaryFails {
				summarized := &summarizerChat{}
				summarized.responses = []mockResponse{finalAnswerResponse()}
				model = summarized
			}
			engine := newTestEngine(t, model, withMaxContextTokens(64000), withMaxCompletionTokens(40960))
			history := append(emptyMessages(),
				finalToolMessages(strings.Repeat("光功率预算需要核对。", 15000))...)
			before, err := json.Marshal(history)
			require.NoError(t, err)
			state := &types.AgentState{}
			require.NoError(t, engine.streamFinalAnswerToEventBus(t.Context(), "query", state, "session", history))
			if summarized, ok := model.(*summarizerChat); ok {
				base = &summarized.mockChat
				require.Positive(t, summarized.calls)
			}
			require.Len(t, base.calls, 1)
			require.Equal(t, 40960, base.opts[0].MaxCompletionTokens)
			require.LessOrEqual(t,
				engine.tokenEstimator.EstimateMessages(base.calls[0])+40960+contextSafetyTokens, 64000)
			require.Equal(t, "Complete answer.", state.FinalAnswer)
			after, err := json.Marshal(history)
			require.NoError(t, err)
			require.Equal(t, before, after)
		})
	}
}

func TestFinalAnswerRejectsUnshrinkablePromptBeforeProviderCall(t *testing.T) {
	model := &mockChat{}
	engine := newTestEngine(t, model, withMaxContextTokens(64000), withMaxCompletionTokens(40960))
	history := []chat.Message{{Role: "system", Content: strings.Repeat("mandatory instructions ", 40000)}}
	err := engine.streamFinalAnswerToEventBus(t.Context(), "query", &types.AgentState{}, "session", history)
	require.ErrorContains(t, err, "final answer context exceeds budget after compaction")
	require.Zero(t, model.callCount)
}

type finalOverflowChat struct {
	summarizerChat
	attempts   int
	alwaysFail bool
	streamErr  bool
	partial    string
	failure    string
	requests   [][]chat.Message
	cancel     context.CancelFunc
}

func (m *finalOverflowChat) ChatStream(
	ctx context.Context, messages []chat.Message, opts *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	m.attempts++
	m.requests = append(m.requests, append([]chat.Message(nil), messages...))
	if m.attempts == 1 || m.alwaysFail {
		if m.cancel != nil {
			m.cancel()
		}
		failure := m.failure
		if failure == "" {
			failure = "maximum context length exceeded"
		}
		if !m.streamErr {
			return nil, fmt.Errorf("%s", failure)
		}
		stream := make(chan types.StreamResponse, 2)
		if m.partial != "" {
			stream <- types.StreamResponse{ResponseType: types.ResponseTypeAnswer, Content: m.partial}
		}
		stream <- types.StreamResponse{ResponseType: types.ResponseTypeError, Content: failure}
		close(stream)
		return stream, nil
	}
	return m.mockChat.ChatStream(ctx, messages, opts)
}

func TestFinalAnswerOverflowRetryIsBounded(t *testing.T) {
	for _, streamErr := range []bool{false, true} {
		for _, alwaysFail := range []bool{false, true} {
			t.Run(fmt.Sprintf("streamErr=%v/alwaysFail=%v", streamErr, alwaysFail), func(t *testing.T) {
				model := &finalOverflowChat{streamErr: streamErr, alwaysFail: alwaysFail}
				model.responses = []mockResponse{finalAnswerResponse()}
				engine := newTestEngine(t, model, withMaxContextTokens(200000), withMaxCompletionTokens(40960))
				history := append(emptyMessages(), finalToolMessages(strings.Repeat("retrieved evidence ", 20000))...)
				doneEvents := 0
				engine.eventBus.On(event.EventAgentFinalAnswer, func(_ context.Context, evt event.Event) error {
					if evt.Data.(event.AgentFinalAnswerData).Done {
						doneEvents++
					}
					return nil
				})
				err := engine.streamFinalAnswerToEventBus(t.Context(), "query", &types.AgentState{}, "session", history)
				require.Equal(t, 2, model.attempts)
				require.Less(t, engine.tokenEstimator.EstimateMessages(model.requests[1]),
					engine.tokenEstimator.EstimateMessages(model.requests[0]))
				if alwaysFail {
					require.Error(t, err)
					require.Zero(t, doneEvents)
				} else {
					require.NoError(t, err)
					require.Equal(t, 1, doneEvents)
					require.Equal(t, 40960, model.opts[0].MaxCompletionTokens)
					require.Equal(t, "none", model.opts[0].ToolChoice)
				}
			})
		}
	}
}

func TestFinalAnswerDoesNotRetryPartialOutputOrUnrelatedErrors(t *testing.T) {
	for _, tc := range []struct {
		name, partial, failure string
	}{
		{name: "partial answer", partial: "Already delivered."},
		{name: "authentication failure", failure: "invalid API key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := &finalOverflowChat{streamErr: true, partial: tc.partial, failure: tc.failure}
			engine := newTestEngine(t, model, withMaxContextTokens(200000), withMaxCompletionTokens(40960))
			history := append(emptyMessages(), finalToolMessages(strings.Repeat("retrieved evidence ", 20000))...)
			var delivered strings.Builder
			doneEvents := 0
			engine.eventBus.On(event.EventAgentFinalAnswer, func(_ context.Context, evt event.Event) error {
				data := evt.Data.(event.AgentFinalAnswerData)
				delivered.WriteString(data.Content)
				if data.Done {
					doneEvents++
				}
				return nil
			})
			err := engine.streamFinalAnswerToEventBus(t.Context(), "query", &types.AgentState{}, "session", history)
			require.Error(t, err)
			require.Equal(t, 1, model.attempts)
			require.Zero(t, doneEvents)
			require.Equal(t, tc.partial, delivered.String())
		})
	}
}

func TestFinalAnswerDoesNotRetryWithoutSmallerContext(t *testing.T) {
	model := &finalOverflowChat{}
	engine := newTestEngine(t, model, withMaxContextTokens(200000), withMaxCompletionTokens(40960))
	err := engine.streamFinalAnswerToEventBus(t.Context(), "query", &types.AgentState{}, "session", emptyMessages())
	require.ErrorContains(t, err, "maximum context length exceeded")
	require.Equal(t, 1, model.attempts)
}

func TestFinalAnswerDoesNotCompactOrRetryAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	model := &finalOverflowChat{cancel: cancel}
	engine := newTestEngine(t, model, withMaxContextTokens(200000), withMaxCompletionTokens(40960))
	history := append(emptyMessages(), finalToolMessages(strings.Repeat("retrieved evidence ", 20000))...)
	err := engine.streamFinalAnswerToEventBus(ctx, "query", &types.AgentState{}, "session", history)
	require.Error(t, err)
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	require.Equal(t, 1, model.attempts)
	require.Zero(t, model.summarizerChat.calls)
}

func TestFinalAnswerBudgetIncludesSafetyMargin(t *testing.T) {
	engine := newTestEngine(t, &mockChat{}, withMaxContextTokens(200000), withMaxCompletionTokens(40960))
	messages := emptyMessages()
	engine.config.MaxContextTokens = engine.tokenEstimator.EstimateMessages(messages) + 40960 + contextSafetyTokens
	budget, err := engine.finalAnswerBudget(messages)
	require.NoError(t, err)
	require.Equal(t, 40960, budget)
	engine.config.MaxContextTokens--
	_, err = engine.finalAnswerBudget(messages)
	require.ErrorContains(t, err, "final answer context exceeds budget after compaction")
}

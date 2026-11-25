package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	agenttoken "github.com/Tencent/WeKnora/internal/agent/token"
	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/api/anthropicmessages"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// This fixture uses synthetic data only. No model service or deployment is needed.
type oversizedSynthesisChat struct {
	estimator *agenttoken.Estimator
	window    int
	err       error
	calls     int
	oversized int
	prompt    int
	output    int
}

func (m *oversizedSynthesisChat) GetModelName() string { return "synthetic-test-model" }
func (m *oversizedSynthesisChat) GetModelID() string   { return "synthetic-test-model" }
func (m *oversizedSynthesisChat) Chat(context.Context, []chat.Message, *chat.ChatOptions) (*types.ChatResponse, error) {
	return &types.ChatResponse{Content: "Short synthetic summary.", FinishReason: "stop"}, nil
}

func (m *oversizedSynthesisChat) ChatStream(
	_ context.Context, messages []chat.Message, opts *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	m.calls++
	if m.err != nil {
		return nil, m.err
	}
	m.prompt = m.estimator.EstimateMessages(messages)
	m.output = max(opts.MaxTokens, opts.MaxCompletionTokens)
	if m.prompt+m.output > m.window {
		m.oversized++
		return nil, fmt.Errorf("context_length_exceeded: input=%d output=%d window=%d", m.prompt, m.output, m.window)
	}
	ch := make(chan types.StreamResponse, 1)
	ch <- types.StreamResponse{
		ResponseType: types.ResponseTypeAnswer, Content: "Synthetic answer.", FinishReason: "stop", Done: true,
	}
	close(ch)
	return ch, nil
}

// Final synthesis must not emit a request it knows the provider will reject:
// when the estimated input already exceeds the context window, it should
// refuse before sending (or recover after a provider rejection).
func TestStreamFinalAnswer_RejectsOversizedInput(t *testing.T) {
	model := &oversizedSynthesisChat{window: 8192}
	engine := newTestEngine(t, model, withMaxContextTokens(8192), withMaxCompletionTokens(2048))
	model.estimator = engine.tokenEstimator
	messages := []chat.Message{
		{Role: "system", Content: "Use only the synthetic evidence."},
		{Role: "user", Content: "Summarize the evidence."},
		{Role: "assistant", ToolCalls: []chat.ToolCall{{
			ID: "synthetic-call", Type: "function",
			Function: chat.FunctionCall{Name: "synthetic_tool", Arguments: `{}`},
		}}},
		{
			Role: "tool", Name: "synthetic_tool", ToolCallID: "synthetic-call",
			Content: strings.Repeat("synthetic evidence ", 10000),
		},
	}
	state := &types.AgentState{}
	err := engine.streamFinalAnswerToEventBus(
		context.Background(), "Summarize the evidence.", state, "synthetic-session", messages)
	t.Logf("calls=%d prompt_tokens=%d completion_budget=%d window=%d err=%v",
		model.calls, model.prompt, model.output, model.window, err)
	require.True(t, model.oversized == 0 || (err == nil && model.calls > model.oversized),
		"final synthesis should reject oversized input before sending, or recover after provider rejection")
}

// A final-synthesis failure after the iteration budget is exhausted must stay
// observable by the caller: handleMaxIterations must propagate the error
// instead of swallowing it behind IsComplete=true and a nil error.
func TestHandleMaxIterations_PropagatesSynthesisError(t *testing.T) {
	want := errors.New("synthetic final-synthesis failure")
	model := &oversizedSynthesisChat{err: want}
	engine := newTestEngine(t, model, withMaxIterations(1))
	state := &types.AgentState{CurrentRound: 1}
	_, err := engine.executeLoop(context.Background(), state, "Synthetic task",
		emptyMessages(), nil, "synthetic-session", "synthetic-message")
	t.Logf("calls=%d complete=%v returned_error=%v final_answer=%q",
		model.calls, state.IsComplete, err, state.FinalAnswer)
	require.ErrorIs(t, err, want, "final-synthesis failures must remain observable by the caller")
}

// Compaction and final synthesis use the same model, but only synthesis carries
// a runtime notice. Keep the two call counts separate to bound recovery.
type finalSynthesisChat struct {
	mockChat
	summaries  int
	finals     [][]chat.Message
	summaryErr error
	onSummary  func()
	synthesize func(context.Context, []chat.Message, *chat.ChatOptions) (<-chan types.StreamResponse, error)
}

func (m *finalSynthesisChat) ChatStream(
	ctx context.Context, messages []chat.Message, opts *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	if len(opts.Tools) > 0 {
		return m.mockChat.ChatStream(ctx, messages, opts)
	}
	if opts.RuntimeNotice == "" {
		m.summaries++
		if m.onSummary != nil {
			m.onSummary()
		}
		if m.summaryErr != nil {
			return nil, m.summaryErr
		}
		return finalSynthesisChunks(types.StreamResponse{
			ResponseType: types.ResponseTypeAnswer, Content: "## Goal\nSummarize the synthetic evidence.",
			FinishReason: "stop", Done: true,
		}), nil
	}
	m.finals = append(m.finals, append([]chat.Message(nil), messages...))
	if m.synthesize != nil {
		return m.synthesize(ctx, messages, opts)
	}
	return finalSynthesisChunks(types.StreamResponse{
		ResponseType: types.ResponseTypeAnswer, Content: "Recovered answer.", FinishReason: "stop", Done: true,
	}), nil
}

func finalSynthesisChunks(chunks ...types.StreamResponse) <-chan types.StreamResponse {
	ch := make(chan types.StreamResponse, len(chunks))
	for _, chunk := range chunks {
		ch <- chunk
	}
	close(ch)
	return ch
}

func TestStreamFinalAnswer_DisablesThinking(t *testing.T) {
	for _, configured := range []string{"unset", "disabled", "enabled"} {
		t.Run(configured, func(t *testing.T) {
			model := &finalSynthesisChat{}
			engine := newTestEngine(t, model, withMaxIterations(8))
			if configured != "unset" {
				thinking := configured == "enabled"
				engine.config.Thinking = &thinking
			}
			model.synthesize = func(
				_ context.Context, _ []chat.Message, opts *chat.ChatOptions,
			) (<-chan types.StreamResponse, error) {
				level, requested := opts.Reasoning()
				require.True(t, requested, "final synthesis must override the provider default")
				require.Equal(t, api.ReasoningOff, level)
				return finalSynthesisChunks(types.StreamResponse{
					ResponseType: types.ResponseTypeAnswer, Content: "Final summary.", FinishReason: "stop", Done: true,
				}), nil
			}
			state := &types.AgentState{CurrentRound: 8}
			_, err := engine.executeLoop(t.Context(), state, "Summarize", emptyMessages(), nil, "session", "message")
			require.NoError(t, err)
			require.Len(t, model.finals, 1)
			require.Equal(t, "Final summary.", state.FinalAnswer)
		})
	}
}

func finalSynthesisInputTokens(e *AgentEngine, messages []chat.Message, opts *chat.ChatOptions) int {
	return e.tokenEstimator.EstimateMessages(messages) + e.tokenEstimator.EstimateMessage(
		&chat.Message{Role: "system", Content: opts.RuntimeNotice},
	)
}

func TestFinalSynthesisCompactsBeforeSendingOversizedInput(t *testing.T) {
	model := &finalSynthesisChat{}
	engine := newTestEngine(t, model, withMaxContextTokens(24000), withMaxCompletionTokens(2048))
	sink := &recordingCheckpointSink{}
	engine.SetContextCheckpointSink(sink)
	messages := append(storedHistoryOverflow(), chat.Message{
		Role: "user", Content: "Latest correction: answer in Chinese using only the supplied evidence.",
		Images: []string{"https://example.com/correction.png"},
	})
	// The long tool transcript belongs to a completed stored turn. The latest
	// correction starts the live turn, so compaction can persist turn-c.
	for i := 5; i < len(messages)-1; i++ {
		messages[i].TurnID = "turn-c"
	}
	original, err := json.Marshal(messages)
	require.NoError(t, err)
	model.synthesize = func(
		_ context.Context, sent []chat.Message, opts *chat.ChatOptions,
	) (<-chan types.StreamResponse, error) {
		require.LessOrEqual(t, finalSynthesisInputTokens(engine, sent, opts)+opts.MaxCompletionTokens, 24000)
		require.Empty(t, opts.Tools)
		require.Equal(t, "none", opts.ToolChoice)
		require.Equal(t, messages[len(messages)-1], sent[len(sent)-1], "retain the latest correction and images")
		return finalSynthesisChunks(types.StreamResponse{
			ResponseType: types.ResponseTypeAnswer, Content: "Recovered answer.",
			Done: true, FinishReason: "stop",
		}), nil
	}
	state := &types.AgentState{}
	require.NoError(t, engine.streamFinalAnswerToEventBus(t.Context(), "Summarize", state, "session", messages))
	require.Positive(t, model.summaries)
	require.Len(t, model.finals, 1)
	require.Equal(t, "Recovered answer.", state.FinalAnswer)
	require.Equal(t, []string{"turn-c"}, sink.turnIDs)
	after, err := json.Marshal(messages)
	require.NoError(t, err)
	require.JSONEq(t, string(original), string(after), "do not mutate the conversation saved by the caller")
}

func TestFinalSynthesisBoundsTheLastLargeToolResult(t *testing.T) {
	model := &finalSynthesisChat{}
	engine := newTestEngine(t, model, withMaxContextTokens(8192), withMaxCompletionTokens(2048))
	messages := []chat.Message{
		{Role: "system", Content: "Use only the evidence."},
		{Role: "user", Content: "Summarize."},
		{Role: "assistant", ToolCalls: []chat.ToolCall{{
			ID: "call-1", Type: "function",
			Function: chat.FunctionCall{Name: "synthetic_tool", Arguments: `{}`},
		}}},
		{
			Role: "tool", Name: "synthetic_tool", ToolCallID: "call-1",
			Content: strings.Repeat("synthetic evidence ", 10000),
		},
	}
	model.synthesize = func(
		_ context.Context, sent []chat.Message, opts *chat.ChatOptions,
	) (<-chan types.StreamResponse, error) {
		tokens := finalSynthesisInputTokens(engine, sent, opts)
		if tokens+opts.MaxCompletionTokens > 8192 {
			return nil, fmt.Errorf("context_length_exceeded: input=%d output=%d", tokens, opts.MaxCompletionTokens)
		}
		last := sent[len(sent)-1]
		require.Equal(t, "tool", last.Role)
		require.Equal(t, "call-1", last.ToolCallID)
		require.Equal(t, "call-1", sent[len(sent)-2].ToolCalls[0].ID)
		require.Contains(t, last.Content, "Tool result compacted")
		return finalSynthesisChunks(types.StreamResponse{
			ResponseType: types.ResponseTypeAnswer, Content: "Bounded answer.", Done: true, FinishReason: "stop",
		}), nil
	}
	state := &types.AgentState{}
	require.NoError(t, engine.streamFinalAnswerToEventBus(t.Context(), "Summarize", state, "session", messages))
	require.Len(t, model.finals, 1, "the known oversized input must never be sent")
	require.Equal(t, "Bounded answer.", state.FinalAnswer)
	require.Equal(t, strings.Repeat("synthetic evidence ", 10000), messages[len(messages)-1].Content)
}

func TestFinalSynthesisRecoversProviderOverflowOnce(t *testing.T) {
	for _, repeated := range []bool{false, true} {
		t.Run(fmt.Sprintf("repeated=%v", repeated), func(t *testing.T) {
			model := &finalSynthesisChat{}
			engine := newTestEngine(t, model, withMaxContextTokens(64000), withMaxCompletionTokens(2048))
			// A previous ReAct round's recovery must not consume the final call's
			// own retry: the final transcript includes that round's tool results.
			engine.overflowRecovered = true
			want := errors.New("context_length_exceeded: provider tokenizer counted more input")
			model.synthesize = func(
				_ context.Context, _ []chat.Message, _ *chat.ChatOptions,
			) (<-chan types.StreamResponse, error) {
				if len(model.finals) == 1 || repeated {
					return nil, want
				}
				return finalSynthesisChunks(types.StreamResponse{
					ResponseType: types.ResponseTypeAnswer, Content: "Recovered answer.",
					Done: true, FinishReason: "stop",
				}), nil
			}
			state := &types.AgentState{}
			err := engine.streamFinalAnswerToEventBus(
				t.Context(), "Summarize", state, "session", storedHistoryOverflow(),
			)
			if repeated {
				require.ErrorIs(t, err, want)
				require.Empty(t, state.FinalAnswer)
			} else {
				require.NoError(t, err)
				require.Equal(t, "Recovered answer.", state.FinalAnswer)
			}
			require.Len(t, model.finals, 2)
			require.Positive(t, model.summaries)
			require.Less(t, engine.tokenEstimator.EstimateMessages(model.finals[1]),
				engine.tokenEstimator.EstimateMessages(model.finals[0]))
		})
	}
}

func TestFinalSynthesisClearsPartialAnswerBeforeOverflowRetry(t *testing.T) {
	model := &finalSynthesisChat{}
	engine := newTestEngine(t, model, withMaxContextTokens(64000), withMaxCompletionTokens(2048))
	recorder := &truncationRecorder{}
	recorder.attach(engine.eventBus)
	model.synthesize = func(
		_ context.Context, _ []chat.Message, _ *chat.ChatOptions,
	) (<-chan types.StreamResponse, error) {
		if len(model.finals) == 1 {
			return finalSynthesisChunks(
				types.StreamResponse{ResponseType: types.ResponseTypeAnswer, Content: "Discard this partial answer."},
				types.StreamResponse{
					ResponseType: types.ResponseTypeError,
					Content:      "context_length_exceeded", Done: true,
				},
			), nil
		}
		return finalSynthesisChunks(types.StreamResponse{
			ResponseType: types.ResponseTypeAnswer, Content: "Recovered answer.",
			Done: true, FinishReason: "stop",
		}), nil
	}
	require.NoError(t, engine.streamFinalAnswerToEventBus(
		t.Context(), "Summarize", &types.AgentState{}, "session", storedHistoryOverflow(),
	))
	var rendered string
	done, resets := 0, 0
	for _, evt := range recorder.snapshot() {
		if evt.Replace {
			rendered = ""
			resets++
		}
		rendered += evt.Content
		if evt.Done {
			done++
		}
	}
	require.Equal(t, "Recovered answer.", rendered)
	require.Equal(t, 1, resets)
	require.Equal(t, 1, done)
}

func TestFinalSynthesisRecoversContextLengthStopAndAccountsForBothAttempts(t *testing.T) {
	model := &finalSynthesisChat{}
	engine := newTestEngine(t, model, withMaxContextTokens(64000), withMaxCompletionTokens(2048))
	recorder := &truncationRecorder{}
	recorder.attach(engine.eventBus)
	model.synthesize = func(
		_ context.Context, _ []chat.Message, _ *chat.ChatOptions,
	) (<-chan types.StreamResponse, error) {
		if len(model.finals) == 1 {
			return finalSynthesisChunks(types.StreamResponse{
				ResponseType: types.ResponseTypeAnswer, Content: "Unfinished answer.",
				FinishReason: "length", Done: true,
				Usage: &types.TokenUsage{PromptTokens: 63999, CompletionTokens: 1, TotalTokens: 64000},
			}), nil
		}
		return finalSynthesisChunks(types.StreamResponse{
			ResponseType: types.ResponseTypeAnswer, Content: "Recovered answer.", FinishReason: "stop", Done: true,
			Usage: &types.TokenUsage{PromptTokens: 5000, CompletionTokens: 50, TotalTokens: 5050},
		}), nil
	}
	state := &types.AgentState{}
	require.NoError(t, engine.streamFinalAnswerToEventBus(
		t.Context(), "Summarize", state, "session", storedHistoryOverflow(),
	))
	require.Len(t, model.finals, 2)
	require.Equal(t, "Recovered answer.", state.FinalAnswer)
	require.Equal(t, 69050, state.TurnUsage.TotalTokens)
	done := 0
	for _, evt := range recorder.snapshot() {
		require.NotContains(t, evt.Content, "Unfinished answer")
		if evt.Done {
			done++
		}
	}
	require.Equal(t, 1, done, "the rejected attempt must never close the answer")
}

func TestFinalSynthesisDoesNotRetryOrdinaryOutputTruncation(t *testing.T) {
	model := &finalSynthesisChat{}
	engine := newTestEngine(t, model, withMaxContextTokens(64000), withMaxCompletionTokens(2048))
	model.synthesize = func(
		_ context.Context, _ []chat.Message, _ *chat.ChatOptions,
	) (<-chan types.StreamResponse, error) {
		return finalSynthesisChunks(types.StreamResponse{
			ResponseType: types.ResponseTypeAnswer, Content: "Answer cut off at its output limit.",
			FinishReason: "length", Done: true,
			Usage: &types.TokenUsage{PromptTokens: 5000, CompletionTokens: 2048, TotalTokens: 7048},
		}), nil
	}
	state := &types.AgentState{}
	require.NoError(t, engine.streamFinalAnswerToEventBus(t.Context(), "Summarize", state, "session", emptyMessages()))
	require.Len(t, model.finals, 1)
	require.Zero(t, model.summaries)
	require.Equal(t, "Answer cut off at its output limit.", state.FinalAnswer)
}

func TestFinalSynthesisRejectsUnshrinkableInput(t *testing.T) {
	model := &finalSynthesisChat{}
	engine := newTestEngine(t, model, withMaxContextTokens(8192), withMaxCompletionTokens(2048))
	messages := []chat.Message{{Role: "system", Content: strings.Repeat("fixed policy ", 10000)}}
	err := engine.streamFinalAnswerToEventBus(t.Context(), "Summarize", &types.AgentState{}, "session", messages)
	require.ErrorContains(t, err, "context window")
	require.Empty(t, model.finals)
	require.Zero(t, model.summaries)
}

func TestFinalSynthesisDoesNotRetryWithoutSmallerContext(t *testing.T) {
	model := &finalSynthesisChat{}
	engine := newTestEngine(t, model, withMaxContextTokens(8192))
	want := errors.New("context_length_exceeded")
	model.synthesize = func(
		context.Context, []chat.Message, *chat.ChatOptions,
	) (<-chan types.StreamResponse, error) {
		return nil, want
	}
	err := engine.streamFinalAnswerToEventBus(t.Context(), "Summarize", &types.AgentState{}, "session", emptyMessages())
	require.ErrorIs(t, err, want)
	require.Len(t, model.finals, 1)
	require.Zero(t, model.summaries)
}

func TestFinalSynthesisCancellationSkipsRecovery(t *testing.T) {
	for _, beforeCall := range []bool{false, true} {
		t.Run(fmt.Sprintf("before_call=%v", beforeCall), func(t *testing.T) {
			model := &finalSynthesisChat{}
			engine := newTestEngine(t, model, withMaxContextTokens(64000))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if beforeCall {
				cancel()
			}
			model.synthesize = func(
				context.Context, []chat.Message, *chat.ChatOptions,
			) (<-chan types.StreamResponse, error) {
				cancel()
				return nil, errors.New("context_length_exceeded")
			}
			err := engine.streamFinalAnswerToEventBus(
				ctx, "Summarize", &types.AgentState{}, "session", storedHistoryOverflow(),
			)
			require.ErrorIs(t, err, context.Canceled)
			require.Zero(t, model.summaries)
			if beforeCall {
				require.Empty(t, model.finals)
			} else {
				require.Len(t, model.finals, 1)
			}
		})
	}
}

func TestFinalSynthesisCancellationDuringCompactionSkipsModelCall(t *testing.T) {
	model := &finalSynthesisChat{summaryErr: context.Canceled}
	engine := newTestEngine(t, model, withMaxContextTokens(24000), withMaxCompletionTokens(2048))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	model.onSummary = cancel
	err := engine.streamFinalAnswerToEventBus(
		ctx, "Summarize", &types.AgentState{}, "session", storedHistoryOverflow(),
	)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, model.finals)
}

func TestFinalSynthesisRecoversWhenSummarizerFails(t *testing.T) {
	model := &finalSynthesisChat{summaryErr: errors.New("summarizer unavailable")}
	engine := newTestEngine(t, model, withMaxContextTokens(24000), withMaxCompletionTokens(2048))
	model.synthesize = func(
		_ context.Context, sent []chat.Message, opts *chat.ChatOptions,
	) (<-chan types.StreamResponse, error) {
		require.LessOrEqual(t, finalSynthesisInputTokens(engine, sent, opts)+opts.MaxCompletionTokens, 24000)
		return finalSynthesisChunks(types.StreamResponse{
			ResponseType: types.ResponseTypeAnswer,
			Content:      "Recovered from bounded archive.", Done: true, FinishReason: "stop",
		}), nil
	}
	state := &types.AgentState{}
	require.NoError(t, engine.streamFinalAnswerToEventBus(
		t.Context(), "Summarize", state, "session", storedHistoryOverflow(),
	))
	require.Positive(t, model.summaries)
	require.LessOrEqual(t, model.summaries, 4, "at most two attempts each for history and turn-prefix summaries")
	require.Len(t, model.finals, 1)
	require.Equal(t, "Recovered from bounded archive.", state.FinalAnswer)
}

type finalSynthesisLargeTool struct {
	*countingTool
	output string
}

func (tool *finalSynthesisLargeTool) Execute(context.Context, json.RawMessage) (*types.ToolResult, error) {
	tool.calls++
	return &types.ToolResult{Success: true, Output: tool.output}, nil
}

func TestMaxIterationsCompactsFinalAnswerWithoutReplayingTools(t *testing.T) {
	model := &finalSynthesisChat{mockChat: mockChat{responses: []mockResponse{{chunks: []types.StreamResponse{{
		ResponseType: types.ResponseTypeAnswer, FinishReason: "tool_calls", Done: true,
		ToolCalls: []types.LLMToolCall{{ID: "call-once", Type: "function", Function: types.FunctionCall{
			Name: "synthetic_tool", Arguments: `{}`,
		}}},
	}}}}}}
	engine := newTestEngine(t, model, withMaxIterations(1), withMaxContextTokens(8192), withMaxCompletionTokens(2048))
	engine.toolRegistry = agenttools.NewToolRegistry()
	tool := &finalSynthesisLargeTool{
		countingTool: newCountingTool("synthetic_tool"),
		output:       strings.Repeat("synthetic evidence ", 10000),
	}
	engine.toolRegistry.RegisterTool(tool)
	var recordedOutput string
	engine.eventBus.On(event.EventAgentToolResult, func(_ context.Context, evt event.Event) error {
		recordedOutput = evt.Data.(event.AgentToolResultData).Output
		return nil
	})
	model.synthesize = func(
		_ context.Context, sent []chat.Message, opts *chat.ChatOptions,
	) (<-chan types.StreamResponse, error) {
		require.LessOrEqual(t, finalSynthesisInputTokens(engine, sent, opts)+opts.MaxCompletionTokens, 8192)
		return finalSynthesisChunks(types.StreamResponse{
			ResponseType: types.ResponseTypeAnswer,
			Content:      "Final answer after tool execution.", Done: true, FinishReason: "stop",
		}), nil
	}
	state := &types.AgentState{}
	_, err := engine.executeLoop(
		t.Context(), state, "Summarize", emptyMessages(), engine.buildToolsForLLM(), "session", "message",
	)
	require.NoError(t, err)
	require.Equal(t, 1, tool.calls)
	require.Len(t, model.finals, 1)
	require.True(t, state.IsComplete)
	require.Equal(t, "Final answer after tool execution.", state.FinalAnswer)
	require.Len(t, state.RoundSteps, 1)
	require.NotEmpty(t, recordedOutput)
	require.Equal(t, recordedOutput, state.RoundSteps[0].ToolCalls[0].Result.Output,
		"synthesis trimming must not alter the tool result already emitted and retained for persistence")
	require.Contains(t, model.finals[0][len(model.finals[0])-1].Content, "Tool result compacted")
}

func TestFinalSynthesisDoesNotDoubleCountAnthropicThinking(t *testing.T) {
	model := &finalSynthesisChat{}
	engine := newTestEngine(t, model, withMaxContextTokens(32768), withMaxCompletionTokens(4096))
	thinking := strings.Repeat("synthetic reasoning ", 10000)
	blocks, err := json.Marshal([]map[string]string{
		{"type": "thinking", "thinking": thinking, "signature": "synthetic-signature"},
	})
	require.NoError(t, err)
	messages := []chat.Message{
		{Role: "system", Content: "Use the evidence."},
		{Role: "user", Content: "Answer briefly."},
		{
			Role: "assistant", ReasoningContent: thinking,
			ReasoningSignature: api.TagSignature(api.APIAnthropicMessages, "synthetic-signature"),
			ReasoningMetadata:  types.ProviderMetadata{anthropicmessages.MetadataThinkingBlocks: blocks},
			ToolCalls: []chat.ToolCall{{
				ID: "call-1", Type: "function",
				Function: chat.FunctionCall{Name: "synthetic_tool", Arguments: `{}`},
			}},
		},
		{Role: "tool", ToolCallID: "call-1", Name: "synthetic_tool", Content: "Successful tool result."},
	}
	client := anthropicmessages.New(anthropicmessages.Config{
		Endpoint: api.Endpoint{Model: "synthetic-anthropic"}, Settings: api.DefaultAnthropicMessages(),
	})
	model.synthesize = func(
		_ context.Context, sent []chat.Message, opts *chat.ChatOptions,
	) (<-chan types.StreamResponse, error) {
		body, err := client.BuildRequestBody(sent, opts, true)
		require.NoError(t, err)
		wire, err := json.Marshal(body)
		require.NoError(t, err)
		require.Less(t, engine.tokenEstimator.EstimateString(string(wire))+opts.MaxCompletionTokens, 32768)
		require.Equal(t, 4096, opts.MaxCompletionTokens, "duplicate display fields must not consume the output budget")
		return finalSynthesisChunks(types.StreamResponse{
			ResponseType: types.ResponseTypeAnswer, Content: "OK", Done: true, FinishReason: "stop",
		}), nil
	}
	require.NoError(t, engine.streamFinalAnswerToEventBus(
		t.Context(), "Answer briefly", &types.AgentState{}, "session", messages,
	))
	require.Zero(t, model.summaries, "a request that fits must retain the signed thinking without compaction")
	require.Len(t, model.finals, 1)
}

func TestMaxIterationsPropagatesSynthesisFailureAndStillCompletes(t *testing.T) {
	model := &finalSynthesisChat{}
	engine := newTestEngine(t, model, withMaxIterations(1))
	want := errors.New("synthetic synthesis failure")
	model.synthesize = func(
		context.Context, []chat.Message, *chat.ChatOptions,
	) (<-chan types.StreamResponse, error) {
		return nil, want
	}
	var completions []event.AgentCompleteData
	engine.eventBus.On(event.EventAgentComplete, func(_ context.Context, evt event.Event) error {
		completions = append(completions, evt.Data.(event.AgentCompleteData))
		return nil
	})
	state := &types.AgentState{
		CurrentRound: 1,
		RoundSteps:   []types.AgentStep{{Iteration: 0, Thought: "Collected evidence"}},
	}
	_, err := engine.executeLoop(t.Context(), state, "Summarize", emptyMessages(), nil, "session", "message")
	require.ErrorIs(t, err, want)
	require.Len(t, model.finals, 1)
	require.True(t, state.IsComplete)
	require.Equal(t, "Sorry, I was unable to generate a complete answer.", state.FinalAnswer)
	require.Len(t, completions, 1)
	require.Equal(t, state.FinalAnswer, completions[0].FinalAnswer)
	require.Equal(t, state.RoundSteps, completions[0].AgentSteps)
	require.Equal(t, "message", completions[0].MessageID)
}

package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	agenttoken "github.com/Tencent/WeKnora/internal/agent/token"
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
func (m *oversizedSynthesisChat) ChatStream(_ context.Context, messages []chat.Message, opts *chat.ChatOptions) (<-chan types.StreamResponse, error) {
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
	ch <- types.StreamResponse{ResponseType: types.ResponseTypeAnswer, Content: "Synthetic answer.", FinishReason: "stop", Done: true}
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
		{Role: "assistant", ToolCalls: []chat.ToolCall{{ID: "synthetic-call", Type: "function", Function: chat.FunctionCall{Name: "synthetic_tool", Arguments: `{}`}}}},
		{Role: "tool", Name: "synthetic_tool", ToolCallID: "synthetic-call", Content: strings.Repeat("synthetic evidence ", 10000)},
	}
	state := &types.AgentState{}
	err := engine.streamFinalAnswerToEventBus(context.Background(), "Summarize the evidence.", state, "synthetic-session", messages)
	t.Logf("calls=%d prompt_tokens=%d completion_budget=%d window=%d err=%v", model.calls, model.prompt, model.output, model.window, err)
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
	_, err := engine.executeLoop(context.Background(), state, "Synthetic task", emptyMessages(), nil, "synthetic-session", "synthetic-message")
	t.Logf("calls=%d complete=%v returned_error=%v final_answer=%q", model.calls, state.IsComplete, err, state.FinalAnswer)
	require.ErrorIs(t, err, want, "final-synthesis failures must remain observable by the caller")
}

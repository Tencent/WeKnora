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

// issue4606Chat is a synthetic model fixture. It reports a window and can be
// armed to reject oversized prompts (mimicking a provider's context-length
// rejection) or to fail outright. No model service or deployment is involved.
type issue4606Chat struct {
	estimator *agenttoken.Estimator
	window    int
	err       error
	calls     int
	oversized int
	prompt    int
	output    int
}

func (m *issue4606Chat) GetModelName() string { return "synthetic-test-model" }
func (m *issue4606Chat) GetModelID() string   { return "synthetic-test-model" }

func (m *issue4606Chat) Chat(context.Context, []chat.Message, *chat.ChatOptions) (*types.ChatResponse, error) {
	return &types.ChatResponse{Content: "Short synthetic summary.", FinishReason: "stop"}, nil
}

func (m *issue4606Chat) ChatStream(_ context.Context, messages []chat.Message, opts *chat.ChatOptions) (<-chan types.StreamResponse, error) {
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

// TestIssue4606_FinalSynthesisBoundsInput covers the overflow half: final
// synthesis must either reject an oversized input before sending, or compact
// and recover after the provider rejects it. It must never silently shrink the
// completion budget to 1 and still send the oversized request.
func TestIssue4606_FinalSynthesisBoundsInput(t *testing.T) {
	model := &issue4606Chat{window: 8192}
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

// TestIssue4606_MaxIterationsPropagatesSynthesisError covers the error-
// propagation half: a final-synthesis failure must stay observable by the
// caller instead of being swallowed into the generic fallback.
func TestIssue4606_MaxIterationsPropagatesSynthesisError(t *testing.T) {
	want := errors.New("synthetic final-synthesis failure")
	model := &issue4606Chat{err: want}
	engine := newTestEngine(t, model, withMaxIterations(1))
	state := &types.AgentState{CurrentRound: 1}
	_, err := engine.executeLoop(context.Background(), state, "Synthetic task", emptyMessages(), nil, "synthetic-session", "synthetic-message")
	t.Logf("calls=%d complete=%v returned_error=%v final_answer=%q", model.calls, state.IsComplete, err, state.FinalAnswer)
	require.ErrorIs(t, err, want, "final-synthesis failures must remain observable by the caller")
}

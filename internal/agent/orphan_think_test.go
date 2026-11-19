package agent

import (
	"context"
	"strings"
	"testing"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func orphanThinkResponse() mockResponse {
	return mockResponse{chunks: []types.StreamResponse{
		{ResponseType: types.ResponseTypeAnswer, Content: "refs\n\n任务全部结束，先整理证据。"},
		{ResponseType: types.ResponseTypeAnswer, Content: "Prepare complete summary.</thi"},
		{ResponseType: types.ResponseTypeAnswer, Content: "nk:6124c78e>## 进展\n\n"},
		{ResponseType: types.ResponseTypeAnswer, Content: "真正的完整答案。", Done: true, FinishReason: "stop"},
	}}
}

func TestOrphanThinkAnswerReplacementMatchesPersistedRound(t *testing.T) {
	model := &mockChat{responses: []mockResponse{orphanThinkResponse()}}
	e := newTestEngine(t, model)
	e.toolRegistry = agenttools.NewToolRegistry()
	var answer string
	var replacements int
	var progress []string
	e.eventBus.On(event.EventAgentFinalAnswer, func(_ context.Context, evt event.Event) error {
		d := evt.Data.(event.AgentFinalAnswerData)
		if d.Replace {
			require.Contains(t, answer, "refs")
			answer = ""
			replacements++
		}
		answer += d.Content
		return nil
	})
	e.eventBus.On(event.EventAgentProgress, func(_ context.Context, evt event.Event) error {
		progress = append(progress, evt.Data.(event.AgentProgressData).Content)
		return nil
	})
	state, err := e.Execute(context.Background(), "s", "m", "report", nil, nil)
	require.NoError(t, err)
	require.Equal(t, 1, replacements)
	require.Equal(t, "## 进展\n\n真正的完整答案。", answer)
	require.Equal(t, answer, state.FinalAnswer)
	require.Len(t, state.RoundSteps, 1)
	require.Equal(t, "refs\n\n任务全部结束，先整理证据。Prepare complete summary.", state.RoundSteps[0].ReasoningContent)
	require.NotContains(t, strings.Join(progress, ""), "Prepare complete summary")
}

func TestOrphanThinkFinalSynthesisReplacesSameAnswer(t *testing.T) {
	e := newTestEngine(t, &mockChat{responses: []mockResponse{orphanThinkResponse()}})
	var answer string
	var replacementID string
	e.eventBus.On(event.EventAgentFinalAnswer, func(_ context.Context, evt event.Event) error {
		d := evt.Data.(event.AgentFinalAnswerData)
		if d.Replace {
			answer = ""
			replacementID = evt.ID
		}
		if replacementID != "" {
			require.Equal(t, replacementID, evt.ID)
		}
		answer += d.Content
		return nil
	})
	state := &types.AgentState{}
	err := e.streamFinalAnswerToEventBus(context.Background(), "report", state, "s", nil)
	require.NoError(t, err)
	require.NotEmpty(t, replacementID)
	require.Equal(t, "## 进展\n\n真正的完整答案。", answer)
	require.Equal(t, answer, state.FinalAnswer)
}

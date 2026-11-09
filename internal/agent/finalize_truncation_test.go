package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/collaboration"
	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Exhausting the ReAct round budget starts a tool-free synthesis. A completion
// cap there is still a partial answer, both in the live stream and after reload.
func TestFinalSynthesisPreservesTruncationInStreamAndHistory(t *testing.T) {
	for _, role := range []string{"ordinary", "parent", "child"} {
		for _, reason := range []string{"length", "max_tokens", "stop"} {
			t.Run(role+"/"+reason, func(t *testing.T) {
				const answer = "The evidence supports the following partial report"
				model := &mockChat{responses: []mockResponse{{chunks: []types.StreamResponse{
					{ResponseType: types.ResponseTypeAnswer, Content: answer},
					{ResponseType: types.ResponseTypeAnswer, FinishReason: reason, Done: true},
				}}}}
				engine := newTestEngine(t, model, withMaxIterations(1))
				if role != "ordinary" {
					engine.collaboration = &collaboration.Binding{}
					if role == "child" {
						engine.collaboration.Child = &collaboration.Work{}
					}
				}
				recorder := &truncationRecorder{}
				recorder.attach(engine.eventBus)
				prior := types.AgentStep{Iteration: 0, ToolCalls: []types.ToolCall{{
					ID: "search", Name: "search", Result: &types.ToolResult{Success: true, Output: "Evidence"},
				}}}
				state := &types.AgentState{CurrentRound: 1, RoundSteps: []types.AgentStep{prior}}
				err := engine.handleMaxIterations(t.Context(), "Write a report", state, "session", emptyMessages())
				require.NoError(t, err)
				require.True(t, state.IsComplete)
				require.Equal(t, answer, state.FinalAnswer)
				require.Equal(t, 1, model.callCount, "reporting truncation must not start a continuation")

				truncated := reason != "stop"
				assert.Equal(t, truncated, recorder.doneEvent(t).Truncated, "live clients need the partial-answer flag")
				terminalCount := 0
				for _, emitted := range recorder.snapshot() {
					if emitted.Done {
						terminalCount++
					}
				}
				assert.Equal(t, 1, terminalCount)

				var completed event.AgentCompleteData
				engine.eventBus.On(event.EventAgentComplete, func(_ context.Context, evt event.Event) error {
					completed = evt.Data.(event.AgentCompleteData)
					return nil
				})
				engine.emitCompletionEvent(t.Context(), state, "session", "message", time.Now())
				steps := completed.AgentSteps.([]types.AgentStep)
				stored := agenttools.SanitizeAgentStepsForStorage(steps)
				data, err := json.Marshal(stored)
				require.NoError(t, err)
				var reloaded []types.AgentStep
				require.NoError(t, json.Unmarshal(data, &reloaded))
				wantSteps := 1
				if truncated {
					wantSteps++
				}
				assert.Len(t, reloaded, wantSteps, "keep existing tool steps and record synthesis at most once")
				truncatedSteps := 0
				for _, step := range reloaded {
					if step.Truncated {
						truncatedSteps++
						assert.Equal(t, answer, step.Thought)
					}
				}
				if truncated {
					assert.Equal(t, 1, truncatedSteps, "history must retain one partial synthesis marker")
				} else {
					assert.Zero(t, truncatedSteps, "a complete synthesis is not truncated")
				}
				assert.Equal(t, prior, reloaded[0], "the preceding tool round must stay intact")
			})
		}
	}
}

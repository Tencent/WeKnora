package agent

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestMaxIterationsRejectsEmptyFinalSynthesis(t *testing.T) {
	for _, content := range []string{"", " \n\t", "<think>internal reasoning only</think>"} {
		t.Run(content, func(t *testing.T) {
			model := &finalSynthesisChat{}
			model.synthesize = func(
				context.Context, []chat.Message, *chat.ChatOptions,
			) (<-chan types.StreamResponse, error) {
				return finalSynthesisChunks(types.StreamResponse{
					ResponseType: types.ResponseTypeAnswer, Content: content, FinishReason: "stop", Done: true,
				}), nil
			}
			engine := newTestEngine(t, model, withMaxIterations(1))
			state := &types.AgentState{CurrentRound: 1, RoundSteps: []types.AgentStep{{Thought: "Collected evidence"}}}
			_, err := engine.executeLoop(t.Context(), state, "Summarize", emptyMessages(), nil, "session", "message")
			require.Error(t, err, "an empty final synthesis must not be reported as a successful answer")
			require.NotEmpty(t, state.FinalAnswer,
				"the existing synthesis error path must preserve a visible failure explanation")
		})
	}
}

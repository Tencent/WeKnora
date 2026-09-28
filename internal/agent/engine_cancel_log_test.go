package agent

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cancelWarningLine returns the "[Agent] Context cancelled at round" line the
// cancel branch emits, failing the test when the branch logged nothing at all.
func cancelWarningLine(t *testing.T, logs string) string {
	t.Helper()
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, "Context cancelled at round") {
			return line
		}
	}
	require.Failf(t, "cancel warning was not logged", "captured logs:\n%s", logs)
	return ""
}

// TestExecuteLoop_CancelLogDoesNotClaimToollessSynthesis pins the wording of the
// cancel branch's warning: it may say that the remaining tool calls are skipped,
// but it must not announce a toolless final answer.
//
// The branch returns right after a salvage attempt that reuses the
// already-cancelled ctx. With no tool results nothing is synthesized at all, and
// with tool results the fallback LLM call dies on that same dead ctx — which is
// exactly why the loop skips handleMaxIterations when ctx.Err() != nil. So "the
// final answer below is synthesized without tools" describes an answer that
// normally never exists: the misleading-log class this change exists to remove.
func TestExecuteLoop_CancelLogDoesNotClaimToollessSynthesis(t *testing.T) {
	// The salvage attempt runs on a dead context, so the provider layer reports
	// the cancellation instead of answer content.
	salvageFails := []mockResponse{{
		chunks: []types.StreamResponse{{
			ResponseType: types.ResponseTypeError,
			Content:      "context canceled",
		}},
	}}

	tests := []struct {
		name         string
		steps        []types.AgentStep
		responses    []mockResponse
		wantLLMCalls int
	}{
		{
			name:         "no tool results: nothing can be synthesized",
			wantLLMCalls: 0,
		},
		{
			name: "tool results present: salvage runs on the dead ctx and fails",
			steps: []types.AgentStep{{
				Iteration: 1,
				ToolCalls: []types.ToolCall{{ID: "call-1", Name: "wiki_search"}},
			}},
			responses:    salvageFails,
			wantLLMCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger.SetOutput(&buf)
			t.Cleanup(func() { logger.SetOutput(os.Stdout) })

			model := &mockChat{responses: tt.responses}
			engine := newTestEngine(t, model)
			state := &types.AgentState{RoundSteps: tt.steps}

			ctx, cancel := context.WithCancel(context.Background())
			cancel() // the user pressed stop before the round began

			_, err := engine.executeLoop(ctx, state, "test query", emptyMessages(), emptyTools(),
				"sess-1", "msg-1")
			require.ErrorIs(t, err, context.Canceled)

			assert.Equal(t, tt.wantLLMCalls, model.callCount,
				"only the salvage branch reaches the model, and it does so once")
			assert.Empty(t, state.FinalAnswer,
				"the cancelled ctx prevents the salvage synthesis from producing an answer")

			line := cancelWarningLine(t, buf.String())
			t.Logf("cancel warning: %s", line)
			assert.Contains(t, line, "remaining tool calls will not run",
				"the warning must still tell the operator that pending tool calls are dropped")
			for _, claim := range []string{
				"synthesized without tools",
				"final answer below",
				"final answer is synthesized",
			} {
				assert.NotContains(t, line, claim,
					"the cancel warning must not announce a final answer this branch does not produce:\n%s",
					line)
			}
		})
	}
}
